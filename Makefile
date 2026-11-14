VERSION ?= $(shell date +%Y.%m.%d)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: run build linux test clean

# Run locally on this Mac (http://localhost:8080)
run:
	go run ./cmd/rcm -data ./rcm-data

# Build for this Mac
build:
	go build -ldflags "$(LDFLAGS)" -o dist/rcm ./cmd/rcm

# Build for the 2012 Mac Mini running Linux (Intel x86-64)
linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/rcm-linux-amd64 ./cmd/rcm

test:
	go vet ./...
	go test ./...

clean:
	rm -rf dist
