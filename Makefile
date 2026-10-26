GO      ?= go
BIN     := bin
COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: build test test-e2e vet gen gen-check lint-spec up down vcsim

build: ## Build rvav and rvav-vcsim into ./bin
	$(GO) build -o $(BIN)/rvav ./cmd/rvav
	$(GO) build -o $(BIN)/rvav-vcsim ./cmd/rvav-vcsim

test: ## Unit tests, plus vcsim and workflow tests (no database needed)
	$(GO) test -short ./...

test-e2e: ## End-to-end tests on their own PostgreSQL, or on RVAV_TEST_DATABASE_URL (wiped)
	$(GO) test ./internal/e2e/ -count=1 -v

vet:
	$(GO) vet ./...

gen: ## Regenerate internal/api from api/openapi.yaml
	$(GO) generate ./internal/api/

gen-check: gen ## Fail if the generated code is out of date
	git diff --exit-code -- internal/api/api.gen.go

lint-spec:
	npx @redocly/cli@1 lint --config api/redocly.yaml api/openapi.yaml

up: ## Start the local stack (PostgreSQL, Temporal, Keycloak, vcsim, rvav)
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

vcsim: ## Run the vCenter simulator with the GVA layout on 127.0.0.1:8989
	$(GO) run ./cmd/rvav-vcsim
