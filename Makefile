BINARY=zedstream
CMD=./cmd/api
MIGRATE=migrate
DB_URL=$(shell grep DATABASE_URL .env | cut -d '=' -f2-)
MIGRATIONS_DIR=db/migrations

.PHONY: all build run test clean migrate migrate-down migrate-create swag lint deps

## Build the binary
build:
	go build -o bin/$(BINARY) $(CMD)

## Run in development (hot config from .env)
run:
	go run $(CMD)

## Run all tests
test:
	go test ./... -v -race -cover

## Run tests with coverage report
test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html

## Tidy and download dependencies
deps:
	go mod tidy
	go mod download

## Apply all pending migrations
migrate:
	$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" up

## Rollback the last migration
migrate-down:
	$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down 1

## Roll back all migrations
migrate-reset:
	$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DB_URL)" down

## Create a new migration (usage: make migrate-create NAME=add_indexes)
migrate-create:
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq $(NAME)

## Generate Swagger docs (requires swag CLI: go install github.com/swaggo/swag/cmd/swag@latest)
swag:
	swag init -g cmd/api/main.go -o docs

## Lint the project (requires golangci-lint)
lint:
	golangci-lint run ./...

## Clean build artifacts
clean:
	rm -rf bin/ coverage.out coverage.html docs/

## Show help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
