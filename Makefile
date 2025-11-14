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
	$(GO) test -shuffle=on -count=1 ./...

