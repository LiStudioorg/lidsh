# lidsh 构建工具链：前端 Vite → internal/webui/dist（go:embed 源）→ 单二进制。
#
# 使用：
#   make build-frontend   # 构建 Vue3 前端到 internal/webui/dist
#   make build            # build-frontend + go build
#   make release          # 产出 ./release/lidsh 单二进制（linux amd64）
#   make test             # go build + go test ./...
#   make clean

GO ?= go
BIN := bin/lidsh
RELEASE_DIR := release
DIST := internal/webui/dist

.PHONY: all build-frontend build release test clean

all: build

# 前端源在 web/；产物写到 internal/webui/dist 供 //go:embed 嵌入。
build-frontend:
	cd web && pnpm install --frozen-lockfile 2>/dev/null || true
	cd web && pnpm build

build: build-frontend
	$(GO) build -o $(BIN) ./cmd/lidsh

release:
	@echo "==> building frontend"
	cd web && pnpm build
	@echo "==> building single binary (linux/amd64)"
	mkdir -p $(RELEASE_DIR)
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -ldflags "-s -w" -o $(RELEASE_DIR)/lidsh ./cmd/lidsh
	@echo "==> release/lidsh ready:"
	@ls -lh $(RELEASE_DIR)/lidsh

test: build-frontend
	$(GO) test ./...

clean:
	rm -rf $(BIN) $(RELEASE_DIR) $(DIST) web/node_modules
