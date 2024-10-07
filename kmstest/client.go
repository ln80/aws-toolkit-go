package kmstest

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

func LocalClient(t *testing.T, endpoint string) *kms.Client {
	isTestHelper := t != nil
	if isTestHelper {
		t.Helper()
	}

	fatal := func(v any) {
		if isTestHelper {
			t.Helper()
			t.Fatal(v)
		}
		panic(v)
	}

	if endpoint == "" {
		fatal(errors.New("empty kms local endpoint"))
	}

	cfg, err := config.LoadDefaultConfig(
		context.Background(),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("TEST", "TEST", "TEST"),
		),
	)
	if err != nil {
		fatal(err)
	}

	dbsvc := kms.NewFromConfig(cfg, func(o *kms.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	return dbsvc
}
