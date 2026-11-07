GO ?= go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

.PHONY: build test lint snap gif

build:
	$(GO) build -ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o mkto ./cmd/mkto

test:
	$(GO) test -race ./...

lint:
	$(GO) vet ./...
	test -z "$$(gofmt -l .)"

snap:
	snapcraft pack

# Re-render docs/assets/mkto_demo.gif from testdata/golden (needs uv; Pillow is pinned in tools/demo_gif.py.lock).
gif:
	uv run tools/demo_gif.py
