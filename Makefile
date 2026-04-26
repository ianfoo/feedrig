# feedrig developer entry points.
#
# `make` is the friendly default that builds everything end-to-end.
# `make spa` builds just the React frontend.
# `make go` builds just the Go binary (requires a prior SPA build for embed).
# `make docker` produces the runtime image.
# `make clean` wipes build artifacts (does NOT touch data/ or media/).

SPA_DIR := spa
SPA_OUT := internal/web/static/app
GO_BIN := feedrig

.PHONY: all spa-deps spa go docker clean run

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

clean:
	rm -f $(GO_BIN)
	rm -rf $(SPA_OUT)/assets $(SPA_OUT)/index.html
	rm -rf $(SPA_DIR)/node_modules
