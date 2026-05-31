package sqshelper

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type mockSQSClient struct {
	mu sync.Mutex

	receiveCalls int
	receiveFn    func(ctx context.Context, in *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error)

	deletedMu sync.Mutex
	deleted   []string

	deleteErr error
}

func (m *mockSQSClient) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, _ ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	m.mu.Lock()
	m.receiveCalls++
	fn := m.receiveFn
	m.mu.Unlock()

	if fn == nil {
		return &sqs.ReceiveMessageOutput{}, nil
	}
	return fn(ctx, in)
}

func (m *mockSQSClient) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	m.deletedMu.Lock()
	m.deleted = append(m.deleted, aws.ToString(in.ReceiptHandle))
	m.deletedMu.Unlock()
	return &sqs.DeleteMessageOutput{}, nil
}

func (m *mockSQSClient) DeleteMessageBatch(ctx context.Context, in *sqs.DeleteMessageBatchInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	if m.deleteErr != nil {
		return nil, m.deleteErr
	}
	m.deletedMu.Lock()
	for _, entry := range in.Entries {
		m.deleted = append(m.deleted, aws.ToString(entry.ReceiptHandle))
	}
	m.deletedMu.Unlock()
	return &sqs.DeleteMessageBatchOutput{}, nil
}

func (m *mockSQSClient) deletedHandles() []string {
	m.deletedMu.Lock()
	defer m.deletedMu.Unlock()
	out := append([]string(nil), m.deleted...)
	return out
}

func fifoMessage(groupID, body, receiptHandle string) types.Message {
	return types.Message{
		Body:          aws.String(body),
		ReceiptHandle: aws.String(receiptHandle),
		Attributes: map[string]string{
			attrMessageGroupID: groupID,
		},
	}
}

func newTestConsumer(t *testing.T, client sqsAPI, cfg Config, handler Handler) *Consumer {
	t.Helper()

	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		t.Fatalf("invalid config: %v", err)
	}
	if handler == nil {
		t.Fatal("handler is required")
	}

	return &Consumer{
		client:     client,
		cfg:        cfg,
		handler:    handler,
		workers:    make(map[string]*groupWorker),
		workerSlot: make(chan struct{}, cfg.MaxWorkers),
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()

	_, err := New(nil, Config{QueueURL: "https://example.com/queue.fifo"}, func(context.Context, types.Message) error { return nil })
	if err == nil {
		t.Fatal("expected error for nil client")
	}

	_, err = New(&sqs.Client{}, Config{QueueURL: ""}, func(context.Context, types.Message) error { return nil })
	if !errors.Is(err, ErrInvalidQueueURL) {
		t.Fatalf("expected ErrInvalidQueueURL, got %v", err)
	}

	_, err = New(&sqs.Client{}, Config{QueueURL: "https://example.com/queue.fifo", MaxWorkers: 0}, func(context.Context, types.Message) error { return nil })
	if err != nil {
		t.Fatalf("expected default max workers to pass validation, got %v", err)
	}

	_, err = New(&sqs.Client{}, Config{QueueURL: "https://example.com/queue.fifo"}, nil)
	if !errors.Is(err, ErrMissingHandler) {
		t.Fatalf("expected ErrMissingHandler, got %v", err)
	}
}

func TestProcessesSameGroupInOrder(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	msgs := []types.Message{
		fifoMessage("group-a", "1", "rh-1"),
		fifoMessage("group-a", "2", "rh-2"),
		fifoMessage("group-a", "3", "rh-3"),
	}

	mock.receiveFn = func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if len(msgs) == 0 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		batch := msgs
		msgs = nil
		return &sqs.ReceiveMessageOutput{Messages: batch}, nil
	}

	var order []string
	var orderMu sync.Mutex
	processed := make(chan struct{}, 3)

	consumer := newTestConsumer(t, mock, Config{
		QueueURL:      "https://example.com/queue.fifo",
		MaxWorkers:    4,
		WorkerIdleTTL: time.Minute,
	}, func(_ context.Context, msg types.Message) error {
		orderMu.Lock()
		order = append(order, aws.ToString(msg.Body))
		orderMu.Unlock()
		processed <- struct{}{}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = consumer.Run(ctx)
		close(done)
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-processed:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for message processing")
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not stop")
	}

	if got, want := order, []string{"1", "2", "3"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestMaxWorkersEnforced(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	var active int32
	var maxActive int32

	groups := []string{"g1", "g2", "g3", "g4", "g5"}
	var msgs []types.Message
	for i, g := range groups {
		msgs = append(msgs, fifoMessage(g, fmt.Sprintf("body-%d", i), fmt.Sprintf("rh-%d", i)))
	}

	mock.receiveFn = func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if len(msgs) == 0 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		batch := msgs
		msgs = nil
		return &sqs.ReceiveMessageOutput{Messages: batch}, nil
	}

	consumer := newTestConsumer(t, mock, Config{
		QueueURL:      "https://example.com/queue.fifo",
		MaxWorkers:    2,
		WorkerIdleTTL: time.Minute,
	}, func(_ context.Context, _ types.Message) error {
		cur := atomic.AddInt32(&active, 1)
		for {
			prev := atomic.LoadInt32(&maxActive)
			if cur <= prev || atomic.CompareAndSwapInt32(&maxActive, prev, cur) {
				break
			}
		}
		started <- struct{}{}
		<-release
		atomic.AddInt32(&active, -1)
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = consumer.Run(ctx) }()

	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for initial workers")
		}
	}

	select {
	case <-started:
		t.Fatalf("max active workers exceeded limit before third worker could start: max=%d", atomic.LoadInt32(&maxActive))
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	time.Sleep(200 * time.Millisecond)

	if got := atomic.LoadInt32(&maxActive); got > 2 {
		t.Fatalf("max active workers = %d, want <= 2", got)
	}

	cancel()
}

func TestHandlerErrorDoesNotDelete(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	delivered := false
	mock.receiveFn = func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if !delivered {
			delivered = true
			return &sqs.ReceiveMessageOutput{
				Messages: []types.Message{
					fifoMessage("group-a", "fail", "rh-fail"),
				},
			}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	processed := make(chan struct{}, 1)
	consumer := newTestConsumer(t, mock, Config{
		QueueURL:      "https://example.com/queue.fifo",
		MaxWorkers:    1,
		WorkerIdleTTL: time.Minute,
	}, func(_ context.Context, _ types.Message) error {
		select {
		case processed <- struct{}{}:
		default:
		}
		return errors.New("boom")
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	time.Sleep(100 * time.Millisecond)
	cancel()

	if deleted := mock.deletedHandles(); len(deleted) != 0 {
		t.Fatalf("expected no deletes, got %v", deleted)
	}
}

func TestSuccessDeletesMessage(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	delivered := false
	mock.receiveFn = func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if !delivered {
			delivered = true
			return &sqs.ReceiveMessageOutput{
				Messages: []types.Message{
					fifoMessage("group-a", "ok", "rh-ok"),
				},
			}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	processed := make(chan struct{}, 1)
	consumer := newTestConsumer(t, mock, Config{
		QueueURL:      "https://example.com/queue.fifo",
		MaxWorkers:    1,
		WorkerIdleTTL: time.Minute,
	}, func(_ context.Context, _ types.Message) error {
		select {
		case processed <- struct{}{}:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	time.Sleep(100 * time.Millisecond)
	cancel()

	deleted := mock.deletedHandles()
	if len(deleted) != 1 || deleted[0] != "rh-ok" {
		t.Fatalf("deleted = %v, want [rh-ok]", deleted)
	}
}

func TestIdleWorkersAreRemoved(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	delivered := false
	mock.receiveFn = func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		if !delivered {
			delivered = true
			return &sqs.ReceiveMessageOutput{
				Messages: []types.Message{
					fifoMessage("group-a", "one", "rh-one"),
				},
			}, nil
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	processed := make(chan struct{}, 1)
	consumer := newTestConsumer(t, mock, Config{
		QueueURL:      "https://example.com/queue.fifo",
		MaxWorkers:    1,
		WorkerIdleTTL: 50 * time.Millisecond,
		MonitorPeriod: 20 * time.Millisecond,
	}, func(_ context.Context, _ types.Message) error {
		select {
		case processed <- struct{}{}:
		default:
		}
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = consumer.Run(ctx) }()

	select {
	case <-processed:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		consumer.workerMu.RLock()
		count := len(consumer.workers)
		consumer.workerMu.RUnlock()
		if count == 0 {
			cancel()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	t.Fatalf("expected idle worker cleanup, still have %d workers", len(consumer.workers))
}

func TestRunStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	mock.receiveFn = func(ctx context.Context, _ *sqs.ReceiveMessageInput) (*sqs.ReceiveMessageOutput, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	consumer := newTestConsumer(t, mock, Config{
		QueueURL:      "https://example.com/queue.fifo",
		MaxWorkers:    1,
		WorkerIdleTTL: time.Minute,
	}, func(context.Context, types.Message) error { return nil })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = consumer.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestDeleteMessageBatchWithRetry(t *testing.T) {
	t.Parallel()

	mock := &mockSQSClient{}
	attempts := 0
	mock.deleteErr = errors.New("temporary")

	origDeleteBatch := mock.DeleteMessageBatch
	_ = origDeleteBatch

	client := &retryBatchClient{
		inner: mock,
		onBatch: func() error {
			attempts++
			if attempts < 2 {
				return errors.New("temporary")
			}
			mock.deleteErr = nil
			return nil
		},
	}

	msgs := []types.Message{
		fifoMessage("g", "body", "rh-1"),
		fifoMessage("g", "body", "rh-2"),
	}

	err := deleteMessageBatchWithRetry(context.Background(), client, "https://example.com/queue.fifo", msgs)
	if err != nil {
		t.Fatalf("deleteMessageBatchWithRetry() error = %v", err)
	}
	if got := mock.deletedHandles(); len(got) != 2 {
		t.Fatalf("deleted = %v, want 2 handles", got)
	}
}

type retryBatchClient struct {
	inner   *mockSQSClient
	onBatch func() error
}

func (c *retryBatchClient) ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return c.inner.ReceiveMessage(ctx, in, optFns...)
}

func (c *retryBatchClient) DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return c.inner.DeleteMessage(ctx, in, optFns...)
}

func (c *retryBatchClient) DeleteMessageBatch(ctx context.Context, in *sqs.DeleteMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error) {
	if err := c.onBatch(); err != nil {
		return nil, err
	}
	return c.inner.DeleteMessageBatch(ctx, in, optFns...)
}
