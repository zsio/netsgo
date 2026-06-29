.PHONY: build build-web build-go build-desktop-sidecar build-desktop build-desktop-macos-local sign-desktop-macos-app package-desktop-macos-local clean docs dev-server dev-client dev-bench dev-web test test-race lint test-tdd-red test-tdd-red-client test-tdd-red-server test-system-e2e test-system-e2e-nginx test-system-e2e-caddy test-system-e2e-capability-loss test-playwright-e2e test-playwright-e2e-smoke test-playwright-e2e-full test-playwright-e2e-cdp test-playwright-e2e-cdp-smoke test-playwright-e2e-cdp-full test-playwright-e2e-cdp-run test-playwright-e2e-cdp-check test-playwright-e2e-run bench-data system-e2e-up system-e2e-logs system-e2e-down system-e2e-clean docker-build-e2e-current docker-build-e2e-capability-loss docker-build-e2e-stable test-baseline-e2e test-compat-e2e test-upgrade-e2e

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
DESKTOP_MACOS_APP ?= desktop/src-tauri/target/$(DESKTOP_TARGET_TRIPLE)/release/bundle/macos/NetsGo.app
DESKTOP_MACOS_DMG ?= desktop/src-tauri/target/$(DESKTOP_TARGET_TRIPLE)/release/bundle/macos/NetsGo-macOS-$(DESKTOP_TARGET_TRIPLE).dmg
DESKTOP_CODESIGN_IDENTITY ?= -
DESKTOP_CLEAR_QUARANTINE ?= 1

# 构建当前 Rust target 对应的 desktop client sidecar。使用 dev tag 跳过 server Web 面板嵌入。
build-desktop-sidecar:
	@VERSION="$(VERSION)" COMMIT="$(COMMIT)" DATE="$(DATE)" scripts/build-desktop-sidecar.sh "$(DESKTOP_TARGET_TRIPLE)"

# 本地验证 desktop 能消费上一步生成的 netsgo sidecar。默认只编译不打包安装器。
build-desktop: build-desktop-sidecar
	@echo "🖥️  构建 desktop..."
	cd desktop && bun install --frozen-lockfile && bun run tauri build --target "$(DESKTOP_TARGET_TRIPLE)" $(DESKTOP_BUNDLE_ARGS)
	@echo "✅ desktop 构建完成"

# 构建 macOS .app 并做本地可用的签名。没有 Apple Developer 账号时默认使用 ad-hoc identity "-".
build-desktop-macos-local: DESKTOP_BUNDLE_ARGS=--bundles app
build-desktop-macos-local: build-desktop sign-desktop-macos-app
	@echo "✅ macOS desktop 本地构建完成: $(DESKTOP_MACOS_APP)"

sign-desktop-macos-app:
	@CODESIGN_IDENTITY="$(DESKTOP_CODESIGN_IDENTITY)" CLEAR_QUARANTINE="$(DESKTOP_CLEAR_QUARANTINE)" scripts/sign-macos-app.sh "$(DESKTOP_MACOS_APP)"

package-desktop-macos-local: build-desktop-macos-local
	@echo "📦 打包 macOS desktop dmg..."
	@scripts/package-macos-dmg.sh "$(DESKTOP_MACOS_APP)" "$(DESKTOP_MACOS_DMG)"

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
DEV_KEY  ?=

# 服务端首次初始化参数（已初始化后自动忽略，均可通过环境变量覆盖）
# DEV_INIT_ADMIN_PASSWORD 必须由本地环境显式提供，避免把可预测的开发管理员密码写入源码。
DEV_INIT_ADMIN_USERNAME ?= admin
DEV_INIT_ADMIN_PASSWORD ?=
DEV_INIT_SERVER_ADDR    ?= http://localhost:$(DEV_PORT)
E2E_PROXY ?= nginx
E2E_PROJECT ?= netsgo-system-$(E2E_PROXY)
E2E_CAPABILITY_LOSS_PROJECT ?= netsgo-system-capability-loss
E2E_PROXY_PORT ?= 19080
E2E_UPSTREAM_PORT ?= 19081
E2E_SERVER_TCP_PORT ?= 19093
E2E_SERVER_UDP_PORT ?= 19094
E2E_SERVER_SOCKS5_PORT ?= 19095
E2E_SERVER_TCP_ALT_PORT ?= 19104
E2E_SERVER_UDP_ALT_PORT ?= 19105
E2E_SERVER_SOCKS5_ALT_PORT ?= 19106
E2E_C2C_SOCKS5_PORT ?= 19096
E2E_C2C_SOCKS5_DENY_PORT ?= 19097
E2E_C2C_TCP_PORT ?= 19098
E2E_C2C_TCP_ALT_PORT ?= 19099
E2E_C2C_TCP_SLOW_PORT ?= 19100
E2E_C2C_UDP_PORT ?= 19101
E2E_C2C_SOCKS5_AUTH_PORT ?= 19102
E2E_C2C_SOCKS5_SOURCE_DENY_PORT ?= 19103
E2E_BASE_COMPOSE := $(CURDIR)/test/e2e/docker-compose.system.yml
E2E_PROXY_COMPOSE := $(CURDIR)/test/e2e/docker-compose.proxy.$(E2E_PROXY).yml
E2E_PORT_ENV = \
	NETSGO_E2E_DIR=$(CURDIR) \
	PROXY_PORT=$(E2E_PROXY_PORT) \
	UPSTREAM_PORT=$(E2E_UPSTREAM_PORT) \
	SERVER_TCP_PORT=$(E2E_SERVER_TCP_PORT) \
	SERVER_UDP_PORT=$(E2E_SERVER_UDP_PORT) \
	SERVER_SOCKS5_PORT=$(E2E_SERVER_SOCKS5_PORT) \
	SERVER_TCP_ALT_PORT=$(E2E_SERVER_TCP_ALT_PORT) \
	SERVER_UDP_ALT_PORT=$(E2E_SERVER_UDP_ALT_PORT) \
	SERVER_SOCKS5_ALT_PORT=$(E2E_SERVER_SOCKS5_ALT_PORT) \
	C2C_SOCKS5_PORT=$(E2E_C2C_SOCKS5_PORT) \
	C2C_SOCKS5_DENY_PORT=$(E2E_C2C_SOCKS5_DENY_PORT) \
	C2C_TCP_PORT=$(E2E_C2C_TCP_PORT) \
	C2C_TCP_ALT_PORT=$(E2E_C2C_TCP_ALT_PORT) \
	C2C_TCP_SLOW_PORT=$(E2E_C2C_TCP_SLOW_PORT) \
	C2C_UDP_PORT=$(E2E_C2C_UDP_PORT) \
	C2C_SOCKS5_AUTH_PORT=$(E2E_C2C_SOCKS5_AUTH_PORT) \
	C2C_SOCKS5_SOURCE_DENY_PORT=$(E2E_C2C_SOCKS5_SOURCE_DENY_PORT)
PLAYWRIGHT_PROJECT ?= netsgo-playwright
PLAYWRIGHT_SERVER_PORT ?= 19180
PLAYWRIGHT_TCP_INGRESS_PORT ?= 19190
PLAYWRIGHT_UDP_INGRESS_PORT ?= 19191
PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT ?= 19192
PLAYWRIGHT_TCP_EDIT_INGRESS_PORT ?= 19193
LOCAL_CHROME_CDP_ENDPOINT ?= http://127.0.0.1:9222
LOCAL_CHROME_CDP_SLOW_MO_MS ?= 250
LOCAL_CHROME_CDP_FINISH_DELAY_MS ?= 5000
LOCAL_CHROME_CDP_KEEP_TAB ?= 1
PLAYWRIGHT_COMPOSE := $(CURDIR)/test/e2e/docker-compose.playwright.yml

# 启动服务端（-tags dev 跳过 go:embed，使用 Vite 独立前端）
dev-server:
	@if [ -z "$(strip $(DEV_INIT_ADMIN_PASSWORD))" ]; then \
		echo "DEV_INIT_ADMIN_PASSWORD is required. Example:"; \
		echo "  DEV_INIT_ADMIN_PASSWORD=NetsGo1-$$(openssl rand -hex 12 2>/dev/null || uuidgen) make dev-server"; \
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
	@if [ -z "$(strip $(DEV_KEY))" ]; then \
		echo "DEV_KEY is required. Create an API key in the admin panel, then run:"; \
		echo "  DEV_KEY=<client-api-key> make dev-client"; \
		exit 1; \
	fi
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

test-tdd-red: test-tdd-red-client test-tdd-red-server

test-tdd-red-client:
	go test ./internal/client -run 'TestClient_HandleStream_Fixed(TCP|UDP|HTTP)Target|TestClientControlLoopUnifiedPayloadIgnoresLegacyFlatFields|TestUnifiedClientRuntime(DoesNotCallProxyRequestFromTunnelSpec|DefinesFixedTargetStore)|TestClientCleanupClearsFixedTargetRuntimes|TestClientHandleStreamUsesFixedTargetRuntimes|TestClientHandleTunnelUnprovisionUsesFixedTargetRuntimes|TestClientTunnelProvisionFixed(TCP|UDP)TargetDoesNotRegisterLegacyProxy|TestClientTunnelProvisionUnsupportedTargetRejectsWithoutRuntime' -count=1

test-tdd-red-server:
	go test ./internal/server -run 'TestUnifiedReconcileRegistry(SerializesSameTunnelAndRerunsDirty|CoalescesMultipleDirtyCallsIntoSingleRerun)|TestUnifiedServerExpose(ReconcileRejectsStaleProvisionAckAfterRevisionAdvance|RejectedProvisionLeavesNoListenerOrAckWaiter)' -count=1

lint:
	cd web && bun run lint

bench-data:
	go test ./pkg/mux -run '^$$' -bench 'BenchmarkDataChannelTransport_YamuxOverPipe_vs_WSConn' -benchmem

test-system-e2e: test-system-e2e-nginx test-system-e2e-caddy

test-system-e2e-nginx:
	$(MAKE) test-system-e2e-run E2E_PROXY=nginx E2E_PROJECT=netsgo-system-nginx

test-system-e2e-caddy:
	$(MAKE) test-system-e2e-run E2E_PROXY=caddy E2E_PROJECT=netsgo-system-caddy

test-system-e2e-capability-loss: docker-build-e2e-current docker-build-e2e-capability-loss
	@admin_pass="$${NETSGO_ADMIN_PASS:-NetsGo1-$$(openssl rand -hex 12 2>/dev/null || uuidgen)}"; \
	$(E2E_PORT_ENV) \
	NETSGO_ADMIN_PASS="$${admin_pass}" \
	NETSGO_E2E_COMPOSE_PROJECT=$(E2E_CAPABILITY_LOSS_PROJECT) \
	NETSGO_E2E_COMPOSE_FILES=$(E2E_BASE_COMPOSE),$(E2E_PROXY_COMPOSE) \
	NETSGO_SERVER_IMAGE="$(E2E_CURRENT_IMAGE)" \
	NETSGO_TARGET_CLIENT_IMAGE="$(E2E_CURRENT_IMAGE)" \
	NETSGO_INGRESS_CLIENT_IMAGE="$(E2E_CURRENT_IMAGE)" \
	NETSGO_E2E_TOOLS_IMAGE="$(E2E_CURRENT_IMAGE)" \
	NETSGO_E2E_CAPABILITY_LOSS_IMAGE="$(E2E_CAPABILITY_LOSS_IMAGE)" \
	NETSGO_E2E_COMPOSE_BUILD=0 \
	go test -tags=e2e ./test/e2e -run '^TestSystemCapabilityLossReconcileE2E$$' -count=1 -timeout 10m

test-system-e2e-run:
	@admin_pass="$${NETSGO_ADMIN_PASS:-NetsGo1-$$(openssl rand -hex 12 2>/dev/null || uuidgen)}"; \
	$(E2E_PORT_ENV) \
	NETSGO_ADMIN_PASS="$${admin_pass}" \
	NETSGO_E2E_COMPOSE_PROJECT=$(E2E_PROJECT) \
	NETSGO_E2E_COMPOSE_FILES=$(E2E_BASE_COMPOSE),$(E2E_PROXY_COMPOSE) \
	go test -tags=e2e ./test/e2e -run 'TestSystem.*E2E' -count=1 -timeout 20m

test-playwright-e2e: test-playwright-e2e-smoke

test-playwright-e2e-smoke: PLAYWRIGHT_ARGS=--grep @smoke
test-playwright-e2e-smoke: test-playwright-e2e-run

test-playwright-e2e-full: test-playwright-e2e-run

test-playwright-e2e-cdp: test-playwright-e2e-cdp-smoke

test-playwright-e2e-cdp-smoke: PLAYWRIGHT_ARGS=--grep @smoke
test-playwright-e2e-cdp-smoke: test-playwright-e2e-cdp-run

test-playwright-e2e-cdp-full: test-playwright-e2e-cdp-run

test-playwright-e2e-cdp-run: PLAYWRIGHT_CDP_ENDPOINT=$(LOCAL_CHROME_CDP_ENDPOINT)
test-playwright-e2e-cdp-run: test-playwright-e2e-cdp-check test-playwright-e2e-run

test-playwright-e2e-cdp-check:
	@curl -fsS "$(LOCAL_CHROME_CDP_ENDPOINT)/json/version" >/dev/null || { \
		echo "Chrome CDP endpoint is not reachable: $(LOCAL_CHROME_CDP_ENDPOINT)"; \
		echo "Start your local Chrome CDP profile first:"; \
		echo "  devtools"; \
		exit 1; \
	}
	@osascript -e 'tell application "Google Chrome" to activate' >/dev/null 2>&1 || true

test-playwright-e2e-run: build-web
	@set -e; \
	admin_pass="$${NETSGO_ADMIN_PASS:-NetsGo1-$$(openssl rand -hex 12 2>/dev/null || uuidgen)}"; \
	playwright_cdp_endpoint="$${PLAYWRIGHT_CDP_ENDPOINT:-}"; \
	if [ -z "$${playwright_cdp_endpoint}" ] && curl -fsS "$(LOCAL_CHROME_CDP_ENDPOINT)/json/version" >/dev/null 2>&1; then \
		playwright_cdp_endpoint="$(LOCAL_CHROME_CDP_ENDPOINT)"; \
		echo "Using local Chrome CDP endpoint: $${playwright_cdp_endpoint}"; \
	fi; \
	cleanup() { \
		PLAYWRIGHT_SERVER_PORT=$(PLAYWRIGHT_SERVER_PORT) \
		PLAYWRIGHT_TCP_INGRESS_PORT=$(PLAYWRIGHT_TCP_INGRESS_PORT) \
		PLAYWRIGHT_UDP_INGRESS_PORT=$(PLAYWRIGHT_UDP_INGRESS_PORT) \
		PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT=$(PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT) \
		PLAYWRIGHT_TCP_EDIT_INGRESS_PORT=$(PLAYWRIGHT_TCP_EDIT_INGRESS_PORT) \
		NETSGO_ADMIN_PASS="$${admin_pass}" \
		docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) down -v --remove-orphans; \
	}; \
	trap cleanup EXIT; \
	PLAYWRIGHT_SERVER_PORT=$(PLAYWRIGHT_SERVER_PORT) \
	PLAYWRIGHT_TCP_INGRESS_PORT=$(PLAYWRIGHT_TCP_INGRESS_PORT) \
	PLAYWRIGHT_UDP_INGRESS_PORT=$(PLAYWRIGHT_UDP_INGRESS_PORT) \
	PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT=$(PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT) \
	PLAYWRIGHT_TCP_EDIT_INGRESS_PORT=$(PLAYWRIGHT_TCP_EDIT_INGRESS_PORT) \
	NETSGO_ADMIN_PASS="$${admin_pass}" \
	docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) up -d --build --remove-orphans; \
	cd web && \
	NETSGO_E2E_BASE_URL=http://127.0.0.1:$(PLAYWRIGHT_SERVER_PORT) \
	NETSGO_ADMIN_PASS="$${admin_pass}" \
	PLAYWRIGHT_TCP_INGRESS_PORT=$(PLAYWRIGHT_TCP_INGRESS_PORT) \
	PLAYWRIGHT_UDP_INGRESS_PORT=$(PLAYWRIGHT_UDP_INGRESS_PORT) \
	PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT=$(PLAYWRIGHT_TCP_LIFECYCLE_INGRESS_PORT) \
	PLAYWRIGHT_TCP_EDIT_INGRESS_PORT=$(PLAYWRIGHT_TCP_EDIT_INGRESS_PORT) \
	PLAYWRIGHT_CDP_ENDPOINT="$${playwright_cdp_endpoint}" \
	PLAYWRIGHT_CDP_SLOW_MO_MS="$(LOCAL_CHROME_CDP_SLOW_MO_MS)" \
	PLAYWRIGHT_CDP_FINISH_DELAY_MS="$(LOCAL_CHROME_CDP_FINISH_DELAY_MS)" \
	PLAYWRIGHT_CDP_KEEP_TAB="$(LOCAL_CHROME_CDP_KEEP_TAB)" \
	$${PLAYWRIGHT_RUNNER:-} bun run e2e:playwright $(if $(PLAYWRIGHT_ARGS),-- $(PLAYWRIGHT_ARGS),) || status=$$?; \
	if [ "$${status:-0}" -ne 0 ]; then \
		NETSGO_ADMIN_PASS="$${admin_pass}" docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) ps; \
		NETSGO_ADMIN_PASS="$${admin_pass}" docker compose -f $(PLAYWRIGHT_COMPOSE) -p $(PLAYWRIGHT_PROJECT) logs --no-color --tail 200; \
		exit $$status; \
	fi

system-e2e-up:
	@if [ -z "$${NETSGO_ADMIN_PASS:-}" ]; then \
		echo "NETSGO_ADMIN_PASS is required for system-e2e-up."; \
		echo "  NETSGO_ADMIN_PASS=NetsGo1-$$(openssl rand -hex 12 2>/dev/null || uuidgen) make system-e2e-up"; \
		exit 1; \
	fi; \
	$(E2E_PORT_ENV) NETSGO_ADMIN_PASS="$${NETSGO_ADMIN_PASS}" docker compose -f $(E2E_BASE_COMPOSE) -f $(E2E_PROXY_COMPOSE) -p $(E2E_PROJECT) up -d --build --remove-orphans

system-e2e-logs:
	$(E2E_PORT_ENV) NETSGO_ADMIN_PASS="$${NETSGO_ADMIN_PASS:-unused-for-compose-command}" docker compose -f $(E2E_BASE_COMPOSE) -f $(E2E_PROXY_COMPOSE) -p $(E2E_PROJECT) logs -f

system-e2e-down:
	$(E2E_PORT_ENV) NETSGO_ADMIN_PASS="$${NETSGO_ADMIN_PASS:-unused-for-compose-command}" docker compose -f $(E2E_BASE_COMPOSE) -f $(E2E_PROXY_COMPOSE) -p $(E2E_PROJECT) down --remove-orphans

system-e2e-clean:
	$(E2E_PORT_ENV) NETSGO_ADMIN_PASS="$${NETSGO_ADMIN_PASS:-unused-for-compose-command}" docker compose -f $(E2E_BASE_COMPOSE) -f $(E2E_PROXY_COMPOSE) -p $(E2E_PROJECT) down -v --remove-orphans

# ========== Compatibility / Upgrade E2E ==========

COMPAT_BASELINE ?= v0.1.8
E2E_CURRENT_IMAGE ?= netsgo-e2e:current
E2E_CAPABILITY_LOSS_IMAGE ?= netsgo-e2e:capability-loss
E2E_STABLE_IMAGE ?= netsgo-e2e:$(COMPAT_BASELINE)
COMPAT_MODE ?= full
COMPAT_ABORT_ON_FAILURE ?= false
BASELINE_MODE ?= full
BASELINE_REBUILD_IMAGE ?= false
UPGRADE_RECOVERY_TIMEOUT_SECONDS ?= 120

docker-build-e2e-current: build-web
	@echo "Building e2e image $(E2E_CURRENT_IMAGE) from current code..."
	docker buildx build --load --target e2e \
		--build-arg NETSGO_VERSION=$(VERSION) \
		--build-arg NETSGO_COMMIT=$(COMMIT) \
		--build-arg NETSGO_DATE=$(DATE) \
		-t $(E2E_CURRENT_IMAGE) .

docker-build-e2e-capability-loss: build-web
	@echo "Building e2e capability-loss image $(E2E_CAPABILITY_LOSS_IMAGE) from current code..."
	docker buildx build --load --target e2e \
		--build-arg NETSGO_VERSION=$(VERSION) \
		--build-arg NETSGO_COMMIT=$(COMMIT) \
		--build-arg NETSGO_DATE=$(DATE) \
		--build-arg NETSGO_GO_TAGS=e2e_capability_loss \
		-t $(E2E_CAPABILITY_LOSS_IMAGE) .

docker-build-e2e-stable:
	@bash $(CURDIR)/test/e2e/scripts/build-e2e-stable.sh "$(COMPAT_BASELINE)" "$(E2E_STABLE_IMAGE)"

test-baseline-e2e:
	@E2E_PROXY="$(E2E_PROXY)" \
	E2E_PROJECT="$(E2E_PROJECT)" \
	E2E_BASE_COMPOSE="$(E2E_BASE_COMPOSE)" \
	E2E_PROXY_COMPOSE="$(E2E_PROXY_COMPOSE)" \
	PROXY_PORT="$(E2E_PROXY_PORT)" \
	UPSTREAM_PORT="$(E2E_UPSTREAM_PORT)" \
	SERVER_TCP_PORT="$(E2E_SERVER_TCP_PORT)" \
	SERVER_UDP_PORT="$(E2E_SERVER_UDP_PORT)" \
	SERVER_SOCKS5_PORT="$(E2E_SERVER_SOCKS5_PORT)" \
	SERVER_TCP_ALT_PORT="$(E2E_SERVER_TCP_ALT_PORT)" \
	SERVER_UDP_ALT_PORT="$(E2E_SERVER_UDP_ALT_PORT)" \
	SERVER_SOCKS5_ALT_PORT="$(E2E_SERVER_SOCKS5_ALT_PORT)" \
	C2C_SOCKS5_PORT="$(E2E_C2C_SOCKS5_PORT)" \
	C2C_SOCKS5_DENY_PORT="$(E2E_C2C_SOCKS5_DENY_PORT)" \
	C2C_TCP_PORT="$(E2E_C2C_TCP_PORT)" \
	C2C_TCP_ALT_PORT="$(E2E_C2C_TCP_ALT_PORT)" \
	C2C_TCP_SLOW_PORT="$(E2E_C2C_TCP_SLOW_PORT)" \
	C2C_UDP_PORT="$(E2E_C2C_UDP_PORT)" \
	C2C_SOCKS5_AUTH_PORT="$(E2E_C2C_SOCKS5_AUTH_PORT)" \
	C2C_SOCKS5_SOURCE_DENY_PORT="$(E2E_C2C_SOCKS5_SOURCE_DENY_PORT)" \
	COMPAT_BASELINE="$(COMPAT_BASELINE)" \
	E2E_STABLE_IMAGE="$(E2E_STABLE_IMAGE)" \
	BASELINE_MODE="$(BASELINE_MODE)" \
	BASELINE_REBUILD_IMAGE="$(BASELINE_REBUILD_IMAGE)" \
	NETSGO_E2E_DIR="$(CURDIR)" \
	bash $(CURDIR)/test/e2e/scripts/test-baseline.sh

test-compat-e2e:
	@E2E_PROXY="$(E2E_PROXY)" \
	E2E_PROJECT="$(E2E_PROJECT)" \
	E2E_BASE_COMPOSE="$(E2E_BASE_COMPOSE)" \
	E2E_PROXY_COMPOSE="$(E2E_PROXY_COMPOSE)" \
	PROXY_PORT="$(E2E_PROXY_PORT)" \
	UPSTREAM_PORT="$(E2E_UPSTREAM_PORT)" \
	SERVER_TCP_PORT="$(E2E_SERVER_TCP_PORT)" \
	SERVER_UDP_PORT="$(E2E_SERVER_UDP_PORT)" \
	SERVER_SOCKS5_PORT="$(E2E_SERVER_SOCKS5_PORT)" \
	SERVER_TCP_ALT_PORT="$(E2E_SERVER_TCP_ALT_PORT)" \
	SERVER_UDP_ALT_PORT="$(E2E_SERVER_UDP_ALT_PORT)" \
	SERVER_SOCKS5_ALT_PORT="$(E2E_SERVER_SOCKS5_ALT_PORT)" \
	C2C_SOCKS5_PORT="$(E2E_C2C_SOCKS5_PORT)" \
	C2C_SOCKS5_DENY_PORT="$(E2E_C2C_SOCKS5_DENY_PORT)" \
	C2C_TCP_PORT="$(E2E_C2C_TCP_PORT)" \
	C2C_TCP_ALT_PORT="$(E2E_C2C_TCP_ALT_PORT)" \
	C2C_TCP_SLOW_PORT="$(E2E_C2C_TCP_SLOW_PORT)" \
	C2C_UDP_PORT="$(E2E_C2C_UDP_PORT)" \
	C2C_SOCKS5_AUTH_PORT="$(E2E_C2C_SOCKS5_AUTH_PORT)" \
	C2C_SOCKS5_SOURCE_DENY_PORT="$(E2E_C2C_SOCKS5_SOURCE_DENY_PORT)" \
	COMPAT_BASELINE="$(COMPAT_BASELINE)" \
	E2E_CURRENT_IMAGE="$(E2E_CURRENT_IMAGE)" \
	E2E_STABLE_IMAGE="$(E2E_STABLE_IMAGE)" \
	COMPAT_MODE="$(COMPAT_MODE)" \
	COMPAT_ABORT_ON_FAILURE="$(COMPAT_ABORT_ON_FAILURE)" \
	NETSGO_E2E_DIR="$(CURDIR)" \
	bash $(CURDIR)/test/e2e/scripts/test-compat.sh

test-upgrade-e2e:
	@E2E_PROXY="$(E2E_PROXY)" \
	E2E_PROJECT="$(E2E_PROJECT)" \
	E2E_BASE_COMPOSE="$(E2E_BASE_COMPOSE)" \
	E2E_PROXY_COMPOSE="$(E2E_PROXY_COMPOSE)" \
	PROXY_PORT="$(E2E_PROXY_PORT)" \
	UPSTREAM_PORT="$(E2E_UPSTREAM_PORT)" \
	SERVER_TCP_PORT="$(E2E_SERVER_TCP_PORT)" \
	SERVER_UDP_PORT="$(E2E_SERVER_UDP_PORT)" \
	SERVER_SOCKS5_PORT="$(E2E_SERVER_SOCKS5_PORT)" \
	SERVER_TCP_ALT_PORT="$(E2E_SERVER_TCP_ALT_PORT)" \
	SERVER_UDP_ALT_PORT="$(E2E_SERVER_UDP_ALT_PORT)" \
	SERVER_SOCKS5_ALT_PORT="$(E2E_SERVER_SOCKS5_ALT_PORT)" \
	C2C_SOCKS5_PORT="$(E2E_C2C_SOCKS5_PORT)" \
	C2C_SOCKS5_DENY_PORT="$(E2E_C2C_SOCKS5_DENY_PORT)" \
	C2C_TCP_PORT="$(E2E_C2C_TCP_PORT)" \
	C2C_TCP_ALT_PORT="$(E2E_C2C_TCP_ALT_PORT)" \
	C2C_TCP_SLOW_PORT="$(E2E_C2C_TCP_SLOW_PORT)" \
	C2C_UDP_PORT="$(E2E_C2C_UDP_PORT)" \
	C2C_SOCKS5_AUTH_PORT="$(E2E_C2C_SOCKS5_AUTH_PORT)" \
	C2C_SOCKS5_SOURCE_DENY_PORT="$(E2E_C2C_SOCKS5_SOURCE_DENY_PORT)" \
	COMPAT_BASELINE="$(COMPAT_BASELINE)" \
	E2E_CURRENT_IMAGE="$(E2E_CURRENT_IMAGE)" \
	E2E_STABLE_IMAGE="$(E2E_STABLE_IMAGE)" \
	NETSGO_E2E_TOOLS_IMAGE="$(E2E_STABLE_IMAGE)" \
	UPGRADE_RECOVERY_TIMEOUT_SECONDS="$(UPGRADE_RECOVERY_TIMEOUT_SECONDS)" \
	NETSGO_E2E_DIR="$(CURDIR)" \
	bash $(CURDIR)/test/e2e/scripts/test-upgrade.sh
