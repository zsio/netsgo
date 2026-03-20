# 修复计划：UDP 会话清理 sessionCount 双重递减

> 创建时间：2026-03-20

## 问题概述

`UDPProxyState.sessionCount` 在会话关闭时可能被递减两次，导致计数变为负数，使 `MaxUDPSessions` 上限检查失效。

根因是会话清理责任分散在三个位置，它们之间没有互斥保护：

| 位置 | 调用路径 | 做了什么 |
|------|---------|---------|
| `udpReadLoop` L179-181 | WriteUDPFrame 失败 | `sess.Close()` + `sessions.Delete` + `sessionCount.Add(-1)` |
| `udpSessionReverse` defer L188-192 | ReadUDPFrame 失败或 done 信号 | `sess.Close()` + `sessions.Delete` + `sessionCount.Add(-1)` |
| `UDPProxyState.Close()` Range L31-37 | 代理关闭 | `sess.Close()` + `sessions.Delete` + `sessionCount.Add(-1)` |

任意两条路径因同一个会话触发时，`sessionCount` 就会多减一次。

### 复现路径

1. `udpReadLoop` 写入 stream 失败 → 执行 `sess.Close()` + `sessions.Delete` + `sessionCount.Add(-1)`
2. `sess.Close()` 关闭了 `sess.stream` → `udpSessionReverse` 的 `ReadUDPFrame` 返回 error → defer 执行 `sessions.Delete` + `sessionCount.Add(-1)`
3. `sessionCount` 被递减了两次

## 修复方案

### 核心原则

将会话的 map 移除 + 计数递减的责任统一归于一处，采用 `sync.Map.LoadAndDelete` 的原子性保证同一个会话只会被计数递减一次。

### 引入辅助方法 `removeSession`

在 `UDPProxyState` 上新增一个方法，作为会话清理的唯一入口：

```go
// removeSession 原子地从 sessions map 中移除会话并递减计数。
// 返回 true 表示本次调用实际完成了移除（调用方是第一个清理者）。
func (s *UDPProxyState) removeSession(key string) bool {
    if _, loaded := s.sessions.LoadAndDelete(key); loaded {
        s.sessionCount.Add(-1)
        return true
    }
    return false
}
```

`sync.Map.LoadAndDelete` 是原子操作，保证多个 goroutine 竞争时只有一个返回 `loaded=true`。

### 改动清单

所有改动均在 `internal/server/udp_proxy.go` 一个文件内。

#### 改动 1：新增 `removeSession` 方法

在 `UDPProxyState` 类型下新增上述方法。

#### 改动 2：`UDPProxyState.Close()` — 使用 removeSession

```diff
 s.sessions.Range(func(key, value any) bool {
     sess := value.(*UDPSession)
     sess.Close()
-    s.sessions.Delete(key)
-    s.sessionCount.Add(-1)
+    s.removeSession(key.(string))
     return true
 })
```

#### 改动 3：`udpReadLoop` 中 WriteUDPFrame 失败 — 使用 removeSession

```diff
 if err := mux.WriteUDPFrame(sess.stream, buf[:n]); err != nil {
     log.Printf(...)
     sess.Close()
-    state.sessions.Delete(key)
-    state.sessionCount.Add(-1)
+    state.removeSession(key)
 }
```

#### 改动 4：`udpSessionReverse` defer — 使用 removeSession

```diff
 defer func() {
     sess.Close()
-    state.sessions.Delete(sess.srcAddr.String())
-    state.sessionCount.Add(-1)
+    state.removeSession(sess.srcAddr.String())
 }()
```

### 附带改进：`WriteUDPFrame` 合并为单次 Write（可选）

文件：`pkg/mux/udp_frame.go`

将两次 `w.Write()` 合并为一次，消除未来并发写入时帧损坏的隐患：

```diff
 func WriteUDPFrame(w io.Writer, payload []byte) error {
-    var lenBuf [2]byte
-    binary.BigEndian.PutUint16(lenBuf[:], uint16(len(payload)))
-    if _, err := w.Write(lenBuf[:]); err != nil {
-        return err
-    }
-    _, err := w.Write(payload)
-    return err
+    buf := make([]byte, 2+len(payload))
+    binary.BigEndian.PutUint16(buf[:2], uint16(len(payload)))
+    copy(buf[2:], payload)
+    _, err := w.Write(buf)
+    return err
 }
```

当前 `udpReadLoop` 是单 goroutine，不存在并发写。但合并后更健壮，且对性能影响可忽略。

## 涉及文件

| 文件 | 改动内容 |
|------|---------|
| `internal/server/udp_proxy.go` | 新增 `removeSession`，修改 `Close()`、`udpReadLoop`、`udpSessionReverse` |
| `pkg/mux/udp_frame.go` | （可选）`WriteUDPFrame` 合并为单次 Write |

## 验证计划

### 现有测试回归

```bash
go test ./internal/server/ -run "UDP" -count=1 -v
go test ./pkg/mux/ -run "UDP" -count=1 -v
```

所有现有 16 个 UDP 相关测试应继续通过。

### 新增测试 1：`TestRemoveSession_Idempotent`

验证 `removeSession` 重复调用只递减一次：

- 创建 `UDPProxyState`
- 手动存入一个会话，`sessionCount` 设为 1
- 对同一个 key 调用 `removeSession` 两次
- 断言 `sessionCount.Load() == 0`（而不是 -1）

### 新增测试 2：`TestUDPProxy_SessionCount_AfterCleanup`

验证完整场景下会话清理后 `sessionCount` 不会变为负数：

- 创建 UDP 代理，模拟多个 srcAddr 建立会话
- 关闭代理（触发 `UDPProxyState.Close()`）
- 等待 `udpSessionReverse` goroutine 退出
- 断言 `sessionCount.Load() >= 0`

## 风险评估

| 风险 | 等级 | 说明 |
|------|------|------|
| 改动波及面 | 低 | 仅 `udp_proxy.go` 一个文件，逻辑局部 |
| 行为变化 | 无 | 对外行为不变，只是内部清理路径收敛 |
| 兼容性 | 无影响 | 不涉及协议、API、前端 |
| WriteUDPFrame 合并 | 极低 | 接口签名不变，只是内部实现方式变化 |
