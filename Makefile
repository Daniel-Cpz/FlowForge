.PHONY: build test test-unit run-api run-worker docker-up docker-down migrate-up migrate-down fmt vet

build:
	go build -o bin/api ./cmd/api
	go build -o bin/worker ./cmd/worker
	go build -o bin/migrate ./cmd/migrate

test:
	go test ./...

test-unit:
	go test ./internal/... ./migrations/...

run-api:
	go run ./cmd/api

run-worker:
	go run ./cmd/worker

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

migrate-up:
	go run ./cmd/migrate up

migrate-down:
	go run ./cmd/migrate down

fmt:
	gofmt -w cmd internal migrations tests

vet:
	go vet ./...
