.PHONY: build build-web build-go clean docs dev-server dev-client dev-bench dev-web test lint

# 编译输出目录
BIN_DIR=bin

# 完整构建：先前端、再后端，产出单文件二进制
build: build-web build-go

# 仅构建前端
build-web:
	@echo "🌐 构建前端..."
	cd web && bun install --frozen-lockfile && bun run build
	@echo "✅ 前端构建完成: web/dist/"

# 仅构建后端（需要先构建前端，否则 go:embed 会失败）
build-go:
	@echo "🔨 编译 netsgo..."
	go build -o $(BIN_DIR)/netsgo ./cmd/netsgo/
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

DEV_PORT ?= 8080
DEV_KEY  ?= dev-key

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

lint:
	cd web && bun run lint
