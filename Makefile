.PHONY: test lint fmt

test:
	go test -race ./...

lint:
	golangci-lint run

fmt:
	golangci-lint fmt
