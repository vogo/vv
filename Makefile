.PHONY: build

lint:
	golangci-lint run

format:
		goimports -w -l .
		go fix ./...
		go fmt ./...
		gofumpt -w .

test:
		go test ./... -coverprofile=coverage.out -covermode=atomic -v 

build: format lint test
