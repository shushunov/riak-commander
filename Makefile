BINARY  := riak-commander
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build test race vet lint check run install snapshot clean

build: ## build ./riak-commander
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) .

test: ## unit tests + TUI tests on a simulated screen
	go test ./...

race: ## tests with the race detector
	go test -race ./...

vet:
	go vet ./...

lint: vet ## vet, gofmt check and staticcheck (if installed)
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	@if command -v staticcheck >/dev/null; then staticcheck ./...; else echo "staticcheck not installed, skipped"; fi

check: lint race ## everything CI runs

run: build
	./$(BINARY)

install: ## install into $GOBIN / $GOPATH/bin
	go install -trimpath -ldflags "$(LDFLAGS)" .

snapshot: ## local release build of all platforms into dist/ (needs goreleaser)
	goreleaser release --snapshot --clean

clean:
	rm -rf $(BINARY) dist/
