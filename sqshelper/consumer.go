package sqshelper

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// New creates a FIFO queue consumer.
func New(client *sqs.Client, cfg Config, handler Handler) (*Consumer, error) {
	if client == nil {
		return nil, errors.New("sqs client is required")
	}
	if handler == nil {
		return nil, ErrMissingHandler
	}

	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &Consumer{
		client:     client,
		cfg:        cfg,
		handler:    handler,
		workers:    make(map[string]*groupWorker),
		workerSlot: make(chan struct{}, cfg.MaxWorkers),
	}, nil
}

// Run polls the queue until ctx is cancelled, then shuts down workers gracefully.
func (c *Consumer) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c.wg.Add(2)
	go c.pollLoop(ctx)
	go c.monitorLoop(ctx)

	c.wg.Wait()
	return nil
}

func (c *Consumer) pollLoop(ctx context.Context) {
	defer c.wg.Done()

	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}

		msgs, err := c.receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !sleepWithContext(ctx, backoff+jitter(backoff/2)) {
				return
			}
			if backoff < 10*time.Second {
				backoff += time.Second
			}
			continue
		}
		backoff = time.Second

		for _, msg := range msgs {
			if ctx.Err() != nil {
				return
			}
			if err := c.dispatch(ctx, msg); err != nil {
				if ctx.Err() != nil {
					return
				}
			}
		}
	}
}

func (c *Consumer) receive(ctx context.Context) ([]types.Message, error) {
	out, err := c.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(c.cfg.QueueURL),
		MaxNumberOfMessages: defaultMaxMessages,
		WaitTimeSeconds:     defaultReceiveWait,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{
			types.MessageSystemAttributeNameMessageGroupId,
		},
	})
	if err != nil {
		return nil, err
	}
	return out.Messages, nil
}

func (c *Consumer) dispatch(ctx context.Context, msg types.Message) error {
	groupID := msg.Attributes[attrMessageGroupID]
	if groupID == "" {
		return fmt.Errorf("message missing %s attribute", attrMessageGroupID)
	}

	worker, err := c.ensureWorker(ctx, groupID)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case worker.msgs <- msg:
		c.touchWorker(worker)
		return nil
	}
}

func (c *Consumer) ensureWorker(ctx context.Context, groupID string) (*groupWorker, error) {
	c.workerMu.RLock()
	worker, ok := c.workers[groupID]
	c.workerMu.RUnlock()
	if ok {
		return worker, nil
	}

	select {
	case c.workerSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	c.workerMu.Lock()
	defer c.workerMu.Unlock()

	if worker, ok = c.workers[groupID]; ok {
		<-c.workerSlot
		return worker, nil
	}

	workerCtx, cancel := context.WithCancel(ctx)
	worker = &groupWorker{
		groupID:      groupID,
		msgs:         make(chan types.Message, defaultMsgBufferSize),
		lastActiveAt: time.Now(),
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	c.workers[groupID] = worker

	c.wg.Add(1)
	go c.runWorker(workerCtx, worker)

	return worker, nil
}

func (c *Consumer) touchWorker(worker *groupWorker) {
	c.workerMu.Lock()
	worker.lastActiveAt = time.Now()
	c.workerMu.Unlock()
}

func (c *Consumer) runWorker(ctx context.Context, worker *groupWorker) {
	defer c.wg.Done()
	defer close(worker.done)

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-worker.msgs:
			if !ok {
				return
			}
			c.processMessage(ctx, worker, msg)
		}
	}
}

func (c *Consumer) processMessage(ctx context.Context, worker *groupWorker, msg types.Message) {
	c.touchWorker(worker)

	if ctx.Err() != nil {
		return
	}

	err := c.invokeHandler(ctx, msg)
	if err != nil {
		return
	}

	if err := deleteMessageWithRetry(ctx, c.client, c.cfg.QueueURL, msg); err != nil && ctx.Err() == nil {
		_ = err
	}
}

func (c *Consumer) invokeHandler(ctx context.Context, msg types.Message) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("handler panic: %v", r)
		}
	}()
	return c.handler(ctx, msg)
}

func (c *Consumer) monitorLoop(ctx context.Context) {
	defer c.wg.Done()

	ticker := time.NewTicker(c.cfg.MonitorPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.shutdownWorkers()
			return
		case <-ticker.C:
			c.cleanupIdleWorkers()
		}
	}
}

func (c *Consumer) cleanupIdleWorkers() {
	now := time.Now()
	var toStop []*groupWorker

	c.workerMu.Lock()
	for groupID, worker := range c.workers {
		if now.Sub(worker.lastActiveAt) <= c.cfg.WorkerIdleTTL {
			continue
		}
		if len(worker.msgs) > 0 {
			continue
		}
		delete(c.workers, groupID)
		toStop = append(toStop, worker)
	}
	c.workerMu.Unlock()

	for _, worker := range toStop {
		c.stopWorker(worker)
	}
}

func (c *Consumer) shutdownWorkers() {
	c.workerMu.Lock()
	workers := make([]*groupWorker, 0, len(c.workers))
	for _, worker := range c.workers {
		workers = append(workers, worker)
	}
	c.workers = make(map[string]*groupWorker)
	c.workerMu.Unlock()

	var wg sync.WaitGroup
	for _, worker := range workers {
		wg.Add(1)
		go func(w *groupWorker) {
			defer wg.Done()
			c.stopWorker(w)
		}(worker)
	}
	wg.Wait()
}

func (c *Consumer) stopWorker(worker *groupWorker) {
	worker.cancel()
	<-worker.done
	<-c.workerSlot
}

func jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(int64(max)))
}
