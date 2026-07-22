SHELL := /bin/bash
VERSION ?= dev
DIST := dist

.PHONY: all frontend frontend-install frontend-dev frontend-typecheck backend dev-backend test vet check release clean

all: backend

frontend-install:
	cd web && npm ci

frontend:
	cd web && npm run build

frontend-dev:
	cd web && npm run dev

frontend-typecheck:
	cd web && npm run typecheck

backend: frontend
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o sbm-panel ./cmd/sbm-panel

dev-backend:
	go run ./cmd/sbm-panel serve --http

test:
	go test ./...

vet:
	go vet ./...

check: test vet frontend-typecheck

release: frontend
	rm -rf $(DIST)
	mkdir -p $(DIST)
	cp install.sh "$(DIST)/sbm"
	for arch in amd64 arm64; do \
		name="sbm-panel_$(VERSION)_linux_$${arch}"; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$${arch} go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o "$(DIST)/sbm-panel" ./cmd/sbm-panel; \
		tar -C $(DIST) -czf "$(DIST)/$${name}.tar.gz" sbm-panel sbm; \
	done
	rm -f $(DIST)/sbm-panel $(DIST)/sbm
	cd $(DIST) && if command -v sha256sum >/dev/null; then sha256sum *.tar.gz > checksums.txt; else shasum -a 256 *.tar.gz > checksums.txt; fi

clean:
	rm -rf $(DIST) sbm-panel
