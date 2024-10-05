package dynamodbtest

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime/debug"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TableParamList(inputs ...*dynamodb.CreateTableInput) []*dynamodb.CreateTableInput {
	return inputs
}

type TableConfig struct {
	Params     *dynamodb.CreateTableInput
	ParamList  []*dynamodb.CreateTableInput
	OmitSuffix bool
	KeepTable  bool
	MaxWait    time.Duration
}

func WithTable(t *testing.T, svc *dynamodb.Client, cfg TableConfig, fn func(dbsvc *dynamodb.Client, table string)) {
	t.Helper()

	ctx := context.Background()

	params := cfg.Params
	if params == nil {
		fatal(t, "create table params not found %v", params)
	}
	if cfg.MaxWait == 0 {
		cfg.MaxWait = time.Minute
	}
	tableName := aws.ToString(params.TableName)
	if tableName == "" {
		tableName = "test-table"
	}
	if !cfg.OmitSuffix || tableName == "" {
		tableName = addSuffix(tableName)
	}
	params.TableName = aws.String(tableName)

	if err := createTable(ctx, svc, params); err != nil {
		fatal(t, "failed to create test table '%s': %v", tableName, err)
	}
	if err := waitForTable(ctx, svc, aws.ToString(params.TableName), cfg.MaxWait); err != nil {
		fatal(t, "failed to create test table '%s': %v", tableName, err)
	}

	info(t, "dynamodb test table created: %s", tableName)

	func() {
		t.Helper()
		defer func() {
			t.Helper()
			if r := recover(); r != nil {
				fatal(t, "test panic: %v", r)
			}
		}()
		fn(svc, tableName)
	}()

	if cfg.KeepTable {
		return
	}

	if err := deleteTable(ctx, svc, tableName); err != nil {
		fatal(t, "failed to remove test table '%s': %v", tableName, err)
	}
	warn(t, "dynamodb test table deleted: %s", tableName)
}

func WithSharedTables(svc *dynamodb.Client, cfg TableConfig, fn func(dbsvc *dynamodb.Client, tables []string) int) (code int) {
	ctx := context.Background()

	if len(cfg.ParamList) == 0 {
		fatal(nil, "create table params list not found")
	}
	if cfg.MaxWait == 0 {
		cfg.MaxWait = time.Minute
	}

	tableNames := make([]string, len(cfg.ParamList))
	for i, params := range cfg.ParamList {

		tableName := aws.ToString(params.TableName)
		if tableName == "" {
			tableName = "test-table"
		}
		if !cfg.OmitSuffix || tableName == "" {
			tableName = addSuffix(tableName)
		}
		params.TableName = aws.String(tableName)

		tableNames[i] = tableName

		if err := createTable(ctx, svc, params); err != nil {
			fatal(nil, "%d: failed to create test table '%s': %v", i+1, tableName, err)
		}
		if err := waitForTable(ctx, svc, aws.ToString(params.TableName), cfg.MaxWait); err != nil {
			fatal(nil, "%d: failed to create test table '%s': %v", i+1, tableName, err)
		}

		info(nil, "%d: dynamodb test table created: %s", i+1, tableName)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				fail(nil, "panic recovered %v: %v", r, string(debug.Stack()))
			}
		}()

		code = 2
	}()

	code = fn(svc, tableNames)

	if cfg.KeepTable {
		return
	}

	for i, tableName := range tableNames {
		if err := deleteTable(ctx, svc, tableName); err != nil {
			fail(nil, "%d: failed to remove test table '%s': %v", i+1, tableName, err)
			continue
		}
		warn(nil, "%d: dynamodb test table deleted: %s", i+1, tableName)
	}

	return
}

func createTable(ctx context.Context, svc *dynamodb.Client, params *dynamodb.CreateTableInput) error {
	_, err := svc.CreateTable(ctx, params)
	if err != nil {
		var (
			er1 *types.TableAlreadyExistsException
			er2 *types.ResourceInUseException
		)
		if errors.As(err, &er1) || errors.As(err, &er2) {
			return nil
		}

		return err
	}
	return nil
}

func deleteTable(ctx context.Context, svc *dynamodb.Client, table string) error {
	if _, err := svc.DeleteTable(ctx, &dynamodb.DeleteTableInput{
		TableName: aws.String(table),
	}); err != nil {
		return err
	}
	return nil
}

func waitForTable(ctx context.Context, svc *dynamodb.Client, table string, maxWait time.Duration) error {
	w := dynamodb.NewTableExistsWaiter(svc)
	if err := w.Wait(ctx,
		&dynamodb.DescribeTableInput{
			TableName: aws.String(table),
		},
		maxWait,
		func(o *dynamodb.TableExistsWaiterOptions) {
			o.MaxDelay = 5 * time.Second
			o.MinDelay = 1 * time.Second
		}); err != nil {
		return fmt.Errorf("timed out while waiting for table to become active: %w", err)
	}
	return nil
}

var (
	rdm = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func addSuffix(base string) string {
	now := strconv.FormatInt(time.Now().UnixNano(), 36)
	random := strconv.FormatInt(int64(rdm.Int31()), 36)
	return base + "-" + now + "-" + random
}
