## aws-toolkit-go

An opinionated collection of Golang helper functions to simplify testing and AWS SDK usage.

(WIP)

### DynamoDB consumed capacity tracing

Track DynamoDB read/write capacity units across all tables during a request-scoped context:

```go
ctx, cc := dynamodbhelper.CapacityContext(r.Context())
r = r.WithContext(ctx)

// Build the client with capacity middleware (or call AppendCapacityMiddleware on cfg.APIOptions).
dbsvc := dynamodbhelper.NewClient(cfg)

// Session writes and direct client calls (Query, GetItem, etc.) aggregate into cc.

// After the handler:
if !cc.IsZero() {
    summary := cc.Summary()
    // log summary.Total, summary.Read, summary.Write, summary.Tables
}
```