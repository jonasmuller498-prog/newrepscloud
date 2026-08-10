.PHONY: build test test-integration vet fmt-check

build:
	go build ./...

test:
	go test ./...

test-integration:
	go test -tags=integration ./...

vet:
	go vet ./...

fmt-check:
	test -z "$$(gofmt -l .)"
