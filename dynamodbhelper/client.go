package dynamodbhelper

import (
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// NewClient builds a DynamoDB client with capacity-tracking middleware attached.
func NewClient(cfg aws.Config, optFns ...func(*dynamodb.Options)) *dynamodb.Client {
	AppendCapacityMiddleware(&cfg.APIOptions)
	return dynamodb.NewFromConfig(cfg, optFns...)
}
