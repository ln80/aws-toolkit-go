

DYNAMODB_PORT  = 8070

export DYNAMODB_ENDPOINT = http://localhost:$(DYNAMODB_PORT)

start-dynamodb:
	docker run -p $(DYNAMODB_PORT):8000 amazon/dynamodb-local -jar DynamoDBLocal.jar -inMemory

test:
	go test -race -v -cover ./...
