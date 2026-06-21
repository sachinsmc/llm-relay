.PHONY: run build test cover vet fmt fmt-check tidy lint docker docker-run clean

BINARY := llm-relay
PKG := ./...

run: ## Run the standalone server (reads .env via your shell)
	go run ./cmd/llm-relay

build: ## Build the server binary
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) ./cmd/llm-relay

test: ## Run tests with the race detector
	go test -race $(PKG)

cover: ## Run tests and open a coverage report
	go test -race -coverprofile=coverage.txt $(PKG)
	go tool cover -func=coverage.txt | tail -1

vet: ## go vet
	go vet $(PKG)

fmt: ## Format the code
	gofmt -w .

fmt-check: ## Fail if any file is not gofmt-clean
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

tidy: ## Tidy the module graph
	go mod tidy

lint: vet fmt-check ## Lightweight lint: vet + format check

docker: ## Build the Docker image
	docker build -t $(BINARY):latest .

docker-run: ## Run the Docker image (pass env via -e or --env-file .env)
	docker run --rm -p 8080:8080 --env-file .env $(BINARY):latest

clean: ## Remove build artifacts
	rm -f $(BINARY) coverage.txt
