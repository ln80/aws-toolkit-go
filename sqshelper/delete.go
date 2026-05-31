package sqshelper

import (
	"context"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	deleteRetryAttempts = 5
	deleteRetryBase     = 50 * time.Millisecond
)

func deleteMessage(ctx context.Context, client sqsAPI, queueURL string, msg types.Message) error {
	_, err := client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	})
	return err
}

func deleteMessageWithRetry(ctx context.Context, client sqsAPI, queueURL string, msg types.Message) error {
	var err error
	for attempt := 0; attempt < deleteRetryAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err = deleteMessage(ctx, client, queueURL, msg)
		if err == nil {
			return nil
		}

		sleep := deleteRetryBase * time.Duration(1<<attempt)
		jitter := time.Duration(rand.Int63n(int64(sleep / 2)))
		if !sleepWithContext(ctx, sleep+jitter) {
			return ctx.Err()
		}
	}
	return fmt.Errorf("delete message: %w", err)
}

func deleteMessageBatchWithRetry(ctx context.Context, client sqsAPI, queueURL string, msgs []types.Message) error {
	if len(msgs) == 0 {
		return nil
	}

	entries := make([]types.DeleteMessageBatchRequestEntry, len(msgs))
	for i, msg := range msgs {
		entries[i] = types.DeleteMessageBatchRequestEntry{
			Id:            aws.String(strconv.Itoa(i)),
			ReceiptHandle: msg.ReceiptHandle,
		}
	}

	var err error
	for attempt := 0; attempt < deleteRetryAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		out, callErr := client.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
			QueueUrl: aws.String(queueURL),
			Entries:  entries,
		})
		if callErr != nil {
			err = callErr
		} else if out == nil || len(out.Failed) == 0 {
			return nil
		} else {
			err = fmt.Errorf("batch delete failed entries: %v", out.Failed)
			entries = failedBatchEntries(out.Failed, msgs)
			if len(entries) == 0 {
				return err
			}
		}

		sleep := deleteRetryBase * time.Duration(1<<attempt)
		jitter := time.Duration(rand.Int63n(int64(sleep / 2)))
		if !sleepWithContext(ctx, sleep+jitter) {
			return ctx.Err()
		}
	}

	return err
}

func failedBatchEntries(failed []types.BatchResultErrorEntry, msgs []types.Message) []types.DeleteMessageBatchRequestEntry {
	retry := make([]types.DeleteMessageBatchRequestEntry, 0, len(failed))
	for _, f := range failed {
		idx, convErr := strconv.Atoi(aws.ToString(f.Id))
		if convErr != nil || idx < 0 || idx >= len(msgs) {
			continue
		}
		retry = append(retry, types.DeleteMessageBatchRequestEntry{
			Id:            f.Id,
			ReceiptHandle: msgs[idx].ReceiptHandle,
		})
	}
	return retry
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
