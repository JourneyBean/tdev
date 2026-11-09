GO ?= go
VERSION ?= 0.5.0
LDFLAGS = -X tdev/internal/cli.Version=$(VERSION)

.PHONY: build test check cross release
build:
	CGO_ENABLED=0 $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o bin/tdev ./cmd/tdev

test:
	$(GO) test -race ./...

check:
	$(GO) vet ./...
	test -z "$$(gofmt -l cmd internal)"
	git diff --check
	sh -n scripts/install.sh
	sh -n scripts/gitlab-build.sh

cross:
	@mkdir -p dist
	@set -eu; for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		candidate=$$(mktemp dist/.tdev-cross.XXXXXX); trap 'rm -f "$$candidate"' EXIT; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -buildvcs=false -ldflags '$(LDFLAGS)' -o "$$candidate" ./cmd/tdev; \
		chmod 755 "$$candidate"; mv -f "$$candidate" dist/tdev-$$os-$$arch; \
	done

release: cross
	$(GO) run ./cmd/tdev-release --dir dist --version '$(VERSION)' --private-key "$${TDEV_SIGNING_KEY:?set TDEV_SIGNING_KEY to an external PKCS8 Ed25519 key}"
