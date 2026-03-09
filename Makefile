.PHONY: build-server build-client build-all clean

# 编译输出目录
BIN_DIR=bin

# 编译服务端
build-server:
	@echo "🔨 编译服务端..."
	go build -o $(BIN_DIR)/server.exe ./cmd/server/
	@echo "✅ 服务端编译完成: $(BIN_DIR)/server.exe"

# 编译客户端
build-client:
	@echo "🔨 编译客户端..."
	go build -o $(BIN_DIR)/client.exe ./cmd/client/
	@echo "✅ 客户端编译完成: $(BIN_DIR)/client.exe"

# 编译全部
build-all: build-server build-client
	@echo "✅ 全部编译完成"

# 清理
clean:
	@echo "🧹 清理构建产物..."
	rm -rf $(BIN_DIR)
	@echo "✅ 清理完成"

# 开发模式 - 启动服务端
dev-server:
	go run ./cmd/server/ -port 8080

# 开发模式 - 启动客户端
dev-client:
	go run ./cmd/client/ -server ws://localhost:8080
