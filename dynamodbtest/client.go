package dynamodbtest

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func LocalClient(endpoint string, ts ...*testing.T) *dynamodb.Client {
	isTestHelper := len(ts) > 0 && ts[0] != nil
	var t *testing.T
	if isTestHelper {
		t = ts[0]
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
		fatal(errors.New("empty dynamodb local endpoint"))
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

	dbsvc := dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})

	return dbsvc
}
