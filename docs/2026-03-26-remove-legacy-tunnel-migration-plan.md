# Remove Legacy Tunnel Migration Compatibility

## 背景

当前仓库仍然保留一套“旧隧道记录自动迁移到稳定 `client_id`”的兼容逻辑，核心表现为：

- `StoredTunnel.Binding` 同时支持
  - `client_id`
  - `legacy_hostname`
- `normalize()` 会把旧数据默认回退为 `legacy_hostname`
- client 控制通道建立时会调用 `migrateLegacyTunnels(...)`
- store 仍提供 `MigrateLegacyTunnels(...)`、`GetLegacyTunnelsByHostname(...)`
- 还存在围绕 legacy hostname 迁移的专门测试

这套逻辑的前提是：**系统已经存在历史用户数据，需要在升级后自动接续旧 tunnel 记录。**

但按当前项目共识：

- 产品**尚未正式发布**
- 不需要为历史版本、本地旧数据、旧绑定模型做兼容
- 若设计已经明确，应直接切到最终模型，而不是长期保留迁移路径

因此，这套 legacy tunnel migration 兼容应视为**下一步待删除的旧设计残留**。

## 目标

把 tunnel 持久化模型彻底收敛为：

- tunnel 只按稳定 `client_id` 绑定
- store 不再接受或生成 `legacy_hostname` 绑定
- server 不再在 client 登录时执行“按 hostname 自动迁移旧 tunnel”
- 测试中不再保留针对 legacy hostname 迁移的行为断言

## 非目标

这次不顺手处理下面这些内容：

- 不顺手改 tunnel protocol cleanup 已完成的握手逻辑
- 不顺手改 tunnel desired/runtime state 模型
- 不顺手改 UI 上的 OS/platform 展示
- 不顺手做更大范围的 store 重构
- 不顺手做数据迁移工具

一句话：**本次只删除“未发布前不该保留的 legacy tunnel migration 兼容”。**

## 当前代码触点

### 1. store 模型仍保留 legacy 绑定

文件：`internal/server/store.go`

当前保留的 legacy 相关内容包括：

- `TunnelBindingLegacyHostname`
- `StoredTunnel.Binding`
- `StoredTunnel.Hostname`
- `normalize()` 中对旧数据默认回退到 `legacy_hostname`
- `matchesLegacyHostname(...)`
- `MigrateLegacyTunnels(...)`
- `GetLegacyTunnelsByHostname(...)`

这说明 store 仍然承认“按 hostname 绑定 tunnel”是合法模型。

### 2. server 登录时仍会做自动迁移

文件：

- `internal/server/server.go`
- `internal/server/tunnel_manager.go`

当前 client 连上控制通道后，server 仍会调用：

- `migrateLegacyTunnels(...)`

也就是说，系统运行时仍把“旧 tunnel 记录搬迁”视为正式业务路径的一部分。

### 3. 测试仍在保护这套旧兼容能力

文件：

- `internal/server/server_test.go`
- `internal/server/store_test.go`

当前存在的典型测试包括：

- `TestHandleControlWS_MigratesLegacyTunnelsToStableClientID`
- `TestHandleControlWS_SkipsLegacyMigrationForAmbiguousHostname`
- 以及一批 `seedLegacyTunnels(...)` 辅助路径

这会持续给未来维护者传递一个信号：

> legacy hostname 迁移仍是当前系统必须支持的正式行为

但这与“未发布、无需兼容”的项目原则矛盾。

## 目标完成后的模型

完成后，系统应满足：

1. `StoredTunnel` 只接受稳定 `client_id` 绑定
2. `TunnelBindingLegacyHostname` 被删除
3. `normalize()` 不再把旧数据自动降级为 `legacy_hostname`
4. 若读取到旧式 tunnel 绑定数据，应直接报错或按空数据处理，而不是自动迁移
5. server 控制通道建立时不再触发 legacy tunnel migration
6. 相关测试和测试辅助函数一并删除

## 实施建议

建议按 **三步** 做，而不是边改边猜：

### Step 1：先删运行时迁移入口

目标：

- 删除 `migrateLegacyTunnels(...)`
- 删除 server 登录路径对它的调用

涉及文件：

- `internal/server/server.go`
- `internal/server/tunnel_manager.go`

理由：

- 先把运行时迁移入口关掉，避免 store 还没清时继续偷偷使用旧模型

### Step 2：再删 store 的 legacy 绑定模型

目标：

- 删除 `TunnelBindingLegacyHostname`
- 删除 `matchesLegacyHostname(...)`
- 删除 `MigrateLegacyTunnels(...)`
- 删除 `GetLegacyTunnelsByHostname(...)`
- 收紧 `StoredTunnel.normalize()`，只接受稳定 `client_id` 绑定

涉及文件：

- `internal/server/store.go`

建议策略：

- 直接按“旧数据无兼容”处理
- 不引入迁移器
- 不做自动修复

### Step 3：最后清测试和文档

目标：

- 删除 legacy migration 专项测试
- 删除 `seedLegacyTunnels(...)`
- 清理和 legacy hostname migration 相关的测试描述

涉及文件：

- `internal/server/server_test.go`
- `internal/server/store_test.go`
- 如有必要，再补一条新的 store/model 约束测试

## 验证要求

至少执行：

```bash
go test ./internal/server -count=1
go test ./... 
```

并补一轮搜索确认不再残留这批符号：

```bash
rg "TunnelBindingLegacyHostname|MigrateLegacyTunnels|GetLegacyTunnelsByHostname|migrateLegacyTunnels|matchesLegacyHostname|seedLegacyTunnels"
```

预期：

- 代码路径无命中
- 若文档保留历史描述，需明确标注为 archived / historical context

## 风险

### 风险 1：本地旧开发数据无法继续使用

这是**预期结果**，不是缺陷。

既然项目未正式发布，就不应为旧格式继续背兼容债。

### 风险 2：可能误删仍被其他路径使用的 Hostname 展示字段

要区分：

- `Hostname` 作为展示信息是否仍需要
- `legacy_hostname` 作为 tunnel 绑定模型是否仍需要

本次目标是删后者，不是盲删所有 hostname 字段。

### 风险 3：测试可能在多个地方隐式依赖 seedLegacyTunnels

所以删除前要先搜索并收口所有引用，不要只删单个测试函数。

## 建议结论

这项兼容删除应作为**下一步独立任务**推进。

原因很简单：

- 它不是 protocol cleanup 的尾巴
- 但它确实属于“未发布前不该继续保留的历史兼容逻辑”
- 而且边界清晰、收益明确、适合单独收口

完成后，tunnel 持久化与运行时模型会更一致，也更符合当前项目“正确优先、不做历史兼容”的原则。
