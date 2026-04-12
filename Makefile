.PHONY: build build-web build-go clean docs dev-server dev-client dev-bench dev-web test test-race lint test-e2e-nginx test-e2e-caddy bench-data soak-data compose-stack-up compose-stack-logs compose-stack-down compose-stack-clean test-compose-stack test-compose-stack-nginx test-compose-stack-caddy

# 编译输出目录
BIN_DIR=bin

# 完整构建：先前端、再后端，产出单文件二进制
build: build-web build-go

# 构建多平台发布包（Linux/macOS/Windows）
build-release: build-web
	@echo "📦 构建多平台发布包..."
	goreleaser release --snapshot --clean
	@echo "✅ 构建完成: dist/"

# 仅构建前端
build-web:
	@echo "🌐 构建前端..."
	cd web && bun install --frozen-lockfile && bun run build
	@echo "✅ 前端构建完成: web/dist/"

# 版本信息（可被外部覆盖）
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# ldflags 用于注入版本信息
LDFLAGS := -s -w \
	-X netsgo/pkg/version.Current=$(VERSION) \
	-X netsgo/pkg/version.Commit=$(COMMIT) \
	-X netsgo/pkg/version.Date=$(DATE)

# 仅构建后端（需要先构建前端，否则 go:embed 会失败）
build-go:
	@echo "🔨 编译 netsgo..."
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/netsgo ./cmd/netsgo/
	@echo "✅ 编译完成: $(BIN_DIR)/netsgo"

# 清理
clean:
	@echo "🧹 清理构建产物..."
	rm -rf $(BIN_DIR)
	rm -rf web/dist
	@echo "✅ 清理完成"

# 生成 CLI 文档
docs:
	@echo "📝 生成命令行文档..."
	go run ./cmd/netsgo/ docs --output ./docs/cli
	@echo "✅ 文档已生成到 docs/cli/"

# ========== 开发模式 ==========
# 三个终端各跑一个即可：
#   终端 1:  make dev-server
#   终端 2:  make dev-client
#   终端 3:  make dev-web

DEV_PORT ?= 9527
DEV_KEY  ?= sk-8ccf857d-db62-4806-9719-776900e0785d
STACK_PROXY ?= nginx
STACK_PROJECT ?= netsgo-stack-$(STACK_PROXY)
STACK_PROXY_PORT ?= 19080
STACK_UPSTREAM_PORT ?= 19081
STACK_TUNNEL_PORT ?= 19082
STACK_BASE_COMPOSE := $(CURDIR)/test/e2e/docker-compose.stack.yml
STACK_PROXY_COMPOSE := $(CURDIR)/test/e2e/docker-compose.stack.$(STACK_PROXY).yml

# 启动服务端（-tags dev 跳过 go:embed，使用 Vite 独立前端）
dev-server:
	go run -tags dev ./cmd/netsgo/ server --port $(DEV_PORT)

# 启动客户端，连接本地服务端
dev-client:
	go run -tags dev ./cmd/netsgo/ client --server ws://localhost:$(DEV_PORT) --key $(DEV_KEY) $(ARGS)

# 启动前端 Vite 开发服务器（热更新）
dev-web:
	cd web && bun run dev

# 运行压测
dev-bench:
	go run -tags dev ./cmd/netsgo/ benchmark

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	cd web && bun run lint

bench-data:
	go test ./pkg/mux -run '^$$' -bench 'BenchmarkDataChannelTransport_YamuxOverPipe_vs_WSConn' -benchmem

test-e2e-nginx:
	NETSGO_E2E_PROXY=nginx NETSGO_E2E_COMPOSE_FILE=$(CURDIR)/test/e2e/docker-compose.nginx.yml go test -tags=e2e ./test/e2e -run TestProxyE2E -count=1

test-e2e-caddy:
	NETSGO_E2E_PROXY=caddy NETSGO_E2E_COMPOSE_FILE=$(CURDIR)/test/e2e/docker-compose.caddy.yml go test -tags=e2e ./test/e2e -run TestProxyE2E -count=1

soak-data:
	PROXY_PORT=$(STACK_PROXY_PORT) UPSTREAM_PORT=$(STACK_UPSTREAM_PORT) TUNNEL_REMOTE_PORT=$(STACK_TUNNEL_PORT) NETSGO_E2E_COMPOSE_PROJECT=$(STACK_PROJECT)-soak NETSGO_E2E_STACK_COMPOSE_FILES=$(STACK_BASE_COMPOSE),$(STACK_PROXY_COMPOSE) NETSGO_E2E_SOAK_IDLE=45s NETSGO_E2E_SOAK_CYCLES=3 go test -tags=e2e ./test/e2e -run TestComposeStackSoak -count=1 -timeout 15m

compose-stack-up:
	PROXY_PORT=$(STACK_PROXY_PORT) UPSTREAM_PORT=$(STACK_UPSTREAM_PORT) TUNNEL_REMOTE_PORT=$(STACK_TUNNEL_PORT) docker compose -f $(STACK_BASE_COMPOSE) -f $(STACK_PROXY_COMPOSE) -p $(STACK_PROJECT) up -d --build --remove-orphans

compose-stack-logs:
	PROXY_PORT=$(STACK_PROXY_PORT) UPSTREAM_PORT=$(STACK_UPSTREAM_PORT) TUNNEL_REMOTE_PORT=$(STACK_TUNNEL_PORT) docker compose -f $(STACK_BASE_COMPOSE) -f $(STACK_PROXY_COMPOSE) -p $(STACK_PROJECT) logs -f

compose-stack-down:
	PROXY_PORT=$(STACK_PROXY_PORT) UPSTREAM_PORT=$(STACK_UPSTREAM_PORT) TUNNEL_REMOTE_PORT=$(STACK_TUNNEL_PORT) docker compose -f $(STACK_BASE_COMPOSE) -f $(STACK_PROXY_COMPOSE) -p $(STACK_PROJECT) down --remove-orphans

compose-stack-clean:
	PROXY_PORT=$(STACK_PROXY_PORT) UPSTREAM_PORT=$(STACK_UPSTREAM_PORT) TUNNEL_REMOTE_PORT=$(STACK_TUNNEL_PORT) docker compose -f $(STACK_BASE_COMPOSE) -f $(STACK_PROXY_COMPOSE) -p $(STACK_PROJECT) down -v --remove-orphans

test-compose-stack:
	PROXY_PORT=$(STACK_PROXY_PORT) UPSTREAM_PORT=$(STACK_UPSTREAM_PORT) TUNNEL_REMOTE_PORT=$(STACK_TUNNEL_PORT) NETSGO_E2E_COMPOSE_PROJECT=$(STACK_PROJECT) NETSGO_E2E_STACK_COMPOSE_FILES=$(STACK_BASE_COMPOSE),$(STACK_PROXY_COMPOSE) go test -tags=e2e ./test/e2e -run TestComposeStackE2E -count=1 -timeout 12m

test-compose-stack-nginx:
	$(MAKE) test-compose-stack STACK_PROXY=nginx STACK_PROJECT=netsgo-stack-nginx

test-compose-stack-caddy:
	$(MAKE) test-compose-stack STACK_PROXY=caddy STACK_PROJECT=netsgo-stack-caddy
