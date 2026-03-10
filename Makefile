.PHONY: build clean docs

# 编译输出目录
BIN_DIR=bin

# 编译 netsgo 统一二进制
build:
	@echo "🔨 编译 netsgo..."
	go build -o $(BIN_DIR)/netsgo ./cmd/netsgo/
	@echo "✅ 编译完成: $(BIN_DIR)/netsgo"

# 清理
clean:
	@echo "🧹 清理构建产物..."
	rm -rf $(BIN_DIR)
	@echo "✅ 清理完成"

# 生成 CLI 文档
docs:
	@echo "📝 生成命令行文档..."
	go run ./cmd/netsgo/ docs --output ./docs/cli
	@echo "✅ 文档已生成到 docs/cli/"

# 开发模式 - 启动服务端
dev-server:
	go run ./cmd/netsgo/ server --port 8080

# 开发模式 - 启动客户端
dev-client:
	go run ./cmd/netsgo/ client --server ws://localhost:8080

# 开发模式 - 运行压测
dev-bench:
	go run ./cmd/netsgo/ benchmark
