.PHONY: build fmt fmt-check vet test lint vuln check run clean

BIN := wheelhouse

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

# Everything CI runs.
check: fmt-check vet test lint vuln build

run:
	go run ./cmd/wheelhouse

clean:
	rm -f $(BIN)
