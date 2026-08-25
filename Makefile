.PHONY: all build test coverage coverage-check race vet lint vuln fmt-check module-verify module-tidy-check license-check licenses shellcheck-require shell-lint workflow-lint release-doc-check release-guard-test package package-check clean

GO ?= go
GOFLAGS ?=
GOFMT ?= $(shell $(GO) env GOROOT)/bin/gofmt
SHELLCHECK ?= shellcheck
COVERAGE_MIN ?= 95.0

all: fmt-check module-verify module-tidy-check license-check vuln shell-lint workflow-lint release-doc-check release-guard-test vet test coverage-check race lint build

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -trimpath -buildvcs=false -o bin/timer-cli ./cmd/timer-cli

test:
	$(GO) test ./...

coverage:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

coverage-check: coverage
	@total="$$( $(GO) tool cover -func=coverage.out | awk '/^total:/ { sub(/%$$/, "", $$3); print $$3 }' )"; \
		test -n "$$total" || { echo "Could not determine total coverage"; exit 1; }; \
		awk -v total="$$total" -v minimum="$(COVERAGE_MIN)" 'BEGIN { \
			if (total + 0 < minimum + 0) { \
				printf "Coverage %.1f%% is below required %.1f%%\n", total, minimum; \
				exit 1; \
			} \
			printf "Coverage %.1f%% meets required %.1f%%\n", total, minimum; \
		}'

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GO) tool staticcheck ./...

vuln:
	$(GO) tool govulncheck ./...

fmt-check:
	@fmt_output="$$(git ls-files -z --cached --others --exclude-standard -- '*.go' | xargs -0 "$(GOFMT)" -l)" || { echo "Could not run gofmt on tracked or unignored Go files"; exit 1; }; \
		test -z "$$fmt_output" || { printf '%s\n%s\n' "Go files need formatting; run go fmt ./..." "$$fmt_output"; exit 1; }

module-verify:
	$(GO) mod verify

module-tidy-check:
	$(GO) mod tidy -diff

license-check:
	@notice="$$(mktemp)"; \
		trap 'rm -f "$$notice"' EXIT INT TERM; \
		{ test ! -e THIRD_PARTY_LICENSES && test ! -L THIRD_PARTY_LICENSES; } || { \
			echo "THIRD_PARTY_LICENSES is generated for release artifacts and must not exist at the repository root"; \
			exit 1; \
		}; \
		GO="$(GO)" bash scripts/generate-third-party-licenses.sh >"$$notice"; \
		test -s "$$notice"; \
		grep -Fqx 'timer-cli third-party software notices' "$$notice"; \
		grep -Fq 'Component: Go runtime and standard library ' "$$notice"; \
		echo "Third-party license notices generate from linked release dependencies"

licenses:
	mkdir -p dist
	GO="$(GO)" bash scripts/generate-third-party-licenses.sh >dist/THIRD_PARTY_LICENSES
	@echo "Third-party license notices written to dist/THIRD_PARTY_LICENSES"

shellcheck-require:
	@command -v "$(SHELLCHECK)" >/dev/null 2>&1 || { \
		echo "Required tool not found: $(SHELLCHECK) (install ShellCheck or set SHELLCHECK=/path/to/shellcheck)"; \
		exit 1; \
	}

shell-lint: shellcheck-require
	$(SHELLCHECK) --external-sources scripts/*.sh

workflow-lint: shellcheck-require
	$(GO) -C tools/actionlint mod verify
	$(GO) -C tools/actionlint mod tidy -diff
	$(GO) -C tools/actionlint tool actionlint -shellcheck="$(SHELLCHECK)"

release-doc-check:
	@version="$$(GO="$(GO)" bash scripts/source-version.sh)"; \
		bash scripts/validate-release-docs.sh "$$version"

release-guard-test:
	GO="$(GO)" bash scripts/test-release-guards.sh

package package-check: VERSION ?= $(shell bash scripts/source-version.sh)

package:
	GO="$(GO)" bash scripts/package-release.sh "$(VERSION)" dist

package-check:
	GO="$(GO)" bash scripts/verify-release.sh "$(VERSION)"

clean:
	rm -rf bin dist coverage.out
