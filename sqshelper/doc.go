// Package sqshelper provides a production-ready FIFO SQS queue consumer for Go
// using AWS SDK v2.
//
// The consumer preserves strict ordering within each MessageGroupId while
// processing different groups in parallel up to a configurable worker limit.
//
// Delivery semantics are at-least-once: handlers must be idempotent. On handler
// success the message is deleted; on handler error the message is left on the
// queue and becomes visible again after the queue visibility timeout.
//
// Producers should set MessageGroupId (typically aggregate_id) and
// MessageDeduplicationId (typically event_id) when sending to FIFO queues.
//
// Example:
//
//	consumer, err := sqshelper.New(sqsClient, sqshelper.Config{
//	    QueueURL:      queueURL,
//	    MaxWorkers:    100,
//	    WorkerIdleTTL: time.Minute,
//	}, func(ctx context.Context, msg types.Message) error {
//	    return processEvent(ctx, []byte(aws.ToString(msg.Body)))
//	})
//	if err != nil {
//	    return err
//	}
//	return consumer.Run(ctx)
package sqshelper
