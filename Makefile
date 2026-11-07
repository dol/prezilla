# Makefile - local dev helpers that mirror the CI steps.
# Usage:
#   make            lint + test (default)
#   make test-integration   X11 tests on throwaway displays (Xvfb, xfwm4)
#   make build      build ./prezilla
#   make install    install under $(DESTDIR)$(PREFIX) (no root needed with DESTDIR)
#   make package    build .deb/.rpm/.pkg.tar.zst into dist/ (requires nfpm)

# Package version. `git describe` yields e.g. "v1.2.3" on a tag, or a bare hash
# when there are no tags. A leading "v" is stripped; if the result does not start
# with a digit (which nfpm/dpkg require), it becomes "0.0.0-<describe>". The same
# value feeds -ldflags, so `prezilla version` matches the package version.
# Override with: make VERSION=1.2.3
GIT_DESCRIBE := $(shell git describe --tags --always --dirty 2>/dev/null || echo unknown)
VERSION ?= $(shell echo '$(GIT_DESCRIBE)' | sed -e 's/^v//' -e '/^[0-9]/!s/^/0.0.0-/')
export VERSION

PREFIX ?= /usr/local
DESTDIR ?=
ICONS   := packaging/icons/hicolor
LDFLAGS := -X main.version=$(VERSION)

.PHONY: all build test test-integration lint run icons install uninstall package ci clean

all: lint test

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o prezilla ./cmd/prezilla

test:
	go test -race -count=1 ./...

# X11 integration tests on throwaway displays (needs Xvfb, xfwm4, dbus-run-session).
test-integration:
	bash scripts/integration.sh

lint:
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/prezilla $(ARGS)

icons:
	bash scripts/render-icons.sh

install: build
	install -D -m 0755 prezilla $(DESTDIR)$(PREFIX)/bin/prezilla
	install -D -m 0644 packaging/prezilla.desktop $(DESTDIR)$(PREFIX)/share/applications/prezilla.desktop
	cd $(ICONS) && find . -type f | while read -r f; do \
	  install -D -m 0644 "$$f" "$(DESTDIR)$(PREFIX)/share/icons/hicolor/$$f"; \
	done

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/prezilla
	rm -f $(DESTDIR)$(PREFIX)/share/applications/prezilla.desktop
	cd $(ICONS) && find . -type f | while read -r f; do \
	  rm -f "$(DESTDIR)$(PREFIX)/share/icons/hicolor/$$f"; \
	done

# Static linux/amd64 binary, then one package per format.
package:
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/prezilla-linux-amd64 ./cmd/prezilla
	nfpm package -p deb -f nfpm.yaml -t dist/prezilla_$(VERSION)_amd64.deb
	nfpm package -p rpm -f nfpm.yaml -t dist/prezilla_$(VERSION)_amd64.rpm
	nfpm package -p archlinux -f nfpm.yaml -t dist/prezilla_$(VERSION)_amd64.pkg.tar.zst

ci: lint test
	@echo "CI simulation passed"

clean:
	rm -rf prezilla dist/
