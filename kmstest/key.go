package kmstest

import (
	"context"
	"runtime/debug"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/ln80/aws-toolkit-go/internal/testlog"
)

func WithKey(t *testing.T, svc *kms.Client, tfn func(svc *kms.Client, key string)) {
	ctx := context.Background()

	out, err := svc.CreateKey(ctx, &kms.CreateKeyInput{})
	if err != nil {
		testlog.Fatal(t, "failed to create kms test key %v", err)
	}

	kmsKey := aws.ToString(out.KeyMetadata.KeyId)

	testlog.Info(t, "kms test key created: %s", kmsKey)

	func() {
		t.Helper()
		defer func() {
			t.Helper()
			if r := recover(); r != nil {
				testlog.Fatal(t, "%d: test panic: %v", r, string(debug.Stack()))
			}
		}()
		tfn(svc, kmsKey)
	}()
}
