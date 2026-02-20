BINARY  := trioclaw
MODULE  := github.com/machinefi/trioclaw
VERSION := 0.1.0
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)"

.PHONY: build run test clean cross doctor

build:
	go build $(LDFLAGS) -o bin/$(BINARY) ./cmd/trioclaw

run: build
	./bin/$(BINARY) run

test:
	go test ./...

clean:
	rm -rf bin/

doctor: build
	./bin/$(BINARY) doctor

# Cross-compile for all targets
cross:
	GOOS=darwin  GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-darwin-arm64  ./cmd/trioclaw
	GOOS=darwin  GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-darwin-amd64  ./cmd/trioclaw
	GOOS=linux   GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-amd64   ./cmd/trioclaw
	GOOS=linux   GOARCH=arm64 go build $(LDFLAGS) -o bin/$(BINARY)-linux-arm64   ./cmd/trioclaw
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o bin/$(BINARY)-windows-amd64.exe ./cmd/trioclaw
