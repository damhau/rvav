GO      ?= go
PNPM    ?= pnpm
BIN     := bin
COMPOSE := docker compose -f deploy/docker-compose.yml
# Scanners, pinned (the image by digest).
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
IMAGES      ?= rvav:scan rvav-web:scan
# Files Trivy writes are the caller's, not root's.
AS_ME        = -u $$(id -u):$$(id -g) --group-add $$(stat -c %g /var/run/docker.sock) -e TRIVY_CACHE_DIR=/tmp/trivy
TRIVY       := aquasec/trivy:0.69.3@sha256:bcc376de8d77cfe086a917230e818dc9f8528e3c852f7b1aff648949b6258d1c

.PHONY: check check-quick web-quick web-e2e build test test-e2e test-ansible vet gen gen-check lint-spec lint-chart web-check api-docs images vuln vuln-go vuln-web vuln-images sbom up down vcsim

check: gen-check lint-spec lint-chart vet test test-e2e test-ansible test-python web-check ## Everything CI checks on every push (the browser tests run daily: web-e2e)
check-quick: gen-check lint-spec vet test web-quick ## The fast part, before a push: no end-to-end, browser, chart, Ansible or Python tests (CI runs them)

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

api-docs: ## The API reference, one HTML file built from the spec, into dist/api-reference.html
	mkdir -p dist
	npx --yes @redocly/cli@1 build-docs --config api/redocly.yaml --disableGoogleFont \
		--title "rvav API $$(sed -n 's/^  version: //p' api/openapi.yaml | head -1)" -o dist/api-reference.html api/openapi.yaml

lint-chart: ## The Helm chart lints, renders, and refuses dev sign-in (from its values and its Secret)
	@if ! command -v helm >/dev/null; then echo "helm not installed: the chart isn't checked here (CI does)"; exit 0; fi; \
	helm lint --strict deploy/helm/rvav >/dev/null && \
	helm template rvav deploy/helm/rvav -f deploy/helm/kind/values.yaml >/dev/null && \
	if helm template rvav deploy/helm/rvav --set config.RVAV_AUTH_MODE=dev >/dev/null 2>&1; then \
		echo "the chart let RVAV_AUTH_MODE=dev through" >&2; exit 1; fi && \
	if [ "$$(helm template rvav deploy/helm/rvav --set secret.env.RVAV_AUTH_MODE=dev | grep -c '{ name: RVAV_AUTH_MODE, value: oidc }')" -lt 6 ]; then \
		echo "an rvav container lacks the SSO-only override (the Secret could set dev sign-in)" >&2; exit 1; fi && echo "chart ok"

web-quick: ## Web app without its build and Playwright: generated types current, typecheck, lint, unit tests
	cd web && $(PNPM) gen:api
	git diff --exit-code -- web/src/lib/api/schema.d.ts
	cd web && $(PNPM) typecheck
	cd web && $(PNPM) lint
	cd web && $(PNPM) test

web-check: ## Web app: generated types current, typecheck, lint, unit tests, build
	cd web && $(PNPM) gen:api
	git diff --exit-code -- web/src/lib/api/schema.d.ts
	cd web && $(PNPM) typecheck
	cd web && $(PNPM) lint
	cd web && $(PNPM) test
	cd web && $(PNPM) build

web-e2e: ## The browser tests (Playwright on the mock API, accessibility included); CI runs them daily
	cd web && $(PNPM) e2e

vuln: vuln-go vuln-web vuln-images ## Known vulnerabilities in the Go code, the web app's dependencies and the images (CI, daily)

vuln-go: ## Vulnerabilities the Go code reaches (govulncheck, built with the module's toolchain)
	GOTOOLCHAIN=$$($(GO) env GOVERSION) $(GO) run $(GOVULNCHECK) ./...

vuln-web: ## The web app's dependencies, with its build and test tools (pnpm audit)
	cd web && $(PNPM) audit

images: ## Build the rvav and rvav-web images as $(IMAGES)
	docker build -q -t $(word 1,$(IMAGES)) . >/dev/null
	docker build -q -t $(word 2,$(IMAGES)) web >/dev/null

vuln-images: images ## Scan both images: no HIGH or CRITICAL vulnerability that has a fix (Trivy)
	for i in $(IMAGES); do \
		docker run --rm -v /var/run/docker.sock:/var/run/docker.sock $(TRIVY) image --quiet \
			--severity HIGH,CRITICAL --ignore-unfixed --exit-code 1 $$i || exit 1; done

sbom: images ## CycloneDX SBOMs into ./sbom: each image, and the web app's npm dependencies (not in its image)
	mkdir -p sbom
	for i in $(IMAGES); do \
		docker run --rm $(AS_ME) -v /var/run/docker.sock:/var/run/docker.sock -v $$PWD/sbom:/out $(TRIVY) image --quiet \
			--format cyclonedx -o /out/$$(echo $$i | sed 's|.*/||; s|:.*||').cdx.json $$i || exit 1; done
	docker run --rm $(AS_ME) -v $$PWD/web:/src:ro -v $$PWD/sbom:/out $(TRIVY) fs --quiet --format cyclonedx \
		-o /out/rvav-web-npm.cdx.json /src

up: ## Start the local stack (PostgreSQL, Temporal, Keycloak, vcsim, rvav)
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

vcsim: ## Run the vCenter simulator with the GVA layout on 127.0.0.1:8989
	$(GO) run ./cmd/rvav-vcsim
