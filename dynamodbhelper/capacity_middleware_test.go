package dynamodbhelper

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/expression"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ln80/aws-toolkit-go/dynamodbtest"
)

func localClientWithCapacity(t *testing.T) *dynamodb.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(
		context.Background(),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("TEST", "TEST", "TEST"),
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	AppendCapacityMiddleware(&cfg.APIOptions)
	return dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestCapacityMiddleware(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping capacity middleware integration test")
	}
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set")
	}

	dynamodbtest.WithTables(t, dynamodbtest.LocalClient(t, endpoint), dynamodbtest.TableConfig{
		TableList: dynamodbtest.TableList(
			CreateTableInput("capacity-table-a"),
			CreateTableInput("capacity-table-b"),
		),
	}, func(_ *dynamodb.Client, tableNames []string) {
		tableA, tableB := tableNames[0], tableNames[1]
		dbsvc := localClientWithCapacity(t)
		ctx := context.Background()

		t.Run("no capacity context is no-op", func(t *testing.T) {
			_, err := dbsvc.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(tableA),
				Item: map[string]types.AttributeValue{
					HashKey:  &types.AttributeValueMemberS{Value: "k1"},
					RangeKey: &types.AttributeValueMemberS{Value: "r1"},
				},
			})
			if err != nil {
				t.Fatalf("put item: %v", err)
			}
			if cc := CapacityFromContext(ctx); cc != nil {
				t.Fatal("expected no capacity context")
			}
		})

		t.Run("aggregates reads and writes across tables", func(t *testing.T) {
			ctx, cc := CapacityContext(ctx)

			_, err := dbsvc.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(tableA),
				Item: map[string]types.AttributeValue{
					HashKey:  &types.AttributeValueMemberS{Value: "k2"},
					RangeKey: &types.AttributeValueMemberS{Value: "r1"},
				},
			})
			if err != nil {
				t.Fatalf("put item table a: %v", err)
			}

			_, err = dbsvc.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(tableB),
				Item: map[string]types.AttributeValue{
					HashKey:  &types.AttributeValueMemberS{Value: "k3"},
					RangeKey: &types.AttributeValueMemberS{Value: "r1"},
				},
			})
			if err != nil {
				t.Fatalf("put item table b: %v", err)
			}

			expr, err := expression.NewBuilder().
				WithKeyCondition(expression.Key(HashKey).Equal(expression.Value("k2"))).
				Build()
			if err != nil {
				t.Fatalf("build expression: %v", err)
			}
			_, err = dbsvc.Query(ctx, &dynamodb.QueryInput{
				TableName:                 aws.String(tableA),
				KeyConditionExpression:    expr.KeyCondition(),
				ExpressionAttributeNames:  expr.Names(),
				ExpressionAttributeValues: expr.Values(),
				ConsistentRead:            aws.Bool(true),
			})
			if err != nil {
				t.Fatalf("query table a: %v", err)
			}

			if cc.IsZero() {
				t.Fatal("expected consumed capacity to be recorded")
			}
			if cc.Total <= 0 {
				t.Fatalf("expected total capacity > 0, got %v", cc.Total)
			}
			if len(cc.Tables) < 2 {
				t.Fatalf("expected per-table breakdown for both tables, got %v", cc.Tables)
			}
			if _, ok := cc.Tables[tableA]; !ok {
				t.Fatalf("expected table a %q in breakdown", tableA)
			}
			if _, ok := cc.Tables[tableB]; !ok {
				t.Fatalf("expected table b %q in breakdown", tableB)
			}
		})

		t.Run("preserves caller ReturnConsumedCapacity", func(t *testing.T) {
			ctx, cc := CapacityContext(ctx)
			input := &dynamodb.GetItemInput{
				TableName: aws.String(tableA),
				Key: map[string]types.AttributeValue{
					HashKey:  &types.AttributeValueMemberS{Value: "k2"},
					RangeKey: &types.AttributeValueMemberS{Value: "r1"},
				},
				ReturnConsumedCapacity: types.ReturnConsumedCapacityTotal,
			}
			out, err := dbsvc.GetItem(ctx, input)
			if err != nil {
				t.Fatalf("get item: %v", err)
			}
			if input.ReturnConsumedCapacity != types.ReturnConsumedCapacityTotal {
				t.Fatalf("expected ReturnConsumedCapacity TOTAL, got %q", input.ReturnConsumedCapacity)
			}
			if out.ConsumedCapacity == nil {
				t.Fatal("expected consumed capacity in response")
			}
			if cc.Total <= 0 {
				t.Fatalf("expected total capacity > 0, got %v", cc.Total)
			}
		})
	})
}

func TestCapacityMiddleware_SessionUsesContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping session capacity integration test")
	}
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set")
	}

	dynamodbtest.WithTables(t, dynamodbtest.LocalClient(t, endpoint), dynamodbtest.TableConfig{
		TableList: dynamodbtest.TableList(CreateTableInput("session-capacity-table")),
	}, func(_ *dynamodb.Client, tableNames []string) {
		table := tableNames[0]
		dbsvc := localClientWithCapacity(t)
		ctx, cc := CapacityContext(context.Background())
		ses := NewSession(dbsvc)

		if err := ses.Put(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(table),
			Item: map[string]types.AttributeValue{
				HashKey:  &types.AttributeValueMemberS{Value: "sess"},
				RangeKey: &types.AttributeValueMemberS{Value: "1"},
			},
		}); err != nil {
			t.Fatalf("session put: %v", err)
		}

		if cc.IsZero() {
			t.Fatal("expected capacity via context from session write")
		}
		if cc.Total <= 0 {
			t.Fatalf("expected total capacity > 0, got %v", cc.Total)
		}
		if ses.ConsumedCapacity().Total != 0 {
			t.Fatalf("expected session fallback accumulator to stay empty, got %v", ses.ConsumedCapacity().Total)
		}
	})
}
