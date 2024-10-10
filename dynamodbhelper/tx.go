package dynamodbhelper

import (
	"context"
	"fmt"

	"errors"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
	ErrTxAlreadyStarted  = errors.New("transaction already started")
	ErrTxInvalidItemType = errors.New("invalid transaction item type")
)

type ContextKey string

var sessionContextKey ContextKey = "SessionContextKey"

func NewSession(api *dynamodb.Client) *Session {
	return &Session{
		api: api,
		cc:  &ConsumedCapacity{},
	}
}

func SessionFrom(ctx context.Context) (*Session, bool) {
	ses, ok := ctx.Value(sessionContextKey).(*Session)
	if ok {
		return ses, true
	}
	return nil, false
}

func ContextWithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionContextKey, s)
}

type Session struct {
	api *dynamodb.Client
	ops []txOp
	cc  *ConsumedCapacity
}

// ConsumedCapacity implements Session.
func (s *Session) ConsumedCapacity() *ConsumedCapacity {
	return s.cc
}

func (s *Session) StartTx() error {
	if s.ops == nil {
		s.ops = []txOp{}
		return nil
	}
	return ErrTxAlreadyStarted
}

func (s *Session) HasTx() bool {
	return s.ops != nil
}

func (s *Session) CloseTx() error {
	s.ops = nil
	return nil
}

func (s *Session) addConsumedCapacity(out any) {
	if s.cc == nil {
		return
	}
	if o, ok := out.(*dynamodb.TransactWriteItemsOutput); ok && o != nil {
		for _, cc := range o.ConsumedCapacity {
			cc := cc
			AddConsumedCapacity(s.cc, &cc)
		}
		return
	}
	var occ *types.ConsumedCapacity
	switch o := out.(type) {
	case *dynamodb.PutItemOutput:
		if o == nil {
			return
		}
		occ = o.ConsumedCapacity
	case *dynamodb.UpdateItemOutput:
		if o == nil {
			return
		}
		occ = o.ConsumedCapacity
	case *dynamodb.DeleteItemOutput:
		if o == nil {
			return
		}
		occ = o.ConsumedCapacity
	default:
		return
	}
	AddConsumedCapacity(s.cc, occ)
}

func (s *Session) CommitTx(ctx context.Context) (err error) {
	defer func() {
		if err == nil {
			err = s.CloseTx()
		}
	}()

	count := len(s.ops)

	if count == 0 {
		return
	}

	if count == 1 {
		op := s.ops[0]
		flush := func(op txOp) (err error) {
			var out any
			defer s.addConsumedCapacity(out)
			if op.put != nil {
				_, err = s.api.PutItem(ctx, op.put)
				return
			}
			if op.update != nil {
				_, err = s.api.UpdateItem(ctx, op.update)
				return
			}
			if op.delete != nil {
				return
			}
			return
		}
		err = flush(op)
		return
	}

	txItems := make([]types.TransactWriteItem, count)
	for i, op := range s.ops {
		txItems[i] = types.TransactWriteItem{
			Put:            op.putTx(),
			Update:         op.updateTx(),
			Delete:         op.deleteTx(),
			ConditionCheck: op.checkTx(),
		}
	}

	var out any
	out, err = s.api.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems:          txItems,
		ReturnConsumedCapacity: types.ReturnConsumedCapacityIndexes,
	})

	s.addConsumedCapacity(out)

	return
}

func (s *Session) Put(ctx context.Context, p *dynamodb.PutItemInput) error {
	if s.HasTx() {
		s.ops = append(s.ops, txOp{
			put: p,
		})
		return nil
	}
	out, err := s.api.PutItem(ctx, p)
	s.addConsumedCapacity(out)
	if err != nil {
		return err
	}

	return nil
}

func (s *Session) Update(ctx context.Context, u *dynamodb.UpdateItemInput) error {
	if s.HasTx() {
		s.ops = append(s.ops, txOp{
			update: u,
		})
		return nil
	}
	out, err := s.api.UpdateItem(ctx, u)
	s.addConsumedCapacity(out)
	if err != nil {
		return err
	}
	return nil
}

func (s *Session) Delete(ctx context.Context, d *dynamodb.DeleteItemInput) error {
	if s.HasTx() {
		s.ops = append(s.ops, txOp{
			delete: d,
		})
		return nil
	}
	out, err := s.api.DeleteItem(ctx, d)
	s.addConsumedCapacity(out)
	if err != nil {
		return err
	}
	return nil
}

func (s *Session) Check(ctx context.Context, c *types.ConditionCheck) error {
	if s.HasTx() {
		s.ops = append(s.ops, txOp{
			check: c,
		})
		return nil
	}
	return nil
}

func (s *Session) AddToTx(ctx context.Context, ops []any) error {
	for _, item := range ops {
		var err error
		switch txi := item.(type) {
		case *dynamodb.PutItemInput:
			err = s.Put(ctx, txi)
		case *dynamodb.DeleteItemInput:
			err = s.Delete(ctx, txi)
		case *dynamodb.UpdateItemInput:
			err = s.Update(ctx, txi)
		case *types.ConditionCheck:
			err = s.Check(ctx, txi)
		default:
			panic(fmt.Errorf("%w: %T", ErrTxInvalidItemType, txi))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type txOp struct {
	put    *dynamodb.PutItemInput
	update *dynamodb.UpdateItemInput
	delete *dynamodb.DeleteItemInput
	check  *types.ConditionCheck
}

func (op *txOp) putTx() *types.Put {
	if op.put == nil {
		return nil
	}
	return &types.Put{
		Item:                                op.put.Item,
		TableName:                           op.put.TableName,
		ConditionExpression:                 op.put.ConditionExpression,
		ExpressionAttributeNames:            op.put.ExpressionAttributeNames,
		ExpressionAttributeValues:           op.put.ExpressionAttributeValues,
		ReturnValuesOnConditionCheckFailure: op.put.ReturnValuesOnConditionCheckFailure,
	}
}

func (op *txOp) updateTx() *types.Update {
	if op.update == nil {
		return nil
	}
	return &types.Update{
		Key:                                 op.update.Key,
		UpdateExpression:                    op.update.UpdateExpression,
		TableName:                           op.update.TableName,
		ConditionExpression:                 op.update.ConditionExpression,
		ExpressionAttributeNames:            op.update.ExpressionAttributeNames,
		ExpressionAttributeValues:           op.update.ExpressionAttributeValues,
		ReturnValuesOnConditionCheckFailure: op.update.ReturnValuesOnConditionCheckFailure,
	}
}

func (op *txOp) deleteTx() *types.Delete {
	if op.delete == nil {
		return nil
	}
	return &types.Delete{
		Key:                                 op.delete.Key,
		TableName:                           op.delete.TableName,
		ConditionExpression:                 op.delete.ConditionExpression,
		ExpressionAttributeNames:            op.delete.ExpressionAttributeNames,
		ExpressionAttributeValues:           op.delete.ExpressionAttributeValues,
		ReturnValuesOnConditionCheckFailure: op.delete.ReturnValuesOnConditionCheckFailure,
	}
}

func (op *txOp) checkTx() *types.ConditionCheck {
	return op.check
}
