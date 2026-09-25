BINARY=zedstream
CMD=./cmd/api
MIGRATE=migrate
DB_URL=$(shell grep '^DATABASE_URL=' .env | cut -d '=' -f2-)
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

## Pull production DB data and refresh the local database (schema must already exist)
db-pull:
	ssh vps2 'PGPASSFILE=~/.pgpass pg_dump -h 127.0.0.1 -U zedstream --data-only zedstream' > /tmp/zedstream_data.sql
	psql "postgres://jaeycop@/zedstream?host=/var/run/postgresql&sslmode=disable" -c "TRUNCATE tracks, artists, albums, playlists, users, genres, likes, follows RESTART IDENTITY CASCADE;"
	psql "postgres://jaeycop@/zedstream?host=/var/run/postgresql&sslmode=disable" -f /tmp/zedstream_data.sql
	@echo "Local DB data refreshed from production"

## Deploy: rsync code to vps2, rebuild, restart systemd service
deploy:
	rsync -avz --exclude='.git/' --exclude='web/' --exclude='node_modules/' --exclude='bin/' --exclude='.env' ./ vps2:/tmp/zedstream-deploy/
	ssh vps2 "sudo cp -r /tmp/zedstream-deploy/. /opt/zedstream/ && sudo chown -R ubuntu:ubuntu /opt/zedstream && cd /opt/zedstream && go build -o /opt/zedstream/zedstream ./cmd/api && sudo systemctl restart zedstream-api.service"
	@echo "Deployed to vps2"

## Open SSH tunnel to production Postgres on vps2 (local:15432 → vps2:5432)
tunnel:
	fuser -k 15432/tcp 2>/dev/null; ssh -f -N -L 15432:127.0.0.1:5432 vps2 -o ServerAliveInterval=30 -o ExitOnForwardFailure=yes && echo "Tunnel open on localhost:15432"

## Kill the SSH tunnel
tunnel-kill:
	fuser -k 15432/tcp 2>/dev/null; echo "freed"

## Show help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
