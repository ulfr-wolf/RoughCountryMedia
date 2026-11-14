VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || date +%Y.%m.%d)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: run build linux linux-arm64 deploy test clean

# Run locally on this Mac (http://localhost:8080)
run:
	go run ./cmd/rcm -data ./rcm-data

# Build for this Mac
build:
	go build -ldflags "$(LDFLAGS)" -o dist/rcm ./cmd/rcm

# Build for the 2012 Mac Mini running Linux (Intel x86-64)
linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/rcm-linux-amd64 ./cmd/rcm

# Build for ARM Linux boards (Raspberry Pi 4/5 with a 64-bit OS)
linux-arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o dist/rcm-linux-arm64 ./cmd/rcm

# Install or upgrade on the Mini over SSH:  make deploy HOST=you@mini.local
deploy:
	@test -n "$(HOST)" || (echo "usage: make deploy HOST=you@mini.local" && exit 2)
	./deploy.sh $(HOST)

test:
	go vet ./...
	go test ./...

clean:
	rm -rf dist
