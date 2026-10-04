.PHONY: test lint build golden snapshot
test:
	go test -race ./...
lint:
	golangci-lint run
build:
	go build -trimpath -ldflags "-s -w -X main.version=$$(git describe --tags --always)" -o env4ci ./cmd/env4ci
golden:
	go test ./internal/infrastructure/ciscan ./cmd/env4ci -update
snapshot:
	goreleaser release --snapshot --clean
