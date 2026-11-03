# terraform-provider-spaceman. The acceptance tests build SpaceMan from a checkout: SPACEMAN_SRC, or ../spaceman.
GO ?= go

.PHONY: all build test lint acc docs check

all: check

build:
	$(GO) build -trimpath -o terraform-provider-spaceman .

# Unit tests (no Terraform, no SpaceMan needed).
test:
	$(GO) vet ./... && $(GO) test -count=1 ./...

lint:
	golangci-lint run ./...

# Acceptance tests: Terraform >= 1.11 on PATH and a SpaceMan checkout (SPACEMAN_SRC, default ../spaceman).
acc:
	TF_ACC=1 $(GO) test -count=1 -timeout 30m -run '^TestAcc' ./internal/provider

# Regenerates docs/ from the schemas and examples (a test checks they are current).
docs:
	SM_TF_DOCS=1 $(GO) test -count=1 -run TestDocsUpToDate ./internal/provider

check: test lint
