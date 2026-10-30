VERSION ?= $(shell node -p "require('./npm/cli/package.json').version" 2>/dev/null || echo 0.1.0)
LDFLAGS := -X github.com/windviki/agentbus/internal/server.Version=$(VERSION)

.PHONY: all build install fmt vet test test-race setup-test e2e npm-test release clean

all: build

build:
	go build -trimpath -ldflags "$(LDFLAGS)" -o agentbus .

install: build
	./scripts/install.sh

fmt:
	gofmt -w .

vet:
	go vet ./...

test:
	go test ./...

test-race:
	go test -race ./...

setup-test:
	./test/setup.sh

e2e:
	./test/e2e.sh

npm-test:
	./scripts/test-npm.sh

release:
	./scripts/build-release.sh

clean:
	rm -f agentbus
	rm -rf dist npm/platform/*/bin
