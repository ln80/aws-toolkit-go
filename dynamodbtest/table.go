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
	"github.com/ln80/aws-toolkit-go/internal/testlog"
)

func TableList(inputs ...*dynamodb.CreateTableInput) []*dynamodb.CreateTableInput {
	return inputs
}

type TableConfig struct {
	TableList  []*dynamodb.CreateTableInput
	OmitSuffix bool
	KeepTable  bool
	MaxWait    time.Duration
}

func WithTables(t *testing.T, svc *dynamodb.Client, cfg TableConfig, fn func(dbsvc *dynamodb.Client, tableNames []string)) {
	t.Helper()

	ctx := context.Background()

	if len(cfg.TableList) == 0 {
		testlog.Fatal(nil, "create table params list is empty")
	}
	if cfg.MaxWait == 0 {
		cfg.MaxWait = time.Minute
	}

	tableNames := make([]string, len(cfg.TableList))
	for i, params := range cfg.TableList {
		tableName := aws.ToString(params.TableName)
		if !cfg.OmitSuffix || tableName == "" {
			tableName = addSuffix(tableName)
		}
		params.TableName = aws.String(tableName)

		tableNames[i] = tableName

		if err := createTable(ctx, svc, params); err != nil {
			testlog.Fatal(t, "%d: failed to create test table '%s': %v", i+1, tableName, err)
		}
		if err := waitForTable(ctx, svc, aws.ToString(params.TableName), cfg.MaxWait); err != nil {
			testlog.Fatal(t, "%d: failed to create test table '%s': %v", i+1, tableName, err)
		}

		testlog.Info(t, "%d: dynamodb test table created: %s", i+1, tableName)
	}

	func() {
		t.Helper()
		defer func() {
			t.Helper()
			if r := recover(); r != nil {
				testlog.Fatal(t, "%d: test panic: %v", r, string(debug.Stack()))
			}
		}()
		fn(svc, tableNames)
	}()

	if cfg.KeepTable {
		return
	}

	for i, tableName := range tableNames {
		if err := deleteTable(ctx, svc, tableName); err != nil {
			testlog.Fail(t, "%d: failed to remove test table '%s': %v", i+1, tableName, err)
			continue
		}
		testlog.Warn(t, "%d: dynamodb test table deleted: %s", i+1, tableName)
	}
}

func WithSharedTables(svc *dynamodb.Client, cfg TableConfig, fn func(dbsvc *dynamodb.Client, tables []string) int) (code int) {
	ctx := context.Background()

	if len(cfg.TableList) == 0 {
		testlog.Fatal(nil, "create table params list is empty")
	}
	if cfg.MaxWait == 0 {
		cfg.MaxWait = time.Minute
	}

	tableNames := make([]string, len(cfg.TableList))
	for i, params := range cfg.TableList {

		tableName := aws.ToString(params.TableName)
		if !cfg.OmitSuffix || tableName == "" {
			tableName = addSuffix(tableName)
		}
		params.TableName = aws.String(tableName)

		tableNames[i] = tableName

		if err := createTable(ctx, svc, params); err != nil {
			testlog.Fatal(nil, "%d: failed to create test table '%s': %v", i+1, tableName, err)
		}
		if err := waitForTable(ctx, svc, aws.ToString(params.TableName), cfg.MaxWait); err != nil {
			testlog.Fatal(nil, "%d: failed to create test table '%s': %v", i+1, tableName, err)
		}

		testlog.Info(nil, "%d: dynamodb test table created: %s", i+1, tableName)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				testlog.Fatal(nil, "%d: test panic: %v: %v", r, string(debug.Stack()))
			}
			code = 2
		}()
		code = fn(svc, tableNames)
	}()

	if cfg.KeepTable {
		return
	}

	for i, tableName := range tableNames {
		if err := deleteTable(ctx, svc, tableName); err != nil {
			testlog.Fail(nil, "%d: failed to remove test table '%s': %v", i+1, tableName, err)
			continue
		}
		testlog.Warn(nil, "%d: dynamodb test table deleted: %s", i+1, tableName)
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
