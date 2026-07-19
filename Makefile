.PHONY: build test lint fmt install-tools clean

GOCACHE ?= /tmp/gws-go-cache
GOMODCACHE ?= $(CURDIR)/.gomodcache
XDG_CACHE_HOME ?= /tmp/gws-go-xdg-cache
export GOCACHE GOMODCACHE XDG_CACHE_HOME

STATICCHECK_VERSION := v0.7.0
GOLANGCI_LINT_VERSION := v2.12.2

build:
	go build -o bin/gws-go .

test:
	go test -race -cover ./...

lint:
	go vet ./...
	bin/staticcheck ./...
	bin/golangci-lint run

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './.gomodcache/*')

install-tools:
	mkdir -p bin
	GOBIN=$(CURDIR)/bin go install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	GOBIN=$(CURDIR)/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

clean:
	rm -rf bin coverage.out
