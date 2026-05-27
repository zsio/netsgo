.PHONY: build build-web build-go build-desktop-sidecar build-desktop clean docs dev-server dev-client dev-bench dev-web test test-race lint test-e2e-nginx test-e2e-caddy test-playwright-e2e test-playwright-e2e-smoke test-playwright-e2e-full test-playwright-e2e-run bench-data soak-data compose-stack-up compose-stack-logs compose-stack-down compose-stack-clean test-compose-stack test-compose-stack-nginx test-compose-stack-caddy

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

DESKTOP_TARGET_TRIPLE ?= $(shell rustc --print host-tuple 2>/dev/null || rustc -vV 2>/dev/null | sed -n 's/^host: //p')
DESKTOP_BUNDLE_ARGS ?= --no-bundle

# 构建当前 Rust target 对应的 desktop client sidecar。使用 dev tag 跳过 server Web 面板嵌入。
build-desktop-sidecar:
	@VERSION="$(VERSION)" COMMIT="$(COMMIT)" DATE="$(DATE)" scripts/build-desktop-sidecar.sh "$(DESKTOP_TARGET_TRIPLE)"

# 本地验证 desktop 能消费上一步生成的 netsgo sidecar。默认只编译不打包安装器。
build-desktop: build-desktop-sidecar
	@echo "🖥️  构建 desktop..."
	cd desktop && bun install --frozen-lockfile && bun run tauri build --target "$(DESKTOP_TARGET_TRIPLE)" $(DESKTOP_BUNDLE_ARGS)
	@echo "✅ desktop 构建完成"

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

# 服务端首次初始化参数（已初始化后自动忽略，均可通过环境变量覆盖）
# DEV_INIT_ADMIN_PASSWORD 必须由本地环境显式提供，避免把可预测的开发管理员密码写入源码。
DEV_INIT_ADMIN_USERNAME ?= admin
DEV_INIT_ADMIN_PASSWORD ?= admin.2026
DEV_INIT_SERVER_ADDR    ?= http://localhost:$(DEV_PORT)
STACK_PROXY ?= nginx
STACK_PROJECT ?= netsgo-stack-$(STACK_PROXY)
STACK_PROXY_PORT ?= 19080
STACK_UPSTREAM_PORT ?= 19081
STACK_TUNNEL_PORT ?= 19082
STACK_BASE_COMPOSE := $(CURDIR)/test/e2e/docker-compose.stack.yml
STACK_PROXY_COMPOSE := $(CURDIR)/test/e2e/docker-compose.stack.$(STACK_PROXY).yml
PLAYWRIGHT_PROJECT ?= netsgo-playwright
PLAYWRIGHT_SERVER_PORT ?= 19180
PLAYWRIGHT_TCP_INGRESS_PORT ?= 19190
PLAYWRIGHT_UDP_INGRESS_PORT ?= 19191
PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT ?= 19192
PLAYWRIGHT_TCP_EDIT_INGRESS_PORT ?= 19193
PLAYWRIGHT_COMPOSE := $(CURDIR)/test/e2e/docker-compose.playwright.yml

# 启动服务端（-tags dev 跳过 go:embed，使用 Vite 独立前端）
dev-server:
	@if [ -z "$(strip $(DEV_INIT_ADMIN_PASSWORD))" ]; then \
		echo "DEV_INIT_ADMIN_PASSWORD is required. Example:"; \
		echo "  DEV_INIT_ADMIN_PASSWORD=$$(openssl rand -base64 18 2>/dev/null || uuidgen) make dev-server"; \
		exit 1; \
	fi
	go run -tags dev ./cmd/netsgo/ server \
		--port $(DEV_PORT) \
		--allow-loopback-management-host \
		--init-admin-username $(DEV_INIT_ADMIN_USERNAME) \
		--init-admin-password $(DEV_INIT_ADMIN_PASSWORD) \
		--init-server-addr $(DEV_INIT_SERVER_ADDR)

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

test-playwright-e2e: test-playwright-e2e-smoke

test-playwright-e2e-smoke: PLAYWRIGHT_ARGS=--grep @smoke
test-playwright-e2e-smoke: test-playwright-e2e-run

test-playwright-e2e-full: test-playwright-e2e-run

test-playwright-e2e-run: build-web
	@set -e; \
	cleanup() { \
		PLAYWRIGHT_SERVER_PORT=$(PLAYWRIGHT_SERVER_PORT) \
		PLAYWRIGHT_TCP_INGRESS_PORT=$(PLAYWRIGHT_TCP_INGRESS_PORT) \
		PLAYWRIGHT_UDP_INGRESS_PORT=$(PLAYWRIGHT_UDP_INGRESS_PORT) \
		PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT=$(PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT) \
		PLAYWRIGHT_TCP_EDIT_INGRESS_PORT=$(PLAYWRIGHT_TCP_EDIT_INGRESS_PORT) \
		docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) down -v --remove-orphans; \
	}; \
	trap cleanup EXIT; \
	PLAYWRIGHT_SERVER_PORT=$(PLAYWRIGHT_SERVER_PORT) \
	PLAYWRIGHT_TCP_INGRESS_PORT=$(PLAYWRIGHT_TCP_INGRESS_PORT) \
	PLAYWRIGHT_UDP_INGRESS_PORT=$(PLAYWRIGHT_UDP_INGRESS_PORT) \
	PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT=$(PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT) \
	PLAYWRIGHT_TCP_EDIT_INGRESS_PORT=$(PLAYWRIGHT_TCP_EDIT_INGRESS_PORT) \
	docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) up -d --build --remove-orphans; \
	cd web && \
	NETSGO_E2E_BASE_URL=http://127.0.0.1:$(PLAYWRIGHT_SERVER_PORT) \
	PLAYWRIGHT_TCP_INGRESS_PORT=$(PLAYWRIGHT_TCP_INGRESS_PORT) \
	PLAYWRIGHT_UDP_INGRESS_PORT=$(PLAYWRIGHT_UDP_INGRESS_PORT) \
	PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT=$(PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT) \
	PLAYWRIGHT_TCP_EDIT_INGRESS_PORT=$(PLAYWRIGHT_TCP_EDIT_INGRESS_PORT) \
	bun run e2e:playwright $(if $(PLAYWRIGHT_ARGS),-- $(PLAYWRIGHT_ARGS),) || status=$$?; \
	if [ "$${status:-0}" -ne 0 ]; then \
		docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) ps; \
		docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) logs --no-color --tail 200; \
		exit $$status; \
	fi

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
