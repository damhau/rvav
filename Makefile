GO      ?= go
PNPM    ?= pnpm
BIN     := bin
COMPOSE := docker compose -f deploy/docker-compose.yml

.PHONY: check build test test-e2e test-ansible vet gen gen-check lint-spec lint-chart web-check up down vcsim

check: gen-check lint-spec lint-chart vet test test-e2e test-ansible test-python web-check ## Everything CI checks; must pass before every push

build: ## Build rvav and rvav-vcsim into ./bin
	$(GO) build -o $(BIN)/rvav ./cmd/rvav
	$(GO) build -o $(BIN)/rvav-vcsim ./cmd/rvav-vcsim

test: ## Unit tests, plus vcsim and workflow tests (no database needed)
	$(GO) test -short ./...

test-e2e: ## End-to-end tests on their own PostgreSQL, or on RVAV_TEST_DATABASE_URL (wiped)
	$(GO) test ./internal/e2e/ -count=1 -v

test-ansible: ## The rvav.platform collection: it builds, and its inventory plugin works through ansible-inventory (uv)
	export ANSIBLE_HOME=$$(mktemp -d) && cd ansible && uv run --frozen -- ansible-galaxy collection build --force \
		--output-path "$$ANSIBLE_HOME" ansible_collections/rvav/platform
	uv run --frozen --project ansible -- pytest -q -p no:cacheprovider ansible/ansible_collections/rvav/platform/tests

test-python: ## Custom workflow helpers and the decommission example, on Temporal's time-skipping test server (uv)
	uv run --frozen --project sdk/python -- pytest -q -p no:cacheprovider sdk/python/tests
	cd examples/decommission && uv run --frozen -- pytest -q -p no:cacheprovider tests

vet:
	@out=$$(gofmt -l cmd internal pkg); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	$(GO) vet ./...

gen: ## Regenerate internal/api from api/openapi.yaml
	$(GO) generate ./internal/api/

gen-check: gen ## Fail if the generated code is out of date
	git diff --exit-code -- internal/api/api.gen.go

lint-spec:
	npx --yes @redocly/cli@1 lint --config api/redocly.yaml api/openapi.yaml

lint-chart: ## The Helm chart lints, renders, and refuses dev sign-in
	@if ! command -v helm >/dev/null; then echo "helm not installed: the chart isn't checked here (CI does)"; exit 0; fi; \
	helm lint --strict deploy/helm/rvav >/dev/null && \
	helm template rvav deploy/helm/rvav -f deploy/helm/kind/values.yaml >/dev/null && \
	if helm template rvav deploy/helm/rvav --set config.RVAV_AUTH_MODE=dev >/dev/null 2>&1; then \
		echo "the chart let RVAV_AUTH_MODE=dev through" >&2; exit 1; fi && echo "chart ok"

web-check: ## Web app: generated types current, typecheck, lint, unit tests, build, Playwright
	cd web && $(PNPM) gen:api
	git diff --exit-code -- web/src/lib/api/schema.d.ts
	cd web && $(PNPM) typecheck
	cd web && $(PNPM) lint
	cd web && $(PNPM) test
	cd web && $(PNPM) build
	cd web && $(PNPM) e2e

up: ## Start the local stack (PostgreSQL, Temporal, Keycloak, vcsim, rvav)
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

vcsim: ## Run the vCenter simulator with the GVA layout on 127.0.0.1:8989
	$(GO) run ./cmd/rvav-vcsim
