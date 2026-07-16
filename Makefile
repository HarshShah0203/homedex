.PHONY: build web test test-e2e check demo demo-down image smoke

VERSION ?= dev

web:
	npm --prefix web ci
	npm --prefix web run build

build: web
	go build -trimpath -ldflags="-s -w -X main.version=$(VERSION)" -o homedex ./cmd/homedex

test:
	go test ./...
	npm --prefix web test

test-e2e:
	./scripts/test-production-e2e.sh

check: test
	go vet ./...
	npm --prefix web run check
	./scripts/check-npm-audit.sh
	./scripts/check-redaction.sh
	./scripts/check-compose-security.sh

demo:
	docker compose -f demo/compose.yml up --build -d

demo-down:
	docker compose -f demo/compose.yml down

image:
	docker build --build-arg VERSION=$(VERSION) -t homedex:$(VERSION) .

smoke: build
	./scripts/smoke-startup.sh ./homedex
	./scripts/smoke-fake-lab.sh ./homedex
