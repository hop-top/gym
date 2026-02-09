BIN ?= gym

.PHONY: build test fmt vet tidy

build:
\tgo build -o $(BIN) ./cmd/gym

test:
\tgo test ./...

fmt:
\tgofmt -w .

vet:
\tgo vet ./...

tidy:
\tgo mod tidy

