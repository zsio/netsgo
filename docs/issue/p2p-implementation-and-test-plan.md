# P2P 实施与测试计划

## 当前执行状态

- [KNOWN] 本计划的生产代码、capability-gated API/UI、Pion 数据闭环和主要自动化测试已经实现。
- [KNOWN] 当前自动化证据包括扩展 Pion vnet NAT/故障矩阵、两个独立 Linux network namespace 经各自 NAT 的 Docker E2E、真实 Client/Server 切换与重连 E2E、TCP/UDP/SOCKS5 精确 payload 统计、双向共享限速反向响应、nginx/caddy current-only peer-direct system E2E，以及 v0.1.8/current 11 项完整跨版本矩阵。
- [KNOWN] 双 NAT Docker E2E 还单独验证同一内网端通过 NAT 公网地址回到自身的 UDP hairpin，避免把 vnet 未实现的 hairpin 选项误写成覆盖。
- [KNOWN] 仍未完成的是无法由本地自动化替代的真实运营商 CGNAT/移动网络/企业防火墙部署矩阵、passive ICE-TCP 跨平台原型和真实高延迟网络 benchmark；这些边界不得被自动化 vnet 结果夸大为已验证。

## 目标与完成标准

- [KNOWN] 第一版仅为 `client_to_client` 增加 `server_relay_only`、`direct_preferred`、`direct_only` 三种数据传输策略，默认保持 `server_relay_only`。
- [KNOWN] TCP、UDP-over-stream、SOCKS5 必须共用一个 transport selector 和同一个 target 分发入口。
- [KNOWN] 第一版使用 Pion WebRTC/ICE/DTLS/SCTP/DataChannel；不使用 TURN，不引入 quic-go，不承诺严格 NAT 下的 TCP 打洞。
- [KNOWN] 每个应用 TCP、SOCKS5 CONNECT 或 UDP association 在创建时只选择一次 transport，存量连接不迁移，同一 payload 不双发。
- [KNOWN] 完成标准不仅是直连可建立：授权租约、撤销、统计、共享限速、Web 状态、回退、兼容和 E2E 全部通过后，才能开启生产 capability 和 API/UI direct 策略门禁。

## 实现决策

- [INFERRED] 第一版在一个 negotiated、可靠、有序的 detached Pion DataChannel 上承载现有 yamux。理由是它能直接复用现有逻辑 stream 的背压、并发、关闭和 TCP/UDP/SOCKS5 分发语义；每个应用连接创建 DataChannel 会把 SCTP channel 生命周期扩散到各 endpoint handler，并增加协商与资源上限复杂度。
- [INFERRED] direct yamux 两端使用规范化 Client pair 决定固定 client/server 角色，避免双方同时创建不同 session。PeerConnection 仍按 Client pair 共享。
- [INFERRED] STUN 采用显式配置且默认允许空列表；空列表仍可使用 host candidate 完成同 LAN、直接公网地址和可达 IPv6 直连。部署接口及是否增加 NetsGo 自托管 STUN listener 在检查现有 CLI/config 后确定，不能静默依赖公共服务。
- [INFERRED] Passive ICE-TCP 独立做跨平台原型，未通过前不广告为 capability，也不阻塞 UDP 直连第一版。
- [INFERRED] 共享带宽新增显式 `total_bps`，新 direct 策略只接受该共享语义。旧 `ingress_bps`/`egress_bps` 继续供旧 Client 和旧 relay 配置使用；迁移、projection 和 relay 同语义实现必须用兼容测试固定后再开放 direct。

## 分阶段编码计划

1. [INFERRED] 建立 transport-neutral stream 接口、relay adapter 和 selector；让现有三种 client-to-client endpoint 全部走 selector，保持 relay 行为与测试不变。
2. [INFERRED] 在 `pkg/protocol/` 增加有大小上限的 P2P 信令、pair lease、tunnel grant、撤销、状态、流量和 credit 消息；所有消息绑定 pair session、generation、tunnel revision、角色、序号和过期时间。
3. [INFERRED] 在 Server 增加按规范化 Client pair 管理的协调器：能力门禁、offer/answer/candidate 转发、双方 ready 汇聚、20 秒续租、60 秒硬过期、退避、撤销和运行态投影。
4. [INFERRED] 在 Client 增加 Pion peer manager：每 pair 一个 PeerConnection、detached DataChannel + yamux、身份/fingerprint 校验、过期清理、断 control/data 立即关闭、按 grant 接收/open stream。
5. [INFERRED] 接入 selector：`direct_preferred` 未就绪立即 relay，ready 后仅新 stream direct；`direct_only` 使用有界等待；direct 失败关闭 direct stream，后续按 policy 处理。
6. [INFERRED] 增加 owner-only 累计 direct 流量报告和 Server 幂等入账，确保 payload-byte 口径及 transport 分桶正确。
7. [INFERRED] 增加 tunnel-wide `total_bps`、relay 迁移语义和按 tunnel 隔离的 sender-credit 调度；单向可借满，双向活跃趋向平分，新活跃方向在下一调度轮参与。
8. [INFERRED] 完成 Web 策略编辑、readiness、下一新连接 transport、active transports 和失败/重试展示。
9. [INFERRED] 完成真实网络 E2E、反代、兼容、竞态、资源上限和构建验证后，最后开启 `P2P.Supported=true` 及 direct API/UI。

## 单元与集成测试计划（先于对应实现编写）

- [KNOWN] Selector 表驱动测试覆盖三种 policy、ready/not-ready、等待超时、direct open 失败、relay open 失败、每 stream 固定和禁止双 open。
- [KNOWN] Transport-neutral target dispatcher 测试使用 `net.Pipe`/fake stream 覆盖合法 header、错误 tunnel/revision/role/grant、TCP、UDP framing、SOCKS5 元数据和关闭传播。
- [KNOWN] 协议 JSON/validation/fuzz 测试覆盖未知字段、空 ID、超长 SDP/candidate、过期、错误 generation/revision/role、序号回退和消息数量限制。
- [KNOWN] Pair registry 测试覆盖 pair 规范化、多 tunnel 共用、双方角色相反、单方 ready 不算成功、重复/陈旧信令、退避抖动、并发 reconcile 和无 tunnel 清理。
- [KNOWN] Lease/grant 使用 fake clock 测试立即撤销、60 秒硬过期、旧续期重放不延寿、单 tunnel 撤销不关闭同 pair 其他 tunnel、control/data 任一失效停止续租。
- [KNOWN] DataChannel stream adapter 测试覆盖大 payload 分片、短读写、背压、并发 close、remote close、yamux 多 stream、公平性和容量上限。
- [KNOWN] 流量测试覆盖 owner-only、累计计数幂等、断线重报、epoch 变化、双 transport 同桶、UDP 原始 payload 口径和非 owner 报告拒绝。
- [KNOWN] Credit scheduler 使用 fake clock 和 benchmark 覆盖不限速、低速、高速、单向借满、双向长期公平、反向突发最大等待、多 stream、过期/重复 credit 和 tunnel 间隔离。
- [KNOWN] Web 测试覆盖默认 relay、三策略请求、能力禁用、readiness 文案、fallback、混合 active transports 和 SSE 更新。
- [KNOWN] 兼容测试覆盖 old client/current server 与 current client/old server：不支持 P2P 时不发送信令、不选择 direct、relay 不回归。

## E2E 测试计划

- [KNOWN] 同一 Docker network 两 Client：TCP、UDP、SOCKS5 分别验证 direct payload、双向内容一致和 Server relay byte 不增长。
- [KNOWN] `direct_preferred`：P2P 被阻断时立即 relay；恢复后新连接 direct、旧 relay 连接不中断且不迁移。
- [KNOWN] `direct_only`：P2P 不可用时有界失败，不偷偷 relay；恢复后新连接成功。
- [KNOWN] 单路径：为 payload 注入唯一序列，验证任何序列不会同时出现在 relay 与 direct 计量中。
- [KNOWN] 生命周期：删除、禁用、revision 修改、Client 断 control、断 data、Server 重启；验证立即关闭正常路径和消息阻断时不超过 60 秒。
- [KNOWN] NAT：显式 STUN 下验证 server-reflexive UDP；UDP 阻断时验证失败/回退语义。Passive ICE-TCP 仅在原型环境满足外部可达 listener 时单列测试。
- [KNOWN] nginx、caddy、Server 直连三条控制/relay 路径均验证信令和 fallback；UDP P2P 不要求反代转发业务 payload。
- [KNOWN] 资源与稳定性：128 条并发 grant 会合并为一个 pair session；24 条并发 stream、持续低速分块传输、约一分钟带抖动退避和 race detector 分别有自动化覆盖。真实数小时 soak 仍属于发布前独立运行项，不能由短时单测替代。

## 验证命令与门禁

- [KNOWN] 每阶段先运行新增测试及受影响 Go package；协议、会话、数据通道改动后运行 `go test ./...` 和相关 `-race`。
- [KNOWN] Web 改动运行 `bun run lint`、`bun run test`（若现有脚本提供）和 `bun run build`。
- [KNOWN] 数据通道和反代改动运行 system E2E 的 Server 直连、nginx、caddy 变体及新增 P2P E2E。
- [KNOWN] 最终运行 `make build`、`go vet ./...`、`go test ./...`、`make test-race` 和与 CI 相同的兼容/E2E 集合。
- [KNOWN] 当前 P2P capability、API 和 UI direct 门禁已经开放，但只对双方明确广告匹配 `webrtc_ice` 实现的 `client_to_client` 开放；默认策略仍为 `server_relay_only`，旧 Client、能力未知 Client 和跨版本兼容矩阵继续使用 relay。
