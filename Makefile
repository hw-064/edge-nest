.PHONY: ci fmt vet build test
GO ?= go

ci: fmt vet build test

fmt:
	gofmt -s -w .

vet:
	$(GO) vet ./...

build:
	$(GO) build ./...

test:
	$(GO) test -race -shuffle=on -count=1 ./...

