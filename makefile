# Hochzeitsgalerie: Go-Server (cmd/galerie) mit eingebetteten Templates und statischen Dateien (ui/).

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
# docker oder podman, je nachdem was installiert ist
CONTAINER ?= $(shell command -v docker 2>/dev/null || command -v podman 2>/dev/null)

.PHONY: help dev build build-linux css watch-css test check docker clean

help: ## Diese Hilfe
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-12s %s\n", $$1, $$2}'

dev: ## Server auf :8080 mit den Einstellungen aus .env.local
	@test -f .env.local || (cp .env.local.example .env.local && echo ".env.local aus .env.local.example angelegt")
	go run ./cmd/galerie

build: ## bin/galerie bauen (statisch, ohne CGO)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/galerie ./cmd/galerie

build-linux: ## bin/galerie-linux-{amd64,arm64} für den Server (z. B. vom Mac aus)
	for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o bin/galerie-linux-$$arch ./cmd/galerie; \
	done

css: ## ui/static/css/app.css aus ui/frontend/app.css bauen (unminifiziert, so liegt es im Repo)
	@test -d node_modules || npm ci --no-audit --no-fund
	npm run build:css

watch-css: ## CSS bei jeder Änderung an Templates neu bauen
	@test -d node_modules || npm ci --no-audit --no-fund
	npm run watch:css

test: ## Alle Tests
	go test ./...

check: ## go vet und gofmt
	go vet ./...
	test -z "$$(gofmt -l cmd internal ui)"

docker: ## Container-Image hochzeit-galerie:latest bauen
	$(CONTAINER) build --build-arg VERSION=$(VERSION) -t hochzeit-galerie:latest .

clean: ## Build-Ergebnisse entfernen
	rm -rf bin
