package dynamodbhelper

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go/middleware"
)

const capacityInitializeMiddlewareID = "DynamoDBCapacityInitialize"

// AppendCapacityMiddleware registers Smithy middleware on a DynamoDB client config
// that injects ReturnConsumedCapacity on requests and aggregates responses into
// the ConsumedCapacity stored in context via CapacityContext.
func AppendCapacityMiddleware(apiOptions *[]func(*middleware.Stack) error) {
	if apiOptions == nil {
		return
	}
	*apiOptions = append(*apiOptions, func(stack *middleware.Stack) error {
		return stack.Initialize.Add(capacityInitializeMiddleware(), middleware.Before)
	})
}

func capacityInitializeMiddleware() middleware.InitializeMiddleware {
	return middleware.InitializeMiddlewareFunc(capacityInitializeMiddlewareID,
		func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
			out middleware.InitializeOutput, metadata middleware.Metadata, err error,
		) {
			cc := CapacityFromContext(ctx)
			if cc != nil {
				injectReturnConsumedCapacity(in.Parameters)
			}
			out, metadata, err = next.HandleInitialize(ctx, in)
			if err != nil || cc == nil {
				return out, metadata, err
			}
			for _, raw := range extractConsumedCapacity(out.Result) {
				raw := raw
				AddConsumedCapacity(cc, &raw)
			}
			return out, metadata, err
		})
}

func injectReturnConsumedCapacity(in any) {
	switch v := in.(type) {
	case *dynamodb.BatchExecuteStatementInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.BatchGetItemInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.BatchWriteItemInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.DeleteItemInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.ExecuteStatementInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.ExecuteTransactionInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.GetItemInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.PutItemInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.QueryInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.ScanInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.TransactGetItemsInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.TransactWriteItemsInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	case *dynamodb.UpdateItemInput:
		setReturnConsumedCapacity(&v.ReturnConsumedCapacity)
	}
}

func setReturnConsumedCapacity(v *types.ReturnConsumedCapacity) {
	if *v == "" {
		*v = types.ReturnConsumedCapacityIndexes
	}
}

func extractConsumedCapacity(out any) []types.ConsumedCapacity {
	switch v := out.(type) {
	case *dynamodb.BatchExecuteStatementOutput:
		return v.ConsumedCapacity
	case *dynamodb.BatchGetItemOutput:
		return v.ConsumedCapacity
	case *dynamodb.BatchWriteItemOutput:
		return v.ConsumedCapacity
	case *dynamodb.DeleteItemOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	case *dynamodb.ExecuteStatementOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	case *dynamodb.ExecuteTransactionOutput:
		return v.ConsumedCapacity
	case *dynamodb.GetItemOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	case *dynamodb.PutItemOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	case *dynamodb.QueryOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	case *dynamodb.ScanOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	case *dynamodb.TransactGetItemsOutput:
		return v.ConsumedCapacity
	case *dynamodb.TransactWriteItemsOutput:
		return v.ConsumedCapacity
	case *dynamodb.UpdateItemOutput:
		return singleConsumedCapacity(v.ConsumedCapacity)
	default:
		return nil
	}
}

func singleConsumedCapacity(cc *types.ConsumedCapacity) []types.ConsumedCapacity {
	if cc == nil {
		return nil
	}
	return []types.ConsumedCapacity{*cc}
}
