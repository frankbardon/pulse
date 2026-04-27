.PHONY: build test lint cover clean

build:
	go build -o bin/pulse ./cmd/pulse/

test:
	go test ./...

lint:
	golangci-lint run ./...

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

clean:
	rm -rf bin/ coverage.out
