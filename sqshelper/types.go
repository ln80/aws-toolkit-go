package sqshelper

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	attrMessageGroupID = "MessageGroupId"

	defaultMaxWorkers    = 100
	defaultWorkerIdleTTL = time.Minute
	defaultMsgBufferSize = 64
	defaultMonitorPeriod = 30 * time.Second
	defaultReceiveWait   = 20
	defaultMaxMessages   = 10
)

var (
	ErrInvalidQueueURL   = errors.New("queue URL is required")
	ErrInvalidMaxWorkers = errors.New("max workers must be greater than zero")
	ErrInvalidIdleTTL    = errors.New("worker idle TTL must be greater than zero")
	ErrMissingHandler    = errors.New("handler is required")
)

// Handler processes a single SQS message. Implementations must be idempotent
// because delivery is at-least-once.
type Handler func(ctx context.Context, msg types.Message) error

// Config configures a FIFO queue consumer.
type Config struct {
	// QueueURL is the FIFO queue URL.
	QueueURL string

	// MaxWorkers limits concurrent group workers across all MessageGroupIds.
	MaxWorkers int

	// WorkerIdleTTL is how long a group worker may remain idle before termination.
	WorkerIdleTTL time.Duration

	// MonitorPeriod controls how often idle workers are inspected. When zero,
	// a default of 30 seconds is used.
	MonitorPeriod time.Duration
}

func (c Config) withDefaults() Config {
	if c.MaxWorkers <= 0 {
		c.MaxWorkers = defaultMaxWorkers
	}
	if c.WorkerIdleTTL <= 0 {
		c.WorkerIdleTTL = defaultWorkerIdleTTL
	}
	if c.MonitorPeriod <= 0 {
		c.MonitorPeriod = defaultMonitorPeriod
	}
	return c
}

func (c Config) validate() error {
	if c.QueueURL == "" {
		return ErrInvalidQueueURL
	}
	if c.MaxWorkers <= 0 {
		return ErrInvalidMaxWorkers
	}
	if c.WorkerIdleTTL <= 0 {
		return ErrInvalidIdleTTL
	}
	return nil
}

type sqsAPI interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	DeleteMessageBatch(ctx context.Context, params *sqs.DeleteMessageBatchInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageBatchOutput, error)
}

// Consumer polls a FIFO SQS queue and dispatches messages to per-group workers.
type Consumer struct {
	client  sqsAPI
	cfg     Config
	handler Handler

	workers    map[string]*groupWorker
	workerMu   sync.RWMutex
	workerSlot chan struct{}

	wg sync.WaitGroup
}

type groupWorker struct {
	groupID      string
	msgs         chan types.Message
	lastActiveAt time.Time
	cancel       context.CancelFunc
	done         chan struct{}
}
