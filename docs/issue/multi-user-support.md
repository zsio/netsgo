# 多用户设计契约与原执行规划

## 文档状态

- [KNOWN] 状态：`Implemented`，已移出待开发索引；发布时仍需按既有 CI / E2E 门禁回归。当前实现以代码和测试为准，本节以下的 `[FRAME]` 条目保留为设计契约，原分阶段计划用于追溯设计与验收范围。
- [FRAME] 优先级：High。
- [FRAME] 本文保留多用户功能的设计契约；旧方案中的有效期、配额、公开注册和通知设计全部由本文取代。
- [KNOWN] 当前用户运行态收敛默认超时为 10 秒，测试可覆盖该内部时长，生产不增加配置项。

## 当前实现入口

- [KNOWN] `012_multi_user_ownership.sql` 已建立统一用户与 Session 表，并迁移资源所有权；`storage_schema_test.go` 覆盖旧数据回填、严格迁移拒绝旧版本、校验失败回滚和孤儿安全凭据拒绝迁移。
- [KNOWN] `user_api.go`、`user_store.go`、`user_policy.go` 和 `user_lifecycle_gate.go` 已实现管理 API、最后一个正常管理员保护、禁用/恢复、物理删除及运行态收敛。
- [KNOWN] `auth_middleware.go`、`resource_scope.go`、`events.go`、流量与活动存储按当前用户或管理员显式目标用户范围授权；相关 API 和生命周期竞态有 Go 测试。
- [KNOWN] 管理员用户列表、用户资源视图和作用域 Query Key 已实现；`web/e2e/multi_user_lifecycle.pw.ts` 与 `multi_user_live_isolation.pw.ts` 覆盖浏览器生命周期及两个用户的实时隔离。
- [KNOWN] 最近一次文档核实的实际执行范围见 [Issue 索引](../issues.md#本次验证范围)；存在 E2E 测试不等于本次已重跑全部部署矩阵。

## 先固定一个不能含糊的状态语义

[INFERRED] 禁用和删除不能共享一套数据语义：禁用必须可恢复，删除必须彻底且不可恢复。

[FRAME] 本轮采用下面的严格边界：

- [FRAME] 禁用用户时保留密码、API Key、Client Token、Client 注册、隧道配置、历史流量和活动日志。
- [FRAME] 禁用用户时不修改 API Key 的 `is_active`、隧道配置、隧道 `desired_state` 或资源所有者。
- [FRAME] 已经停止承载流量的隧道必须把事实运行态投影为 `offline`；Client 必须投影为离线。
- [FRAME] 隧道通过派生问题 `owner_disabled` 解释“资产本身配置有效，但所有者当前不允许运行”。
- [FRAME] `error` 不写入用户状态原因，避免把所有者策略伪装成资产配置故障。
- [FRAME] 恢复用户后，仍为 `desired_state = running` 的隧道沿用现有 reconcile 路径恢复；不另建恢复运行时。
- [FRAME] 只有已经禁用的用户才允许删除；删除会物理删除用户行以及与该用户有关的全部凭据、Session、Client、Token、隧道、流量和活动数据。
- [FRAME] 删除成功后不存在 `owner_deleted` 运行态、已删除用户列表、恢复入口或历史详情页。

## 已确认的产品模型

### 用户类型与管理员标识

- [FRAME] 系统只有管理员和普通用户两种身份，统一存放在 `users` 表中。
- [FRAME] `users.is_admin = 1` 表示管理员，`users.is_admin = 0` 表示普通用户；身份差异只由这个字段表达。
- [FRAME] 当前初始化产生的第一个用户，以及迁移前已经存在的全部用户，迁移后都设置为管理员。
- [FRAME] 管理员也出现在用户列表中，也拥有自己的 API Key、Client、隧道、流量和活动范围。
- [FRAME] 管理员从 Dashboard 用户列表每行的三点操作菜单中，把任意现有用户设为管理员或移除其管理员身份。
- [FRAME] 管理员身份修改使用显式目标状态，不使用 toggle；重复请求必须得到同一结果。
- [FRAME] 系统允许多个管理员，但任意事务提交后必须至少存在一个 `is_admin = 1 AND status = 'active'` 的用户。
- [FRAME] 不存在永久受保护的特定管理员；保护对象是“最后一个正常管理员”这一系统不变量。
- [FRAME] 管理员不能禁用或删除自己；管理员可以移除自己的管理员身份，但必须还有另一个正常管理员。
- [FRAME] 设置或移除管理员身份后撤销目标用户全部 Web Session、断开其 SSE，并要求重新登录。
- [FRAME] 移除管理员身份时同时删除其 TOTP Secret、Recovery Code、Passkey 和未完成认证 Challenge；再次设为管理员后重新配置管理员安全能力。
- [FRAME] 管理员身份变化不转移、不删除目标用户的 API Key、Client、Client Token、隧道或流量数据。

### 用户状态

- [FRAME] 所有用户的持久化 `status` 第一版只有 `active` 和 `disabled`。
- [FRAME] 管理员和普通用户使用同一套状态语义；管理员权限不能绕过自身的禁用状态。
- [FRAME] 用户是否允许运行资源由一个集中判定给出：

```text
user_operational = user row exists AND status = 'active'
```

- [FRAME] 对未知的未来状态必须 fail closed；只有明确的 `active` 允许工作，不能用 `status != 'disabled'` 判断。
- [FRAME] 用户只保存 `created_at` 和 `updated_at`，不保存 `deleted_at` 或墓碑记录。
- [FRAME] 删除用户是物理删除且不可恢复；删除后用户名立即可以重新使用，新建同名用户是全新的身份和用户 ID。

### 资源所有权与管理员代操作

- [FRAME] API Key、Client、隧道、历史流量归属和用户范围活动都必须有用户所有权。
- [FRAME] 管理员管理某个用户的资源时，资源所有者仍是目标用户，活动 Actor 和 `created_by_user_id` 记录真实管理员。
- [FRAME] 第一版不提供任何资源转移接口，也不允许通过更新请求改变 `owner_user_id`。
- [FRAME] `client_to_client` 的两端 Client 必须属于同一个用户；管理员也不能绕过该约束创建跨用户隧道。
- [FRAME] Server 配置、允许端口、全局认证限流和管理员安全设置仍是 Server 全局资源，不强行挂到某个普通用户下面。

### 禁用、恢复与删除

- [FRAME] 禁用用户时，撤销其 Web Session、拒绝新的 Client 认证、关闭已在线的控制通道和数据通道、关闭相关 P2P 会话，并卸载其全部隧道运行态。
- [FRAME] 禁用请求只有在该用户已经不再承载流量后才返回成功；清理失败时用户保持 `disabled`，接口返回失败并允许管理员重试收敛。
- [FRAME] 恢复用户只把 `status` 改回 `active`；Web 用户需要重新登录，Client 使用保留的 Token 自动重连。
- [FRAME] Client 重连并且数据通道就绪后，现有 reconcile 只恢复禁用前仍有 `desired_state = running` 的隧道。
- [FRAME] 删除用户前必须再次确认其状态为 `disabled`；未禁用用户返回稳定的 `user_must_be_disabled` 冲突错误。
- [FRAME] 删除用户会物理删除密码、管理员安全凭据、Web Session、API Key、Key Permission、Client Token、Client、Client 状态、磁盘信息、隧道、资源锁、流量桶以及与该用户有关的全部活动事件，最后删除 `users` 行。
- [FRAME] 删除用户不保留审计墓碑、用户名快照或“某用户已删除”的持久化活动事件。

### 当前范围明确不做的内容

- [FRAME] 不存在服务到期时间、续期、宽限期或到期扫描器。
- [FRAME] 本轮不实现用户配额；如果仍需要隧道数量、带宽或资源配额，另开设计，不把它混入身份与所有权迁移。
- [FRAME] 不实现公开注册、注册审批或邀请。
- [FRAME] 不实现通知、收件箱、已读/未读、消息投递或推送。
- [FRAME] 不实现资源转移、组织、团队、共享资源或跨用户隧道。
- [FRAME] 不把 NetsGo 扩展为多 Server 实例或分布式控制面。
- [FRAME] 不实现目标服务健康检查。
- [FRAME] 不为 Web 与 Server API 保留旧接口 fallback；二者同二进制、同版本升级。
- [FRAME] 多用户迁移严格不可降级；不设计 Server 回滚、降级 migration、双写补偿或回滚兼容路径。

## 实施前基线与改造约束（历史背景）

本节中的“当前”指多用户改造前的基线。`admin_users` / `admin_sessions`、资源缺少用户所有权和全局 Query Key 等描述记录的是当时需要替换的状态；现状见上方“当前实现入口”。

- [KNOWN] `internal/server/migrations/001_server_runtime_schema.sql` 中的登录身份当前存放在 `admin_users`，Web Session 当前存放在 `admin_sessions`。
- [KNOWN] `internal/server/auth_middleware.go` 的 JWT 当前只携带 `sid`，`RequireAuth` 只从 `admin_sessions` 解析主体。
- [KNOWN] `internal/server/server_http.go` 中 Client、隧道和 `/api/admin/*` 路由当前大多只经过同一个 `RequireAuth`。
- [KNOWN] `registered_clients`、`api_keys`、`tunnels` 和 `traffic_buckets` 当前没有用户所有权字段。
- [KNOWN] `tunnels.created_by_user_id` 已存在，但它表达执行者而不是资源所有者。
- [KNOWN] 当前 `owner_client_id` 表达隧道拓扑中的业务所有 Client，不表达登录用户所有权。
- [KNOWN] `pkg/protocol/message.go` 的 `AuthRequest` 只发送 Key 或 Token、`install_id` 和 Client 信息，没有用户字段。
- [KNOWN] `internal/server/data.go` 的数据通道握手使用 `client_id + data_token` 并绑定当前逻辑 Client 会话。
- [KNOWN] `internal/server/session.go` 已有逻辑会话失效路径，可关闭控制连接、数据 `yamux`、P2P 和关联运行态。
- [KNOWN] `internal/server/unified_tunnel_reconcile.go` 统一处理启动、重试和 Client 恢复后的隧道 reconcile。
- [KNOWN] `internal/server/control_loop.go` 仍存在 Client 控制通道发起的 legacy `MsgTypeProxyCreate` 写路径。
- [KNOWN] `activity_events` 已保存严重程度、类别、动作、来源、Actor 和结构化 Payload，但当前没有用户可见范围字段。
- [KNOWN] `web/src/lib/router.ts` 使用 TanStack Router Hash History。
- [KNOWN] `web/src/routes/dashboard.tsx` 当前在 Dashboard 外壳中全局加载 Client；这会阻碍用户列表作为管理员首页，也会增加跨用户缓存污染风险。
- [KNOWN] `web/src/components/custom/dashboard/OverviewPage.tsx` 当前已经包含“网络拓扑”“客户端”“隧道”三个模块，可改造成显式用户作用域组件。
- [KNOWN] `web/src/hooks/use-clients.ts` 和事件流缓存当前使用全局 `['clients']` Query Key，必须加入用户作用域。

## 数据模型

### 统一用户目录、凭据与管理员安全

[FRAME] `users` 同时是身份目录、登录凭据和资源所有权的权威表；管理员和普通用户不再分属不同身份表。

[FRAME] 建议 migration 名称为 `012_multi_user_ownership.sql`，核心结构如下：

```sql
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    is_admin INTEGER NOT NULL DEFAULT 0
        CHECK (is_admin IN (0, 1)),
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (length(status) > 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_login TEXT,
    totp_enabled INTEGER NOT NULL DEFAULT 0
        CHECK (totp_enabled IN (0, 1)),
    totp_secret TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_users_page
    ON users(created_at DESC, id DESC);
CREATE INDEX idx_users_status_page
    ON users(status, created_at DESC, id DESC);
```

- [INFERRED] `status` 不使用只允许两个值的数据库 CHECK，因为已经确认未来可能增加状态；Go 服务层只允许当前已实现的转换。
- [FRAME] `is_admin` 是稳定的二值字段，可以在数据库层约束；只有管理员专用的显式目标状态接口可以修改它。
- [FRAME] `updated_at` 在用户名、状态、密码、管理员标识或管理员安全凭据发生管理变更时更新。
- [FRAME] `totp_enabled` 和 `totp_secret` 只对管理员启用；普通用户保留默认关闭值。
- [FRAME] Passkey、Recovery Code 和认证 Challenge 可以继续使用管理员专用安全表，但这些表只通过 `user_id -> users.id` 关联，不再承载独立的管理员身份。
- [KNOWN] 当前 `admin_totp_recovery_codes`、`admin_passkeys` 和 `admin_auth_challenges` 的 `user_id` 没有外键，SQLite 不能通过给现有列追加约束完成修复。
- [FRAME] `012_multi_user_ownership` 必须逐表重建这三个安全表：创建带 `user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE` 的新表，复制并核对全部行，删除旧表后改回原表名，再重建原有 UNIQUE 约束和索引。
- [KNOWN] 当前 migration 执行器只在事务中执行 `tx.Exec(migration.Up)` 后写 ledger；把 `PRAGMA foreign_key_check` 裸写进 Up SQL 只会返回结果，不会因存在违规行自动回滚。
- [FRAME] `storage.Migration` 增加可选的事务内校验 Hook，执行顺序固定为 `Up SQL -> ValidateTx -> 写 schema_migrations -> commit`；`012_multi_user_ownership` 在 `serverMigrations` 加载后绑定专用校验器，其他 migration 行为不变。
- [FRAME] `012` 在删除源表前把源行数和复制后行数写入事务内临时校验表；专用校验器在同一 `sql.Tx` 中核对这些计数、`sqlite_schema` 中的 UNIQUE/索引、`PRAGMA foreign_key_list`、`PRAGMA foreign_key_check` 和旧表已不存在，清理临时表后才允许写 ledger。
- [FRAME] 行数、唯一约束、索引、目标外键、全库外键或旧表删除检查任一不一致时，校验器返回错误并回滚整个严格 migration；不能把测试期的迁移后检查当成运行时原子校验的替代品。
- [FRAME] `012` 对 `admin_totp_recovery_codes`、`admin_passkeys` 和 `admin_auth_challenges` 分别记录显式孤儿计数。任一表存在 `user_id` 无法匹配 `admin_users.id` 的行时，Server 必须拒绝启动，完整回滚 `012`，且不能写入 strict migration ledger；修复后重启会安全重试同一 migration。
- [FRAME] 启动错误必须带数据库路径、具体表名、回滚/未记账状态和先备份再检查的操作指引。Server 不自动删除或重新归属孤儿凭据，因为仅凭缺失外键不能判断它是应删除的陈旧数据还是需要恢复的用户数据。运维人员应先复制数据库，再用下列只读查询确认范围；只有在确认数据来源后才做定向修复：

```sql
SELECT 'admin_totp_recovery_codes' AS table_name, c.user_id, COUNT(*) AS orphan_rows
FROM admin_totp_recovery_codes c LEFT JOIN admin_users u ON u.id = c.user_id
WHERE u.id IS NULL GROUP BY c.user_id
UNION ALL
SELECT 'admin_passkeys', p.user_id, COUNT(*)
FROM admin_passkeys p LEFT JOIN admin_users u ON u.id = p.user_id
WHERE u.id IS NULL GROUP BY p.user_id
UNION ALL
SELECT 'admin_auth_challenges', c.user_id, COUNT(*)
FROM admin_auth_challenges c LEFT JOIN admin_users u ON u.id = c.user_id
WHERE u.id IS NULL GROUP BY c.user_id;
```

### 统一 Web Session

[FRAME] 管理员和普通用户统一使用 `user_sessions`，不再分别使用 `admin_sessions` 和普通用户 Session 表。

```sql
CREATE TABLE user_sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_user_sessions_user
    ON user_sessions(user_id);
CREATE INDEX idx_user_sessions_expires
    ON user_sessions(expires_at);
```

- [FRAME] 所有 Web Session 都通过 `user_id -> users.id` 解析当前用户；Session 表不保存可作为权限依据的用户名或管理员标识副本。
- [FRAME] Session ID 不需要按管理员和普通用户划分命名空间，唯一性由统一的 `user_sessions.id` 保证。
- [FRAME] 所有用户第一版只支持用户名和密码登录；只有 `is_admin = 1` 的用户进入 MFA、Recovery Code 和 Passkey 流程。
- [FRAME] 密码重置必须撤销目标用户的全部 Web Session，但不撤销 Client Token。

### 资源所有权字段

[FRAME] 第一版在资源根和独立历史数据上持久化用户 ID，并让用户物理删除级联清除这些记录：

```sql
ALTER TABLE api_keys
    ADD COLUMN owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE registered_clients
    ADD COLUMN owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE tunnels
    ADD COLUMN owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE traffic_buckets
    ADD COLUMN owner_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE activity_events
    ADD COLUMN scope_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE activity_events
    ADD COLUMN subject_user_id TEXT REFERENCES users(id) ON DELETE CASCADE;
```

[FRAME] 建议索引：

```sql
CREATE INDEX idx_api_keys_owner
    ON api_keys(owner_user_id, created_at);
CREATE INDEX idx_registered_clients_owner
    ON registered_clients(owner_user_id, created_at);
CREATE INDEX idx_tunnels_user_topology
    ON tunnels(owner_user_id, topology, created_at);
CREATE INDEX idx_traffic_user_query
    ON traffic_buckets(owner_user_id, resolution, bucket_start);
CREATE INDEX idx_activity_events_user
    ON activity_events(scope_user_id, occurred_at_ns DESC, id DESC);
CREATE INDEX idx_activity_events_subject_user
    ON activity_events(subject_user_id, occurred_at_ns DESC, id DESC);
```

- [FRAME] 这些新增列在 SQLite 物理结构中先保持 nullable，以允许现有数据先回填所有者；迁移完成后，当前 Server 的所有写入口必须把非空所有者作为应用层不变量。
- [FRAME] `owner_user_id` 是授权、隔离和资源列表的唯一用户所有权来源。
- [FRAME] `created_by_user_id` 只记录创建执行者；管理员代用户创建时，两者故意不同。删除该执行者但资源属于其他用户时必须把该字段清空，不能因此删除其他用户的资源。
- [FRAME] `scope_user_id` 表示哪一个用户范围可读取该活动；`NULL` 只用于真正全局、管理员安全或无法可靠归属的事件。
- [FRAME] `subject_user_id` 表示活动明确涉及的用户主体，只用于删除关联和审计完整性，不改变 `scope_user_id` 的可见性语义；已解析出用户的登录、安全和管理事件不得只把用户 ID 写进 `dedupe_key` 或 Payload。
- [FRAME] `activity_events.actor_id` 当前不是外键且不同 Actor 类型共享字符串命名空间；删除用户事务必须显式删除 `scope_user_id = target`、`subject_user_id = target`、`actor_type IN ('admin', 'user') AND actor_id = target`、`actor_type = 'client'` 且该 Client 属于 target，或关联 Client/Tunnel 属于 target 的全部事件。不能仅按裸 `actor_id` 删除，否则与用户 ID 字符串碰撞的无关 Client Actor 会被误删；`012` 的 Actor-to-Subject 回填遵循同一类型约束。
- [FRAME] 管理员安全表都改为 `user_id -> users.id ON DELETE CASCADE`；不再留下无用户主体的 Passkey、Recovery Code 或 Challenge。

### 不重复持久化所有权的表

| 表 | 所有权来源 |
|---|---|
| `client_stats` | [FRAME] 通过 `client_id -> registered_clients.owner_user_id` 推导。 |
| `client_disk_partitions` | [FRAME] 通过 `client_id -> registered_clients.owner_user_id` 推导。 |
| `client_tokens` | [FRAME] 正常记录通过 `client_id -> registered_clients.owner_user_id` 推导；用户删除同时按所属 Client 的 `client_id` 或非空 `install_id` 匹配 Token，以清理尚未写入稳定 Client ID 的历史/预注册记录，又避免空 install ID 扩大删除范围。`key_id` 只保留签发来源，不参与后续 Token 授权。 |
| `api_key_permissions` | [FRAME] 通过 `api_key_id -> api_keys.owner_user_id` 推导。 |
| `tunnel_resource_locks` | [FRAME] 通过 `tunnel_id -> tunnels.owner_user_id` 推导。 |
| `activity_event_clients` | [FRAME] 继承所属 `activity_events.scope_user_id`。 |
| `activity_event_tunnels` | [FRAME] 继承所属 `activity_events.scope_user_id`。 |

[INFERRED] 给这些派生表再写一份用户 ID 会产生必须长期维护的多份权威数据，没有收益。

[FRAME] API Key 与 Client Token 之间故意不建立删除级联：删除单个 API Key 只禁止该 Key 后续换取新 Token，已经签发的 Client Token 继续通过所属 Client 认证。

[FRAME] 用户硬删除不能只依赖外键：`client_tokens`、现有管理员安全表、`tunnel_resource_locks`、活动 Actor 和 `tunnels.created_by_user_id` 等现有结构需要显式删除或清空；用户级删除服务必须维护一份完整顺序并由测试核对零残留。

### 升级与现有数据归属

- [FRAME] 已初始化数据库迁移时，把 `admin_users` 中全部现有记录按原 ID 写入 `users`，统一设置 `is_admin = 1`、`status = 'active'`，并保留各自密码、MFA 状态和用户名；迁移允许产生多个管理员。
- [FRAME] 现有 `admin_sessions` 按原 Session ID 迁移到 `user_sessions`，使现有 JWT 仍能通过统一 Session 表解析；无法迁移的 Session 必须明确撤销。
- [FRAME] 用户、Session 和管理员安全凭据复制并验证完成后，必须在同一个严格迁移事务中先删除 `admin_sessions`、再删除 `admin_users`；迁移成功的 Schema 不得继续保留旧密码、MFA 或 Session 副本。
- [FRAME] 迁移前资源没有足够信息按多个管理员拆分归属，因此按 `(created_at ASC, id ASC)` 选择最早的现有管理员作为 `legacy_owner_user_id`，把全部现有 API Key、Client、隧道和历史流量回填到该用户；不能依赖无排序查询结果。
- [FRAME] 已有 Client、隧道和 P2P 活动可可靠关联资源时回填 `legacy_owner_user_id` 范围；全局管理员安全事件可以保留 `scope_user_id = NULL`。
- [FRAME] 现有 `session_environment_mismatch` 安全事件必须按当前固定 `dedupe_key` 格式解析：能唯一匹配现有 `users.id` 的回填 `subject_user_id`；主体已不存在、格式不符或无法唯一匹配的事件连同关联行直接删除，不能留下只有字符串关联的用户痕迹。
- [FRAME] 现有 `created_by_user_id` 为空时不伪造历史执行者；资源所有权回填与审计执行者是两个问题。
- [FRAME] fresh DB 在 migration 时允许 `users` 为空；初始化流程必须在同一个事务中写入第一个 `is_admin = 1 AND status = 'active'` 用户行。
- [FRAME] `ResetAdminUser` 作为离线恢复命令，按 `(created_at ASC, id ASC)` 选择现存最早的管理员，保留其用户 ID，只更新用户名和密码、恢复为 `active`、清空 MFA/Passkey/Challenge 并撤销其 Session；不能删除其他用户、其他管理员或任何资源后重新生成一个管理员。
- [FRAME] 管理员用户名修改、密码修改和管理员安全设置都直接更新 `users` 或以 `users.id` 关联的安全表。
- [KNOWN] 当前版本没有会写入管理资源的 legacy JSON 导入入口，`012_multi_user_ownership` 不新增这类入口；未来若新增导入，必须在导入事务中显式传入目标用户，不能把空 Owner 自动归给任意管理员。
- [FRAME] migration 完成后执行不变量验证：已初始化实例必须至少存在一个正常管理员，且所有资源根不得残留空所有者。
- [FRAME] `012_multi_user_ownership` 必须分类为 strict 并记录到 `schema_migrations`，不能进入 compatible ledger。
- [FRAME] 已应用该 migration 的数据库不允许旧 Server 启动；旧二进制必须因未知 strict migration 在开始监听端口前失败。

### Migration 全局执行顺序

[KNOWN] 当前 `openServerDB` 先把全部 strict migration 交给 `storage.Open`，完成后才执行 compatible migration；因此 fresh DB 若直接新增 strict `012`，会在 compatible `011_activity_events` 创建 `activity_events` 前执行 `012`，规划中的 Activity 列迁移必然失败。

[FRAME] 新增 `012` 前必须重构 Server migration 编排：数据库配置完成后，先用全部已知 strict 名称校验 `schema_migrations` 中不存在未知记录，再按文件版本 `001 -> ... -> 012` 的全局顺序逐个执行；每个 migration 仍按分类写入 `schema_migrations` 或 `schema_compatible_migrations`。

[FRAME] 当前顺序必须实际表现为 strict `001-009`、compatible `010-011`、strict `012`；不能再把两个分组分别整批执行。每个 migration 仍使用独立事务，并在同一事务中完成 Up、可选 `ValidateTx`、对应 ledger 写入和 commit。

[FRAME] compatible ledger 继续容忍旧二进制不认识的 compatible 记录，strict ledger 在执行任何待应用 migration 前一次性按完整 strict 集合拒绝未知记录；这样已升级数据库中的 `012` 仍会让上一版 Server 在监听前失败。

[FRAME] fresh DB、只有 strict `001-009` 的旧 DB、已经应用 compatible `010-011` 的当前 DB，以及已经应用 `012` 的新 DB 都必须走同一个全局有序执行器，不能用环境分支改变 migration 顺序。

## 统一请求主体与登录

### Principal

[FRAME] HTTP Context 从管理员专用 `SessionInfo` 收敛到统一请求主体：

```go
type RequestPrincipal struct {
    UserID    string
    Username  string
    SessionID string
    IsAdmin   bool
}
```

- [FRAME] JWT 只作为 Session 句柄；所有请求统一查 `user_sessions JOIN users`，从 `users.is_admin` 得到管理员权限，并验证 Session 和 `status = 'active'`。
- [FRAME] 不把管理员标识或用户状态长期固化在 JWT Claim 中，避免用户状态变更后继续使用旧权限快照。

### 登录接口

- [FRAME] 保留统一的 `POST /api/auth/login` 用户名密码入口；Server 从 `users` 查询用户名和密码 Hash，再根据 `is_admin` 决定是否进入管理员 MFA 分支。
- [FRAME] 登录错误继续使用不区分“用户名不存在、密码错误、用户已禁用”的外部文案，内部错误码和活动日志记录真实原因。
- [FRAME] 管理员 MFA 流程保持现有接口；`is_admin = 0` 的普通用户不会进入 MFA 分支。
- [FRAME] 新增 `GET /api/auth/me`，前端启动时必须用服务端 Session 重新确认主体，不能只相信持久化 Zustand 状态。
- [FRAME] `POST /api/auth/logout` 根据 Principal 从 `user_sessions` 删除正确的 Session 记录。
- [FRAME] 登录限流覆盖两类密码登录，不为普通用户另开可绕过的无限流入口。

### 中间件

[FRAME] 中间件职责固定为：

```text
RequirePrincipal        验证任一 Web 主体并注入 RequestPrincipal
RequireAdmin             只允许管理员
RequireOperationalUser  用户行必须存在且 status = active
```

- [FRAME] `/api/admin/*` 全部改为 `RequireAdmin`，权限来源是 `RequestPrincipal.IsAdmin`。
- [FRAME] 普通资源 Handler 不能只用 `RequirePrincipal`；它还必须把资源查询限制在 Principal 所有权内。
- [FRAME] 前端路由守卫只负责体验，后端中间件和带作用域的存储方法才是权限边界。
- [FRAME] 实施期间不能先把现有 `RequireAuth` 全局替换为“任意用户可登录”，再逐个修资源接口；普通用户登录能力必须在全部用户作用域闭合后才启用。

## 授权与操作矩阵

| 操作 | 正常用户本人 | 禁用用户本人 | 管理员代操作 |
|---|---|---|---|
| 查看自己的资源 | [FRAME] 允许。 | [FRAME] Web Session 被撤销，用户本人不允许。 | [FRAME] 允许查看目标用户及其保留资源。 |
| 创建资源、签发或启用 Key | [FRAME] 允许。 | [FRAME] 拒绝。 | [FRAME] 仅当目标用户可运行时允许。 |
| 更新可能触发 reconcile 的配置 | [FRAME] 允许。 | [FRAME] 拒绝。 | [FRAME] 仅当目标用户可运行时允许。 |
| 启动、恢复、迁移隧道 | [FRAME] 允许。 | [FRAME] 拒绝。 | [FRAME] 不允许绕过目标用户状态。 |
| 停止或删除单个资产 | [FRAME] 允许。 | [FRAME] 用户本人无有效 Session。 | [FRAME] 目标用户禁用时仍允许清理。 |
| 修改纯展示元数据 | [FRAME] 允许。 | [FRAME] 用户本人无有效 Session。 | [FRAME] 允许，但不得触发运行态启动。 |
| 设置或移除管理员 | [FRAME] 不允许。 | [FRAME] 不允许。 | [FRAME] 允许，但必须满足最后一个正常管理员约束。 |
| 禁用、恢复、删除用户 | [FRAME] 不允许。 | [FRAME] 不允许。 | [FRAME] 允许；删除额外要求目标已禁用且不能操作自己。 |

- [FRAME] 普通用户请求其他用户的资源 ID 返回 `404`，避免通过 ID 枚举资源是否存在。
- [FRAME] 管理员代操作时必须显式选择目标用户；不能用空 `userID`、特殊 UUID 或布尔开关表达“全局权限”。
- [FRAME] 所有创建请求都由 Server 决定 `owner_user_id`，请求体不能覆盖。
- [FRAME] 所有更新请求都先按 `owner_user_id` 加载资源，再应用修改；禁止“全局加载后只在前端隐藏”。

## 资源写入与隔离规则

### API Key 与 Client

- [FRAME] 普通用户创建 Key 时绑定当前 Principal；管理员在目标用户页创建 Key 时绑定路径中的目标用户。
- [FRAME] 已有 Key 全部归 `legacy_owner_user_id`；它们的内容、有效状态和使用次数不因迁移改变。
- [FRAME] Client 使用 Key 换 Token 时，Server 从 Key 解析用户，先检查用户可运行，再写入或校验 `registered_clients.owner_user_id`。
- [FRAME] 同一 `install_id` 已存在时，新 Key 的用户必须与 Client 所有者一致；不同用户不能接管该 Client。
- [FRAME] Client Token 签发时校验 Key Owner 与 Client Owner 一致；后续 Token 认证只通过 `client_id -> registered_clients.owner_user_id` 解析用户，不要求原 API Key 仍然存在。
- [FRAME] 删除单个 API Key 不撤销已经签发的 Client Token、不关闭已认证 Client，只影响该 Key 后续换取新 Token。
- [FRAME] 禁用用户不修改 Key 的 `is_active`，也不撤销 Client Token；认证入口根据用户状态拒绝。
- [FRAME] 恢复用户后，既有 Client Token 继续用于自动重连。
- [FRAME] 删除用户会连同 API Key、Client Token 和 Client 注册一起物理删除；旧 Client 再次重连时按无效 Token 处理。

### 隧道

- [FRAME] `server_expose` 隧道的 Target Client 必须与隧道 `owner_user_id` 一致。
- [FRAME] `client_to_client` 隧道的 Ingress Client 和 Target Client 都必须与隧道 `owner_user_id` 一致。
- [FRAME] REST 创建、更新、启动、停止、迁移和删除全部使用同一所有权服务层。
- [FRAME] legacy `MsgTypeProxyCreate` 从已认证 `ClientConn.OwnerUserID` 获取所有者，执行相同的用户状态和 Client 所有权检查。
- [FRAME] 隧道迁移只允许迁移到同一用户的 Client，不实现迁移时改变用户所有权。
- [FRAME] 隧道的统一运行语义继续来自 `TunnelSpec` endpoint 字段；不得借多用户改造重新依赖 legacy 扁平字段。

### 流量与活动历史

- [FRAME] 新流量桶从隧道保存 `owner_user_id` 快照，隧道或 Client 后续删除也不影响历史查询。
- [FRAME] 普通用户流量查询强制绑定当前 Principal，不能接受任意 `user_id` 查询参数。
- [FRAME] 管理员从目标用户页查询流量时使用显式管理员用户范围接口。
- [FRAME] 历史活动的用户范围保存于事件本身，不能在查询时只依赖可能已经删除的当前资源。
- [FRAME] 上述历史保留只适用于单独删除 Client 或隧道；删除用户时必须删除其全部流量桶，以及 Scope、Subject、用户 Actor、所属 Client Actor 或资源关联指向该用户的全部活动事件。

## 用户生命周期与运行态收敛

### 状态变更事务

[FRAME] 禁用、恢复和删除采用每用户生命周期锁串行化；管理员标识、状态和删除的数据库提交还必须经过同一个全局用户管理锁，保证跨用户并发操作不能同时移除最后一个正常管理员。

[FRAME] 禁用遵守下面的提交边界：

```text
1. 锁定目标用户生命周期操作，并保持到本次状态收敛结束
2. 仅在即将提交状态时锁定全局用户管理操作
3. 事务内读取并验证当前用户的 is_admin 和 status，并检查正常管理员不变量
4. 写入 status=disabled、updated_at，撤销 Web Session，并写入活动日志
5. 提交用户状态事务并立即释放全局用户管理锁
6. 更新或失效 Server 内部用户策略缓存
7. 在显式有限超时内关闭该用户 SSE、全部逻辑 Client 会话、控制通道、数据通道、P2P 和隧道运行态
8. 对该用户全部 desired_state=running 隧道阻断 reconcile
9. 确认不存在仍可承载流量的会话后释放目标用户生命周期锁并返回成功
```

- [FRAME] 第 5 步提交后用户状态已是权威门禁；即使后续清理遇到瞬时错误，新认证和新 reconcile 也必须被拒绝。
- [FRAME] 全局用户管理锁只覆盖数据库事务，不能覆盖缓存失效、网络关闭、运行态收敛或等待 ACK；一个用户的慢连接不能阻塞其他用户的管理操作。
- [FRAME] 运行态清理失败或超过有限超时必须记录活动和服务日志，并返回稳定的 `503 user_disable_incomplete`；用户保持 `disabled`，重试请求继续收敛，清理尚未完成时不能返回成功。
- [FRAME] 清理任务必须绑定本次用户生命周期代次和可取消 Context；接口超时释放生命周期锁后，不允许失去代次保护的后台清理继续关闭未来恢复后创建的新会话或运行态。
- [FRAME] enable 在把状态提交为 `active` 前必须先确认上一次 disable 已完全收敛；若仍有残留运行态，复用同一清理路径，失败或超时时继续保持 `disabled` 并返回 `503 user_disable_incomplete`。
- [FRAME] 禁用、恢复和管理员标识设置设计为幂等；没有实际状态转换时不重复写活动事件，但禁用重试仍必须重新确认运行态已经收敛。

### 用户硬删除事务

[FRAME] 用户删除使用独立的用户级删除服务，不能循环调用单 Client 删除接口；用户内多个 Client 可能共同出现在同一条 `client_to_client` 隧道中，循环删除会产生重复清理和部分提交风险。

```text
1. 锁定目标用户生命周期操作，并保持到删除结束
2. 验证目标用户存在、status=disabled 且不是当前操作者
3. 在显式有限超时内再次确认并清理任何残留的控制、数据、P2P 和隧道运行态；失败则保持用户禁用并终止删除
4. 获取 clientTunnelMutationMu 冻结 Client/Tunnel 变更，再锁定全局用户管理操作并开启数据库事务
5. 重新验证目标用户状态、当前操作者和正常管理员不变量，并快照目标 Client ID 与 Tunnel ID 集合
6. 清空其他用户保留资源中 created_by_user_id=target 的执行者引用
7. 删除 scope_user_id=target、subject_user_id=target、类型为 admin/user 且 ID=target 的 Actor、类型为 client 且属于目标用户的 Actor，或关联到目标 Client/Tunnel 的全部活动事件及其关联行
8. 删除目标用户的流量桶、隧道资源锁、隧道、按目标 Client ID 或非空 install ID 命中的 Client Token、Client 状态、磁盘信息和 Client 注册
9. 删除目标用户的 API Key Permission、API Key、Web Session、TOTP、Recovery Code、Passkey 和 Challenge
10. 删除 users 行并提交事务，依次释放全局用户管理锁和 clientTunnelMutationMu
11. 失效管理员用户列表缓存，发布不持久化的列表刷新 SSE，再释放目标用户生命周期锁
```

[KNOWN] 当前实现以全局 `user_list_changed` SSE 完成第 11 步；事件只用于管理员列表缓存失效，不写入 `activity_events`，用户范围 SSE 不接收该事件。

- [FRAME] 第 4 至第 10 步必须是一个 SQLite 事务；任一删除失败时全部回滚，用户继续保持禁用状态。
- [FRAME] 删除事务不写持久化 `user_deleted` 活动事件，因为这会保留被删除用户的身份或目标记录。
- [FRAME] 删除成功返回 `204`；同一 ID 再次删除返回 `404`，不存在软删除幂等状态。

### 运行时索引与锁顺序

- [FRAME] `ClientConn` 增加只由 Server 认证结果设置的 `OwnerUserID`，不从 Client 消息接收该值。
- [FRAME] 单实例 Server 可以先遍历在线逻辑会话按 `OwnerUserID` 收敛；如果性能测试证明不足，再增加 `userID -> clientID` 内存索引。
- [FRAME] 固定锁顺序以目标用户生命周期锁为最外层；运行态收敛阶段在不持有全局锁时按 `clientTunnelMutationMu -> Client lifecycle -> tunnel runtime operation` 获取并释放，删除事务需要同时持锁时按 `clientTunnelMutationMu -> 全局用户管理锁 -> SQLite 事务` 获取。
- [FRAME] 禁止持有全局用户管理锁等待目标用户生命周期锁、`clientTunnelMutationMu`、WebSocket 写锁、Client ACK 或运行态操作；并发测试必须覆盖禁用与重连、迁移、删除以及不同用户管理操作互不饥饿。
- [FRAME] 用户状态事务不能在持有 WebSocket 写锁或等待 Client ACK 时保持打开。

### 所有运行入口都要门禁

[FRAME] 下面每个入口都必须调用同一个 `IsUserOperational(userID)` 服务，不能只保护 REST：

1. [FRAME] API Key 换取 Client Token。
2. [FRAME] Client Token 控制通道认证。
3. [FRAME] 控制通道认证完成到 `ClientConn` 发布之间的最终检查。
4. [FRAME] 数据通道握手返回成功之前的检查。
5. [FRAME] legacy Client 隧道创建。
6. [FRAME] REST 隧道创建、更新、启动和迁移。
7. [FRAME] Server 启动时的隧道恢复。
8. [FRAME] 周期 reconcile 重试。
9. [FRAME] Client 数据通道恢复触发的 reconcile。
10. [FRAME] P2P 会话授权、Grant 生成和重建。

[INFERRED] 只检查控制通道认证会留下“禁用发生在控制通道成功与数据通道上线之间”的竞态；只检查 REST 会留下 legacy 创建和自动恢复绕过。

[KNOWN] 当前控制通道认证在 `adminStore == nil` 时会跳过 Key/Token 校验，并生成 `unmanaged-<install_id>` Client ID。

[FRAME] 多用户实现必须删除这个 unmanaged fallback：`auth`、`adminStore`、`UserStore` 或 Client Owner 解析任一不可用时，控制通道都以 `server_uninitialized` 的可重试认证失败关闭，不能创建空 `OwnerUserID` 或 `unmanaged-*` 的 `ClientConn`。

[FRAME] 生产启动、开发构造、测试构造和其他替代构造路径都必须注入统一 Store；故意不注入的测试只能断言 fail closed，不能再依赖无认证 Client 路径。

## Client 控制通道和数据通道兼容性

### 不增加用户字段

- [FRAME] `AuthRequest` 不增加 `user_id`；Key 或 Token 已经足以让 Server 解析注册 Client 和所属用户。
- [FRAME] 数据通道握手不增加 `user_id`；Server 通过 `client_id + data_token` 找到当前逻辑会话及其 `OwnerUserID`。
- [FRAME] `TunnelProvisionRequest`、Provision ACK、数据流 Header 和 P2P 信令不增加用户字段。
- [FRAME] Client 不承担租户授权；所有权校验只在 Server 完成。

[INFERRED] 因此核心控制消息和数据帧结构不需要变化，旧 Client 与新 Server 的主要兼容风险来自认证失败语义，而不是用户 ID 编解码。

### 认证失败语义

[FRAME] 可以复用现有 `AuthResponse.Code`、`Retryable` 和 `ClearToken` 表达用户门禁：

| 原因 | `Code` | `Retryable` | `ClearToken` | 预期 Client 行为 |
|---|---|---:|---:|---|
| 用户禁用 | [FRAME] `user_disabled` | [FRAME] `true` | [FRAME] `false` | [FRAME] 保留 Token，按现有退避重连，恢复后自动上线。 |
| Token 随用户删除 | [FRAME] `invalid_token` | [FRAME] `false` | [FRAME] `true` | [FRAME] 删除本地 Token；用户及 Client 注册已经不存在，不能再自动恢复。 |

- [KNOWN] 当前 Client 对未知 `Code` 已按 `Retryable` 和 `ClearToken` 通用处理。
- [INFERRED] 更老的已发布 Client 是否具有相同行为不能凭当前代码外推，必须通过版本兼容测试确认。
- [FRAME] 禁用时主动关闭已在线连接，重连请求收到 `user_disabled`；不能只等待网络自然断开。
- [FRAME] 删除发生前用户已经禁用且连接已经关闭；删除后旧 Client 使用残留 Token 重连时收到现有 `invalid_token + ClearToken=true`。
- [FRAME] 删除单个 API Key 不改变上述 Token 认证路径，已签发 Token 继续可用。

## 管理 API 契约

### 用户 DTO

[FRAME] 管理 API 返回的用户对象至少包含：

```json
{
  "id": "user-id",
  "username": "alice",
  "is_admin": false,
  "status": "active",
  "created_at": "2026-08-02T00:00:00Z",
  "updated_at": "2026-08-02T00:00:00Z",
  "operational": true
}
```

- [FRAME] `operational` 是派生方便字段，权威数据仍是用户行存在且 `status = active`。
- [FRAME] `is_admin` 只能通过管理员专用的显式目标状态接口修改，不能混入用户名或普通资料更新接口。
- [FRAME] 密码 Hash、Session ID、API Key 原文和 Client Token 不出现在用户 DTO。

### 用户列表与分页

[FRAME] 用户列表采用 Server 端 keyset/cursor 分页，默认按 `(created_at DESC, id DESC)` 稳定排序：

```text
GET /api/admin/users
    ?limit=50
    &cursor=<opaque>
    &query=<username>
    &status=active|disabled
    &is_admin=true|false
```

[FRAME] 返回结构：

```json
{
  "items": [],
  "next_cursor": null,
  "has_more": false
}
```

- [FRAME] `limit` 设默认值和硬上限，禁止通过超大 limit 退化成全表查询。
- [FRAME] cursor 是不透明值并绑定当前排序；筛选条件变化时前端丢弃旧 cursor。
- [FRAME] 用户列表必须包含管理员行。
- [FRAME] 如果列表显示 Client、在线 Client 和隧道数量，只对当前页用户做批量聚合，禁止每行一个查询。
- [FRAME] 第一版不要求返回全量 `total`；用户只需要稳定翻页，不为精确总数增加每次全表计数。

### 用户管理接口

[FRAME] 第一版接口：

```text
GET    /api/admin/users
POST   /api/admin/users
GET    /api/admin/users/{user_id}
GET    /api/admin/users/{user_id}/deletion-impact
PUT    /api/admin/users/{user_id}/username
PUT    /api/admin/users/{user_id}/password
PUT    /api/admin/users/{user_id}/admin        {"is_admin": true|false}
POST   /api/admin/users/{user_id}/disable
POST   /api/admin/users/{user_id}/enable
DELETE /api/admin/users/{user_id}
POST   /api/admin/users/{user_id}/sessions/revoke
```

- [FRAME] `POST /api/admin/users` 只创建 `is_admin = 0` 且 `status = active` 的用户，并在一个事务中写用户和活动日志。
- [FRAME] `PUT .../admin` 写期望状态而不是翻转状态；升降级成功后撤销目标 Web Session，降级时同时清除管理员安全凭据。
- [FRAME] disable、enable、admin 和 delete 都执行最后一个正常管理员检查；跨用户并发请求在同一个全局用户管理锁内完成检查和写入。
- [FRAME] 当前管理员不能 disable 或 delete 自己；自我降级仅在另一个正常管理员存在时允许。
- [FRAME] disable 状态已提交但运行态未在有限超时内收敛时返回 `503` 和稳定错误码 `user_disable_incomplete`；重试同一接口继续清理，不把用户恢复成 active。
- [FRAME] enable 发现上一次禁用尚未完全收敛时执行同一清理；仍未收敛也返回 `503 user_disable_incomplete`，只有零残留后才提交 active。
- [FRAME] delete 只接受 `status = disabled` 的用户；正常用户返回 `409 user_must_be_disabled`，目标不存在返回 `404`。
- [FRAME] delete 没有 soft、restore、retain 或 transfer 参数，成功后返回 `204`。
- [FRAME] `GET .../deletion-impact` 在同一只读事务快照中返回 `user_id`、`api_keys`、`clients`、`tunnels`、`traffic_buckets`、`activity_events` 和 RFC3339 `generated_at`；活动计数必须复用硬删除的同一 typed Actor/Scope/Subject/资源关联谓词，前端必须校验响应 `user_id` 与路径目标一致后才允许确认删除。

### 显式管理员资源范围

[FRAME] 管理员用户详情页使用显式目标用户路径，不用可省略的 `user_id` 参数切换“全局或单用户”语义：

```text
GET /api/admin/users/{user_id}/console/snapshot
GET /api/admin/users/{user_id}/clients
GET /api/admin/users/{user_id}/tunnels
GET /api/admin/users/{user_id}/activity
GET /api/admin/users/{user_id}/keys
```

- [FRAME] 管理员创建 Key、Client 注册材料或隧道时，目标用户也必须来自显式路径或已经按路径加载的资源上下文。
- [FRAME] 现有 `/api/clients`、`/api/tunnels`、`/api/activity` 和流量接口改为当前 Principal 自身范围，不再让管理员身份隐式获得全量响应。
- [FRAME] 真正的 Server 全局配置仍留在 `/api/admin/config`、`/api/admin/security` 和全局访问控制接口。
- [FRAME] Web 与 Server 一起升级，前端直接切换到新契约，不写旧 API fallback、双请求探测或版本分支。

## “活动”的最终定位

### 名称

- [FRAME] 产品界面统一使用“活动日志”；不再称为“用户通知”。
- [FRAME] 后端可继续使用 `activity_events`、`/api/activity` 和现有 Go 类型名，避免没有语义收益的大范围重命名。
- [FRAME] “活动日志”是结构化的操作与运行事件时间线，不等同于进程 stdout/stderr 原始日志。

### 可见范围与 Actor

- [FRAME] 普通用户只能读取 `scope_user_id = principal.UserID` 的活动。
- [FRAME] 管理员可以读取全局活动，并按用户筛选；进入用户详情时默认固定为该用户范围。
- [FRAME] 管理员代用户操作时，`scope_user_id` 和 `subject_user_id` 是目标用户，Actor 为 `type=admin, id=管理员用户 ID`。
- [FRAME] 普通用户操作自己的资产时，`scope_user_id`、`subject_user_id` 和 Actor 用户 ID 都是该普通用户，Actor Type 使用 `user`；`normalizeActivityActor` 必须显式接受该类型。
- [FRAME] Client 生命周期和隧道运行事件的 Actor 可以是 `client` 或 `system`，但 `scope_user_id` 和 `subject_user_id` 必须从 Client 或隧道所有权解析。
- [FRAME] 管理员登录、MFA、Passkey 和 Server 配置等全局安全事件保持 `scope_user_id = NULL`，只对管理员可见；只要已经解析出相关用户，就必须写入 `subject_user_id`。
- [FRAME] 设置管理员、移除管理员、禁用和恢复都写结构化活动事件；删除用户时删除目标 Scope、Subject、用户 Actor、所属 Client Actor 以及本次删除可能产生的全部持久化事件，因此删除操作不会留下用户墓碑日志。
- [FRAME] 密码、Token、Key 原文和敏感认证材料不得写入活动 Payload。

### SSE

- [FRAME] SSE 订阅必须绑定 Principal 和明确的用户范围，Snapshot、Client、Tunnel、Traffic 和 Activity 事件都在 Server 端过滤。
- [FRAME] EventBus 内部事件需要携带足够的 `scope_user_id` 元数据；不能把全局事件先发给浏览器再过滤。
- [FRAME] 用户切换、禁用、管理员身份变化、删除和退出时，前端关闭旧 SSE，并清除旧作用域的 TanStack Query 缓存。
- [FRAME] 不增加通知收件人、已读状态、通知偏好或投递表。

## 管理端信息架构与路由

### 路由结果

[FRAME] 管理员进入 Dashboard 后先看到分页用户列表，而不是一次加载全局 Client 和隧道：

```text
/dashboard
  is_admin = 1 -> /dashboard/users
  is_admin = 0 -> 当前用户自己的资源页

/dashboard/users
  分页用户列表、搜索、筛选、添加和每行三点操作菜单

/dashboard/users/$userId
  用户详情页；tab=topology|clients|tunnels

/dashboard/users/$userId/clients/$clientId
  该用户范围内的 Client 详情
```

- [FRAME] 用户详情页的三个模块固定为“网络拓扑”“客户端”“隧道”。
- [FRAME] 用户列表每行显示三点操作图标；菜单按当前状态提供“设为管理员/移除管理员”“禁用用户/恢复用户”和“删除用户”。
- [FRAME] “删除用户”在目标未禁用时保持不可执行，并明确提示必须先禁用；确认框明确列出用户、Client、隧道、Key、Token、流量和活动数据将永久删除。
- [FRAME] 当前操作者的“禁用用户”和“删除用户”不可执行；最后一个正常管理员的降级、禁用和删除操作不可执行。
- [FRAME] 管理员行同样可以进入详情并查看自己的资源；是否允许操作由上述不变量决定，不再把所有管理员统一视为永久受保护用户。
- [FRAME] 删除成功后该用户立即从列表消失，不存在已删除筛选或已删除详情页。
- [FRAME] 普通用户使用同一组作用域化资源组件，作用域固定为自身，不能选择其他用户。
- [FRAME] API Key 管理放入“客户端”模块的添加 Client 流程或用户范围子区域，不增加第四个顶层资源 Tab。
- [FRAME] 现有全局“活动”导航改名为“活动日志”；管理员页面增加用户筛选，不把它改成通知中心。

### 前端重构边界

- [FRAME] `DashboardLayout` 不再无条件调用全局 `useClients()`；资源数据下沉到用户作用域页面。
- [FRAME] 提取 `ResourceScope`，至少区分 `self` 与 `admin-user:{userId}`，并由 API wrapper 转换为明确后端路径。
- [FRAME] Query Key 必须包含用户 ID 或 `self`，例如 `['users', userId, 'clients']`、`['users', userId, 'tunnels']` 和 `['users', userId, 'activity']`。
- [FRAME] `OverviewPage`、`NetworkTopology`、`DashboardClientTable`、`DashboardTunnelTable`、Client Sidebar 和 Add Client Dialog 接收同一个 Scope，不各自猜当前用户。
- [FRAME] 切换目标用户时不能沿用上一用户的缓存作为占位数据；避免短暂展示跨用户数据。
- [FRAME] Logout、401、用户禁用、管理员身份变化和目标用户删除时清理所有用户范围 Query Cache。
- [FRAME] 路由继续使用 Hash History，业务组件继续放在 `web/src/components/custom/`，请求继续统一走 `web/src/lib/api.ts`。

## 分阶段执行计划

### 阶段 0：契约测试和安全护栏

**改动**

1. [FRAME] 把本文中的用户状态、所有权、不变式、错误码和路由契约转成测试命名与固定 Fixture。
2. [FRAME] 增加 `/api/admin/*` 必须为管理员的路由矩阵测试。
3. [FRAME] 增加普通用户不能访问尚未作用域化资源接口的拒绝测试，确保后续实现不会提前开放。
4. [FRAME] 准备旧版数据库、多个现有管理员和已发布 Client 二进制的迁移与兼容测试 Fixture。
5. [FRAME] 固定认证 Store 缺失、Owner 解析失败和空 Owner 必须 fail closed 的测试，禁止测试环境继续依赖 `unmanaged-*` Client。

**阶段门**

- [FRAME] 在没有完成所有权隔离前，普通用户登录与前端入口保持不可用。
- [FRAME] 安全测试先失败且失败原因符合本文，才进入实现阶段。

### 阶段 1：严格 Schema、用户 Store 与数据回填

**主要位置**

- [KNOWN] `internal/server/migrations/`
- [KNOWN] `internal/server/storage_schema.go`
- [KNOWN] `internal/server/storage_schema_test.go`
- [KNOWN] `internal/storage/sqlite.go`
- [KNOWN] `internal/storage/sqlite_test.go`
- [KNOWN] `internal/server/admin_store.go`
- [KNOWN] `internal/server/server_bootstrap.go`

**改动**

1. [FRAME] 新增统一 `users`、`user_sessions`、所有权列和活动 `subject_user_id`；密码 Hash 与 `is_admin` 保存在 `users`。
2. [FRAME] 把 Server migration 编排改为按全局文件版本执行并按各自 strict/compatible ledger 记录，给通用执行器增加可选事务校验 Hook，把 `012` 的专用校验器绑定到 strict migration，并更新执行器与精确 Schema/Index 测试。
3. [FRAME] 实现全部现有 `admin_users` 到 `users`、`admin_sessions` 到 `user_sessions` 的数据迁移，并按确定顺序选择 `legacy_owner_user_id`，同时完成 fresh initialization 的统一写入。
4. [FRAME] 逐表重建管理员安全表并接入 `users` 外键，回填活动 Subject；验证行数、唯一约束、索引和 `PRAGMA foreign_key_check` 后删除 `admin_sessions` 与 `admin_users`。
5. [FRAME] 修改管理员用户名、密码更新与 reset 流程，保持统一用户 ID。
6. [FRAME] 增加 `UserStore` 的创建、分页、状态转换、管理员标识、硬删除、凭据和 Session 事务。
7. [FRAME] 增加迁移后所有权不变量检查。

**验证**

- [FRAME] fresh DB、旧版 DB 和已有资源 DB Fixture 全部验证；当前版本没有 legacy JSON import Fixture。
- [FRAME] 证明 fresh DB 严格按 `001-009 -> 010-011 -> 012` 执行，`011` 已创建 Activity 表后 `012` 才增加用户范围字段；验证校验失败会在 ledger 写入前整体回滚，旧 Server 拒绝打开已升级数据库。
- [FRAME] 证明现有 Key、Client、Tunnel、Traffic 全部归确定的 `legacy_owner_user_id` 且数量不变，全部旧用户都成为管理员。
- [FRAME] 证明管理员安全表的行数和约束不变、旧 `admin_users`/`admin_sessions` 表不存在，数据库通过 `PRAGMA foreign_key_check`；注入计数、索引、外键和旧表删除异常时 migration 回滚且 ledger 不记录 `012`。
- [FRAME] 证明硬删除用户会清除全部所属资产、历史和凭据，同时不会删除其他用户的资源。
- [FRAME] 证明管理员 reset 后原有资产仍引用同一个用户 ID。

**阶段门**

- [FRAME] 当前 Server 不再产生 `owner_user_id IS NULL` 的资源根。
- [FRAME] 迁移前后资源数量、隧道配置和运行意图完全一致。

### 阶段 2：Principal、认证与管理员边界

**主要位置**

- [KNOWN] `internal/server/auth_middleware.go`
- [KNOWN] `internal/server/admin_api.go`
- [KNOWN] `internal/server/admin_security_api.go`
- [KNOWN] `internal/server/server_http.go`

**改动**

1. [FRAME] 引入统一 `RequestPrincipal`、`user_sessions` 解析和 `users.is_admin` 权限判断。
2. [FRAME] 将 `/api/admin/*`、管理员安全和全局配置明确收紧到管理员。
3. [FRAME] 扩展统一密码登录、logout 和 `/api/auth/me`。
4. [FRAME] 实现普通用户密码校验、Session 创建、撤销和状态门禁。
5. [FRAME] 保持迁移后既有 JWT 的 Session ID 可解析性，不保留独立管理员 Session 查询路径。
6. [FRAME] 管理员身份变化后撤销目标 Session；降级时清除管理员安全凭据并让旧请求立即失去管理员权限。

**验证**

- [FRAME] 覆盖管理员密码、MFA、Passkey、普通用户密码、Session UA 绑定、过期 Session 和撤销 Session。
- [FRAME] 覆盖禁用、硬删除、升降管理员、伪造 `is_admin` 权限、跨用户 Session、未知用户状态和失效 Session。

**阶段门**

- [FRAME] 任意普通用户都无法通过 `/api/admin/*` 或伪造 `is_admin` 状态提升权限。

### 阶段 3：用户管理 API 与分页

**主要位置**

- [FRAME] 新增用户管理 Handler/Store 文件，避免继续扩大单个 `admin_api.go`。
- [KNOWN] `internal/server/server_http.go`
- [KNOWN] `internal/server/activity_store.go`

**改动**

1. [FRAME] 实现分页列表、详情、添加、用户名修改、密码重置和 Session 撤销。
2. [FRAME] 实现全局用户管理锁、最后一个正常管理员约束、自我操作约束和幂等管理员目标状态接口。
3. [FRAME] 实现幂等 disable、enable，以及只接受 disabled 用户的原子 hard delete。
4. [FRAME] 此阶段只完成权威状态和删除事务；对外开放 disable/delete 前必须完成阶段 5 的运行态收敛。

**验证**

- [FRAME] 覆盖 cursor 边界、同时间戳稳定顺序、筛选变化、最大 limit、并发重名和删除后用户名复用。
- [FRAME] 覆盖并发 disable/delete、两个管理员并发互相降级或禁用、重复目标状态请求、自我操作和最后一个正常管理员保护。

**阶段门**

- [FRAME] 用户列表永不执行无上限查询，状态转换只有一条事务入口。

### 阶段 4：所有资源的用户作用域

**主要位置**

- [KNOWN] `internal/server/admin_store.go`
- [KNOWN] `internal/server/control_auth.go`
- [KNOWN] `internal/server/unified_tunnel_api.go`
- [KNOWN] `internal/server/store.go`
- [KNOWN] `internal/server/traffic_store.go`
- [KNOWN] `internal/server/activity_store.go`

**改动**

1. [FRAME] API Key 创建、查询、状态变更和删除接入用户所有权。
2. [FRAME] Client 注册、重连、详情、改名、带宽、版本检查、流量和删除接入用户所有权。
3. [FRAME] 隧道 CRUD、动作、迁移和 legacy 创建接入同一作用域服务。
4. [FRAME] `client_to_client` 两端做同用户强校验。
5. [FRAME] 流量桶写入用户快照并实现 self/admin-user 两类查询。
6. [FRAME] 删除所有通过空用户 ID 表达全局权限的内部调用。
7. [FRAME] Token 认证只通过 Client Owner 解析用户，确保单独删除 API Key 不影响已签发 Token。

**验证**

- [FRAME] 为每个资源接口生成用户 A、用户 B、管理员三主体矩阵。
- [FRAME] 对每个按 ID 的接口验证跨用户返回 `404`。
- [FRAME] 覆盖 Key 与 Client owner 不一致、相同 install ID 接管、跨用户迁移和 legacy 创建绕过。

**阶段门**

- [FRAME] 普通用户身份开放前，所有 REST、后台和 legacy 写入口都已带显式 User Scope。

### 阶段 5：禁用、删除与运行态门禁

**主要位置**

- [KNOWN] `internal/server/control_auth.go`
- [KNOWN] `internal/server/data.go`
- [KNOWN] `internal/server/session.go`
- [KNOWN] `internal/server/control_loop.go`
- [KNOWN] `internal/server/unified_tunnel_reconcile.go`
- [KNOWN] `internal/server/client_relay.go`
- [KNOWN] P2P 管理相关 Server 文件

**改动**

1. [FRAME] `ClientConn` 保存 Server 解析的非空 `OwnerUserID`，删除 `unmanaged-*` fallback，并让缺失 Store 或 Owner 的所有构造路径 fail closed。
2. [FRAME] Key 交换、Token 控制认证和数据握手增加同一用户状态门禁。
3. [FRAME] disable 复用逻辑会话失效路径关闭控制、数据、P2P 和运行态；delete 在确认运行态已收敛后执行用户级硬删除事务。
4. [FRAME] 全局用户管理锁只覆盖状态或删除数据库事务；运行态清理只持有目标用户生命周期锁，并使用显式有限超时。
5. [FRAME] startup、retry、data-ready、legacy create 和 P2P Grant 全部增加门禁。
6. [FRAME] 运行态被阻断时保持 desired state，投影 offline 和 owner issue。
7. [FRAME] enable 先确认不存在旧代次残留运行态，再提交 active，并依赖 Client 重连和现有 reconcile 恢复运行；旧清理任务不能影响恢复后的新代次。
8. [FRAME] 完成后才对外启用阶段 3 的 disable、enable 和 delete 路由。

**验证**

- [FRAME] 禁用与控制认证、数据握手、Tunnel 创建、迁移、P2P 建链并发竞争全部覆盖。
- [FRAME] 覆盖缺失 Store、Owner 解析失败和替代 Server 构造路径，证明不会建立未认证或空 Owner 的 Client 会话。
- [FRAME] 证明禁用返回成功时已不存在该用户可承载流量的逻辑会话。
- [FRAME] 证明单个用户清理超时返回 `503 user_disable_incomplete` 且保持禁用，同时其他用户的管理操作不被该清理长期阻塞。
- [FRAME] 证明 disable 超时后立即 enable 会先完成残留清理；旧代次清理任务不能关闭恢复后新建的 Client 会话或隧道运行态。
- [FRAME] 证明 Server 重启不会恢复禁用用户的运行态，已删除用户和资产不存在。
- [FRAME] 证明恢复不改变原配置，Client 重连后只恢复原本 desired running 的隧道。

**阶段门**

- [FRAME] 任意一个运行入口缺少门禁都阻止进入前端阶段。

### 阶段 6：活动日志与 SSE 隔离

**主要位置**

- [KNOWN] `internal/server/activity_store.go`
- [KNOWN] `internal/server/activity_api.go`
- [KNOWN] `internal/server/events.go`
- [KNOWN] 各 Activity Producer 文件

**改动**

1. [FRAME] 所有用户资源活动写入 `scope_user_id` 和 `subject_user_id`，已解析用户的全局安全事件至少写入 `subject_user_id`。
2. [FRAME] Actor、Scope 与 Subject 分开传递，增加 `user` Actor Type，并覆盖管理员代操作。
3. [FRAME] 活动查询增加 self、admin-user 和 admin-global 三种明确入口。
4. [FRAME] SSE Envelope 增加服务端范围过滤，Snapshot 和增量使用同一 Scope。
5. [FRAME] 保留现有活动保留策略，不引入通知模型。
6. [FRAME] 用户硬删除同时删除目标 Scope、Subject、管理员/普通用户 Actor、所属 Client Actor 与资源关联事件，并验证关联表和 `dedupe_key` 没有目标用户残留。

**验证**

- [FRAME] 用户 A 收不到用户 B 的 Client、Tunnel、Traffic、Activity 和恢复补洞事件。
- [FRAME] 管理员代用户操作同时显示正确的目标范围和真实 Actor。
- [FRAME] Session 环境不匹配等全局安全事件在保持管理员可见的同时，以 Subject 关联实际用户，删除用户后不残留其 ID。
- [FRAME] SSE 断线恢复 Cursor 不能跨用户补发。

**阶段门**

- [FRAME] 浏览器网络层无法观察到其他用户的事件 Payload。

### 阶段 7：管理员用户列表与用户详情前端

**主要位置**

- [KNOWN] `web/src/lib/router.ts`
- [KNOWN] `web/src/lib/auth.ts`
- [KNOWN] `web/src/lib/api.ts`
- [KNOWN] `web/src/stores/auth-store.ts`
- [KNOWN] `web/src/hooks/`
- [KNOWN] `web/src/components/custom/dashboard/`
- [KNOWN] `web/src/components/custom/client/`
- [KNOWN] `web/src/components/custom/tunnel/`

**改动**

1. [FRAME] Auth Store 从 `AdminUser` 改为统一 Principal，并在启动时调用 `/api/auth/me`。
2. [FRAME] 增加管理员用户列表、cursor 翻页、搜索、状态和管理员筛选。
3. [FRAME] 增加每行三点操作菜单，以及添加、设为/移除管理员、禁用、恢复、硬删除、改名和重置密码流程。
4. [FRAME] 增加 `/dashboard/users/$userId`，复用三资源模块并注入 Scope。
5. [FRAME] 将 Query Key、Mutation invalidation、SSE 和 Client 详情路由全部用户化。
6. [FRAME] 把 API Key 管理接入目标用户的 Client 模块。
7. [FRAME] 把“活动”文案收敛为“活动日志”，增加管理员用户筛选。
8. [FRAME] 删除 Web API 兼容 fallback；只调用本版本 Server 契约。

**验证**

- [FRAME] 运行前端单元测试、`bun run lint` 和 `bun run build`。
- [FRAME] 浏览器手工验证多个管理员、普通用户、禁用用户、硬删除消失、最后管理员保护和 Session 被撤销场景。
- [FRAME] 验证快速切换用户、浏览器返回、SSE 重连和 Query Cache 不出现跨用户闪现。

**阶段门**

- [FRAME] 管理员 Dashboard 默认不再全量查询所有 Client；用户列表是资源管理的第一入口。

### 阶段 8：系统验证与发布门

1. [FRAME] 运行相关 Go 包测试后运行 `go test ./...`。
2. [FRAME] 按 CI 顺序运行前端 lint/build、`go vet ./...` 和目标平台测试。
3. [FRAME] 运行 `make build` 验证前端嵌入和统一二进制。
4. [FRAME] 运行直连、nginx、caddy、控制通道、数据通道、Client-to-Client、断线重连和升级 E2E。
5. [FRAME] 运行下面单独定义的严格 migration 不可降级测试。
6. [FRAME] 只有数据隔离、禁用收敛、硬删除零残留、现有资源回填和 Client 跨版本矩阵全部有证据后才能发布 Beta。

## 测试矩阵

### Schema 与迁移

- [FRAME] fresh DB 初始化后恰有一个正常管理员用户。
- [FRAME] 旧版 DB 升级后全部现有用户 ID 不变、全部成为管理员，现有 Session 能迁移到 `user_sessions`。
- [FRAME] 升级后 `admin_users` 和 `admin_sessions` 不存在，原密码、MFA 和 Session 只保留在统一结构中。
- [FRAME] Recovery Code、Passkey 和 Challenge 迁移前后行数、UNIQUE 约束和索引一致，全部 `user_id` 外键指向 `users`，`PRAGMA foreign_key_check` 返回空结果。
- [FRAME] `012` 的事务校验 Hook 在行数、索引、外键或旧表删除检查失败时回滚全部 Schema 与数据变更，`schema_migrations` 不出现 `012`；普通 migration 在没有 Hook 时保持现有行为。
- [FRAME] 现有 `session_environment_mismatch` 安全事件正确回填 `subject_user_id`，无法匹配现有用户的旧事件被删除，不存在仅在 `dedupe_key` 中保留用户 ID 的事件。
- [FRAME] 升级前所有 Key、Client、Tunnel 和 Traffic 数量不变且归按 `(created_at, id)` 确定的 `legacy_owner_user_id`。
- [FRAME] 隧道 desired state、endpoint、transport policy 和资源锁迁移前后不变。
- [FRAME] `012_multi_user_ownership` 只出现在 strict migration ledger，旧 Server 对已升级数据库启动失败且不会开始监听。
- [FRAME] fresh DB、仅有 strict `001-009` 的 DB、已有 compatible `010-011` 的 DB 和已完成 `012` 的 DB 都按全局版本顺序打开；不存在 `012` 先于 `011` 执行的路径。
- [FRAME] hard delete 用户后全部直接和派生数据为零，其他用户的资源数量和内容不变。
- [FRAME] 管理员 reset、改名、重启后资产所有权不漂移。
- [FRAME] 当前 Server 的全部创建路径拒绝空 owner。

### 身份与隔离

- [FRAME] 用户 A 不能读取、修改、停止、迁移或删除用户 B 的任何资源。
- [FRAME] 用户 A 知道用户 B 的真实资源 ID 时仍得到 `404`。
- [FRAME] 普通用户不能访问任意 `/api/admin/*`。
- [FRAME] 普通用户不能把请求体中的 owner 改成其他用户。
- [FRAME] 管理员访问用户 A 资源时，活动 Actor 是管理员，Scope 是用户 A。
- [FRAME] 所有用户 Session 都只能通过 `user_sessions` 解析，权限只能来自对应 `users.is_admin`。
- [FRAME] 设为管理员和移除管理员立即撤销目标 Session，旧 Session 不能继续使用原权限。
- [FRAME] 两个管理员并发互相降级、禁用或删除时，最终至少保留一个正常管理员。
- [FRAME] 用户物理删除后可以创建同名新用户，但新用户 ID 与任何旧凭据都不相同。

### 用户列表

- [FRAME] 管理员包含在第一页或其稳定排序位置。
- [FRAME] 同 `created_at` 用户通过 ID 次排序不重复、不漏项。
- [FRAME] 新增或删除用户发生在翻页之间时，cursor 行为稳定并有测试定义。
- [FRAME] 用户删除后不再出现在任何列表结果中，也不存在 deleted 筛选。
- [FRAME] 任何请求不能绕过最大 `limit`。
- [FRAME] 页内资源计数没有 N+1 查询。

### 禁用、删除与运行态

- [FRAME] disable 提交后新 Key 交换和 Token 认证都失败为 `user_disabled`。
- [FRAME] disable 关闭当前控制通道、数据通道、P2P 和两类隧道运行态，且只有确认不再承载流量后才返回成功。
- [FRAME] disable 清理超过有限超时时返回 `503 user_disable_incomplete`，用户继续保持 disabled，重试可以完成收敛。
- [FRAME] 某用户的禁用清理停滞时，另一个用户的升降管理员、禁用、恢复或删除事务仍可完成，证明全局用户管理锁没有覆盖运行态等待。
- [FRAME] disable 不改变 Key `is_active`、Client 注册、Client Token、Tunnel desired state 或 Tunnel 配置。
- [FRAME] disable 后 Tunnel runtime state 不再报告 active，并显示 `owner_disabled`。
- [FRAME] disable 期间 Server 重启和 reconcile retry 都不能恢复隧道。
- [FRAME] enable 后 Client 保留 Token 并自动重连，desired running 隧道恢复，desired stopped 隧道不启动。
- [FRAME] disable 未收敛时 enable 不得提前提交 active；清理超时后旧代次任务不能影响之后成功恢复的新会话和隧道。
- [FRAME] 正常用户 delete 返回 `409 user_must_be_disabled`；disabled 用户 delete 成功后 `users` 行、凭据、资产、流量和活动记录全部不存在。
- [FRAME] 删除用户后，残留 Client Token 重连返回 `invalid_token + ClearToken=true`，不存在 `user_deleted` 门禁状态。
- [FRAME] 删除事务清空其他用户资源中的该用户 `created_by_user_id`，但不删除其他用户资源。
- [FRAME] disable/delete 与 Client 重连、数据握手、隧道迁移、P2P 建链并发时最终都 fail closed。
- [FRAME] 删除成功后重复 DELETE 返回 `404`，不能查看、恢复或筛选已删除用户。

### 控制与数据通道版本矩阵

- [FRAME] 旧版 Client 连接当前新 Server：正常用户行为不变。
- [FRAME] 旧版 Client 遇到禁用用户：不清 Token，并进入可恢复退避。
- [FRAME] 当前 Client 连接新 Server：正常、禁用、恢复和用户删除后 Token 清理全部覆盖。
- [FRAME] 当前 Client 连接基线旧 Server：现有认证、控制、数据和隧道行为不回归。
- [FRAME] 直连、nginx 和 caddy 都运行同一矩阵。
- [FRAME] `auth`、`adminStore`、`UserStore` 缺失或 Owner 解析失败时控制通道返回可重试的 `server_uninitialized`，不会创建 `unmanaged-*` 或空 Owner 会话。
- [FRAME] 控制通道成功后、数据通道握手前禁用用户时，数据通道不能上线。
- [FRAME] P2P 两端任一用户不正常或所有者不一致时，不生成可用 Grant。
- [FRAME] 删除 API Key 后，已经使用该 Key 换取 Token 的 Client 控制通道、数据通道和后续重连都继续可用；该 Key 本身不能再签发 Token。

### 活动日志与 SSE

- [FRAME] 用户只能查询自己的活动日志。
- [FRAME] 用户只能收到自己的 SSE Snapshot、增量和恢复事件。
- [FRAME] 管理员全局活动支持用户筛选，用户详情默认固定 Scope。
- [FRAME] 管理员代操作同时保留管理员 Actor 和目标用户 Scope。
- [FRAME] 设为管理员、移除管理员、禁用、恢复、登录拒绝和运行态清理均有结构化活动记录。
- [FRAME] 用户删除后，Scope、Subject、管理员/普通用户 Actor、所属 Client Actor 或 Client/Tunnel 关联指向该用户的活动事件及关联行全部不存在，也不保留 `user_deleted` 墓碑事件。
- [FRAME] 用户删除后，活动表不保留目标用户的 admin/user typed Actor、Subject、Scope，也不保留其所属 Client typed Actor 或 Client/Tunnel 关联；不同 Actor 类型中偶然相同的裸 ID 字符串不视为同一身份，不能据此误删无关事件。被删除事件的 Payload 和 `dedupe_key` 随事件一并消失。
- [FRAME] 活动 Payload 不包含密码、Key、Token、Session ID 或完整客户端地址敏感值。

### Web

- [FRAME] 管理员登录后到用户列表，普通用户登录后到自己的资源页。
- [FRAME] 用户列表真实分页，不在前端切片全量结果。
- [FRAME] 点击管理员用户能看到全部迁移的现有资产。
- [FRAME] 点击普通用户只加载该用户的拓扑、Client 和隧道。
- [FRAME] 快速切换用户时不展示上一用户缓存。
- [FRAME] disable、管理员身份变化或 delete 后目标用户现有 Web 页面失效并清理本地状态。
- [FRAME] 每个用户行都有三点操作菜单，并根据自身操作、目标状态和最后一个正常管理员约束禁用对应菜单项。
- [FRAME] 未禁用用户不能执行删除；删除确认明确告知全部数据不可恢复，删除成功后列表和详情缓存立即移除。
- [FRAME] 文案使用“活动日志”，不存在通知、已读或收件箱界面。

## 严格 migration 不可降级测试

[FRAME] 多用户 migration 是单向边界：新 Server 可以升级旧数据库，任何不认识 `012_multi_user_ownership` 的旧 Server 都不得打开已升级数据库进入服务状态。

### 测试步骤

1. [FRAME] 用旧版 Server 在隔离目录创建多个现有管理员、API Key、多个 Client、`server_expose` 和 `client_to_client` 隧道并产生流量。
2. [FRAME] 使用新 Server 完成 strict migration，确认所有旧用户成为管理员，旧资源归确定的 `legacy_owner_user_id`，Client 继续在线或重连。
3. [FRAME] 停止新 Server，使用上一版 Server 尝试打开同一隔离数据库。
4. [FRAME] 断言上一版 Server 因未知 strict migration 明确失败，未开始 HTTP/WebSocket 监听，也未写入数据库。
5. [FRAME] 再次启动新 Server，确认数据、所有权和运行意图未被旧 Server 尝试破坏。

### 通过标准

- [FRAME] 旧 Server 拒绝启动是唯一通过结果；能够忽略 migration 并继续服务属于 P0 发布阻塞。
- [FRAME] 同一套 migration E2E 还必须从空数据库启动，证明 compatible `011` 在 strict `012` 前提交，且两类 ledger 的最终记录与全局文件顺序一致。
- [FRAME] 产品、CLI 和文档不提供 downgrade、Down SQL、兼容写入或回滚承诺。

## 完成标准

- [FRAME] 管理员和普通用户使用同一个 `users` 表，管理员只由 `is_admin = 1` 标识并可动态升降；系统始终至少有一个正常管理员。
- [FRAME] 用户只有 active/disabled 持久化状态；删除是不可恢复的用户及全部相关数据物理删除，不存在 `deleted_at` 或到期概念。
- [FRAME] 所有现有资产归确定的 `legacy_owner_user_id`，所有新资产写入非空用户所有者。
- [FRAME] 迁移后旧 `admin_users`、`admin_sessions` 不存在，管理员安全表全部以外键关联 `users`。
- [FRAME] 普通用户无法观察或操作其他用户的 REST、SSE、Traffic 或 Activity 数据。
- [FRAME] 管理员代操作保留目标 Owner 和真实管理员 Actor。
- [FRAME] disable 会停止全部实际工作但保留资产和原始运行意图；enable 恢复 Client 和应运行隧道；delete 只接受 disabled 用户并清除全部数据。
- [FRAME] Client 控制和数据协议不增加用户字段，跨版本认证失败行为有真实测试证据。
- [FRAME] Server 的所有构造路径在认证 Store 或 Owner 不可用时 fail closed，不存在 `unmanaged-*` Client。
- [FRAME] 管理员 Dashboard 以分页用户列表为入口，用户详情展示网络拓扑、客户端和隧道。
- [FRAME] 产品中没有通知模型，“活动日志”范围、Actor、普通保留策略和用户删除时彻底清除规则明确。
- [FRAME] Web 不包含 Server API 兼容 fallback。
- [FRAME] 多用户 migration 按全局版本顺序执行但写入 strict ledger，旧 Server 对已升级数据库强制拒绝启动。
- [FRAME] 相关 Go 测试、前端 lint/build、统一构建、直连/nginx/caddy E2E、Client 版本矩阵和严格 migration 测试全部完成并有记录。

## 主要代码地图

- [FRAME] `internal/server/migrations/`：用户目录、凭据、Session、所有权和范围字段。
- [FRAME] `internal/server/storage_schema.go`：migration 分类、全局版本顺序编排和旧 Server strict ledger 拒绝。
- [FRAME] `internal/storage/sqlite.go`：按全局顺序执行多 ledger migration，并在 ledger 写入前执行可选的事务内校验 Hook。
- [FRAME] `internal/server/admin_store.go`：初始化、管理员同步、Key、Client、Token 与旧数据导入边界。
- [FRAME] `internal/server/auth_middleware.go`：Principal、`user_sessions` 和管理员中间件。
- [FRAME] `internal/server/server_http.go`：新管理路由和现有资源路由权限。
- [FRAME] `internal/server/control_auth.go`：Key/Token 到用户所有者的解析和控制通道门禁。
- [FRAME] `internal/server/data.go`：数据通道最终门禁。
- [FRAME] `internal/server/session.go`：按用户关闭逻辑 Client 会话和运行态。
- [FRAME] `internal/server/control_loop.go`：legacy 隧道创建门禁。
- [FRAME] `internal/server/unified_tunnel_api.go`：隧道所有权与管理员代操作。
- [FRAME] `internal/server/unified_tunnel_reconcile.go`：用户状态运行门禁和恢复。
- [FRAME] `internal/server/traffic_store.go`：历史流量所有权快照。
- [FRAME] `internal/server/activity_store.go`、`internal/server/activity_api.go`、`internal/server/events.go`：活动日志 Scope 和 SSE 隔离。
- [FRAME] `pkg/protocol/message.go`、`internal/client/client.go`：认证错误兼容行为。
- [FRAME] `web/src/lib/router.ts`、`web/src/lib/auth.ts`、`web/src/lib/api.ts`：前端路由、主体和 API Scope。
- [FRAME] `web/src/hooks/`、`web/src/components/custom/dashboard/`：用户化 Query Key 与三个资源模块。
- [FRAME] `test/e2e/`：直连、反向代理、Client 跨版本、升级与严格 migration 拒绝降级验证。
