# feedrig developer entry points.
#
# Production build:
#   make            builds the SPA + Go binary into ./feedrig
#   make docker     produces the runtime image
#
# Development (live reload):
#   make dev        Go live-reload via Air. Restarts ./feedrig on
#                   changes to *.go / templates / static assets.
#   make dev-spa    Vite dev server on :5173 with HMR. Proxies API
#                   and /media/ to the Go server on :7777.
#                   Open http://localhost:5173/app/ for the SPA.
#                   Run `make dev` in another terminal so the API
#                   it proxies to is alive.
#
# Other:
#   make spa        build SPA only
#   make go         build Go binary only (requires a prior SPA build)
#   make clean      wipe build artifacts (does NOT touch data/ or media/)

SPA_DIR := spa
SPA_OUT := internal/web/static/app
GO_BIN := feedrig

.PHONY: all spa-deps spa go docker clean run dev dev-spa air-install

all: spa go

spa-deps:
	cd $(SPA_DIR) && npm install --no-audit --no-fund --silent

spa: spa-deps
	cd $(SPA_DIR) && npm run build

go:
	go build -o $(GO_BIN) .

docker:
	docker build -t feedrig .

run: all
	./$(GO_BIN)

# Air-based live reload for the Go side. Installs Air on first use.
dev: air-install
	air

dev-spa: spa-deps
	cd $(SPA_DIR) && npm run dev

air-install:
	@command -v air >/dev/null 2>&1 || { \
	  echo "Installing Air (github.com/air-verse/air)…"; \
	  go install github.com/air-verse/air@latest; \
	}

clean:
	rm -f $(GO_BIN)
	rm -rf $(SPA_OUT)/assets $(SPA_OUT)/index.html
	rm -rf $(SPA_DIR)/node_modules
	rm -rf tmp
