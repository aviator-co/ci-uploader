default:
    @just --list

build:
    go build -o aviator-ci ./cmd/aviator-ci

test:
    go test --vet=all ./...

# golangci-lint must be installed
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    unformatted="$(gofmt -l .)"
    if [ -n "$unformatted" ]; then
        echo "not formatted, run 'just fmt':"
        echo "$unformatted"
        exit 1
    fi
    golangci-lint run

fmt:
    gofmt -w .

# just run -- run --check tests -- make test
run *ARGS:
    go run ./cmd/aviator-ci {{ARGS}}
