.PHONY: build test lint integration

build:
	go build -ldflags="-X main.version=$(shell git describe --tags --always --dirty)" -o bin/phasedb ./cmd/phasedb

test:
	go test ./internal/...

integration:
	go test ./tests/integration/... -tags integration -v

lint:
	go vet ./...
