.PHONY: build ui fmt fmt-check vet test lint vuln deps check run clean

BIN := wheelhouse

# Builds the Vite UI (web/) and copies it where `build` embeds it. Needs Node and
# `npm ci` in web/ first; without it the binary serves the placeholder page.
ui:
	cd ../web && npm run build && npm run embed

# A static, CGO-free, path-independent binary.
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/wheelhouse

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }

vet:
	go vet ./...

test:
	go test -race ./...

lint:
	golangci-lint run

vuln:
	govulncheck ./...

# Module hygiene: go.mod and go.sum are tidy and verified, and every module in
# the shipped binary has a permissive license that is compatible with GPL-3.0
# (allow-list; a new license type needs a conscious edit here and in ci.yml).
LICENSES := MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC
deps:
	go mod tidy -diff
	go mod verify
	go run github.com/google/go-licenses/v2@v2.0.1 check ./cmd/wheelhouse --ignore github.com/tristanwl/wheelhouse --allowed_licenses=$(LICENSES)

# Everything CI runs.
check: fmt-check vet test lint vuln deps build

# A development config directory (database, master key, setup token), so that
# `make run` works on a machine with no /config. Set WHEELHOUSE_CONFIG_DIR to use
# another one. It is git-ignored and only for development.
DEVCONFIG ?= $(CURDIR)/.devconfig

run:
	@mkdir -p -m 700 $(DEVCONFIG)
	WHEELHOUSE_CONFIG_DIR=$${WHEELHOUSE_CONFIG_DIR:-$(DEVCONFIG)} go run ./cmd/wheelhouse

clean:
	rm -f $(BIN)
