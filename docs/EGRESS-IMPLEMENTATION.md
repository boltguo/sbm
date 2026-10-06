# 多 WireGuard 出口实现与验证记录

本次扩展基于 `main` 的 `ef33404`（SBM 2.0.2），保留 ConfigVersion=4、StateVersion=1、官方 sing-box 1.13.14。先阅读当前代码及 `566a2bb`、`f38b6dd`、`ef4c78f`、`0d31f2f` 的旧实现，再重新设计多出口模型；没有 cherry-pick 单出口实现。

## 1. 架构

```text
Client → VLESS Reality / Hysteria2 → A / SBM
                                      ├─ Direct → Internet（A IP）
                                      ├─ userspace WireGuard → B1 → Internet（B1 IP）
                                      └─ userspace WireGuard → B2 → Internet（B2 IP）
```

一个入口、一个管理员、一个 sing-box。每个入口协议保留 Direct 凭据，并为每个 Gateway 保存一组独立凭据；同域名、同协议端口，以稳定 `auth_user` 选择出口。每个 Gateway 对应独立的 userspace endpoint，A 不安装系统 WireGuard 接口。

不配置或全部停用 Gateway 时，核心只渲染原有 Direct 配置。订阅仍使用一个 URL，先输出 Direct，再按 Gateway 的 `position` 分组，位置相同时保持保存顺序。Profile Title 继续使用入口名称。概览页只显示已启用的 Gateway；全部关闭时隐藏整个中继出口区域。

中继作为现有二进制中的可选扩展：启用只增加独立用户、路由和 endpoint，Direct 凭据、监听端口及地址族策略继续保留。关闭状态的出口草稿可以创建、编辑和删除，保存凭据而不重启核心。出口不可达或计数不可用不触发全局停机。

## 2. 修改文件

下列是源文件清单；构建产物另列，避免把压缩 bundle 当作可维护源码。

| 范围 | 新增或修改 |
| --- | --- |
| 模型与存储 | `internal/model/model.go`、`internal/model/egress.go`、`internal/store/config.go`、`internal/store/config_test.go` |
| 凭据、名称、密钥 | `internal/protocol/driver.go`、`internal/protocol/egress.go`、`internal/protocol/wireguard.go`、`internal/protocol/egress_test.go` |
| Geo | `internal/geo/lookup.go`、`internal/geo/lookup_test.go` |
| 核心渲染和事务 | `internal/core/render.go`、`internal/core/manager.go`、`internal/core/manager_test.go`、`internal/core/egress_test.go` |
| 流量计数 | `internal/traffic/tracker.go`、`internal/traffic/accounting.go`、`internal/traffic/accounting_test.go`、`internal/traffic/egress.go`、`internal/traffic/egress_test.go` |
| 流量历史 | `internal/traffic/history.go`、`internal/traffic/history_test.go`、`internal/server/traffic_history.go`、`internal/server/traffic_history_test.go`、`web/src/components/TrafficHistory.vue`、`web/src/history-i18n.ts` |
| API、订阅与启动 | `internal/server/server.go`、`internal/server/egress.go`、`internal/server/egress_test.go`、`internal/server/egress_runtime_test.go`、`cmd/sbm-panel/main.go` |
| UI | `web/src/App.vue`、`web/src/types.ts`、`web/src/i18n.ts`、`web/src/egress-i18n.ts`、`web/src/style.css`、`web/src/components/EgressGuide.vue`、`web/src/views/EgressView.vue`、`web/src/views/ProtocolsView.vue`、`web/src/views/DashboardView.vue` |
| 验证与 CI | `scripts/egress-linux-test.sh`、`scripts/sing-box-integration.sh`、`scripts/frontend-style-unit.sh`、`web/scripts/check-i18n.mjs`、`.github/workflows/build.yml` |
| 安装与文档 | `install.sh`、`README.md`、`README.zh-CN.md`、`docs/WIREGUARD-EXIT.md`、`docs/WIREGUARD-EXIT.en.md`、本文 |

`internal/webembed/dist/index.html` 及其 CSS/JS assets 由 `npm run build` 重建并随源码提交。旧 hash 的 assets 被替换，CI 从干净 checkout 重建后检查该目录是否产生差异。

## 3. 数据模型与 API

业务配置新增可省略字段：

```go
Config.EgressGateways []EgressGateway
Inbound.EgressCredentials []EgressCredential
```

`EgressGateway` 包含 `ID`、`Enabled`、`Marker`、`Position`、B 的 IPv4/UDP 端口、A 私钥、B 公钥、持久化 `TunnelSlot`、缓存 `Geo`、`LocationOverride`、独立 `TrafficQuota` 和 `Reset`。

`GatewayGeo` 保存 IP、国家代码、国家、地区、城市及更新时间。`EgressCredential` 使用 `GatewayID` 关联；VLESS 只保存 UUID，HY2 只保存 password。UUID/password 由服务器生成，修改入站时保留服务器持有的衍生凭据。校验阻止重复 ID、地址槽、认证用户、同入站凭据及 IPv4+UDP 端口组合。

State 的 `egress` map 以 Gateway ID 为键，保存独立 TX/RX 累计、基线、每方向规则代次、peer、初始化状态、采样健康和周期。`egressAccountingPending` 记录需要重试的规则协调，包括最后一个出口删除后的清理。

| API | 行为 |
| --- | --- |
| `GET /api/egress` | 按显示顺序返回 Gateway、A 公钥、双方地址和独立用量 |
| `POST /api/egress` | 新建，服务器分配 ID 和稳定地址槽 |
| `PUT /api/egress/{id}` | 编辑或启停，保留 ID、地址槽及衍生凭据 |
| `DELETE /api/egress/{id}` | 删除出口并清理关联凭据、状态和自有计数规则 |
| `POST /api/egress/{id}/geo` | 短超时重新检测指定 IPv4 位置 |
| `POST /api/egress/{id}/reset` | 重置该出口的周期用量，不清空内核 counter |
| `POST /api/egress/keypair` | 独立生成 A 的 WireGuard 密钥对 |
| `POST /api/egress/public-key` | 严格校验 A 私钥并派生公钥 |
| `GET /api/traffic/history` | 查询入口全局代理流量的每日记录或每月汇总，结束日期包含在内 |

以上管理 API 复用原有登录、CSRF 和 Web 管理锁。私钥输入沿用 PasswordInput；错误不返回完整核心配置、敏感命令输出或密钥。

## 4. sing-box 配置示例

以下为生成配置的出口相关片段，保密字段已替换，入站 TLS/Reality 等原配置省略。实际测试使用完整配置与随机密钥。

```json
{
  "dns": {"servers": [{"type": "local", "tag": "local"}]},
  "endpoints": [
    {
      "type": "wireguard", "tag": "egress-wg-aws", "system": false,
      "mtu": 1408, "address": ["10.66.1.2/32"],
      "private_key": "REDACTED_A1_PRIVATE_KEY",
      "peers": [{"address": "203.0.113.1", "port": 51820,
        "public_key": "REDACTED_B1_PUBLIC_KEY", "allowed_ips": ["0.0.0.0/0"],
        "persistent_keepalive_interval": 25}]
    },
    {
      "type": "wireguard", "tag": "egress-wg-jp", "system": false,
      "mtu": 1408, "address": ["10.66.2.2/32"],
      "private_key": "REDACTED_A2_PRIVATE_KEY",
      "peers": [{"address": "203.0.113.2", "port": 51820,
        "public_key": "REDACTED_B2_PUBLIC_KEY", "allowed_ips": ["0.0.0.0/0"],
        "persistent_keepalive_interval": 25}]
    }
  ],
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {
    "rules": [
      {"auth_user": ["egress-aws-vless", "egress-aws-hy2"],
        "action": "resolve", "server": "local", "strategy": "ipv4_only"},
      {"auth_user": ["egress-aws-vless", "egress-aws-hy2"],
        "action": "route", "outbound": "egress-wg-aws"},
      {"auth_user": ["egress-jp-vless", "egress-jp-hy2"],
        "action": "resolve", "server": "local", "strategy": "ipv4_only"},
      {"auth_user": ["egress-jp-vless", "egress-jp-hy2"],
        "action": "route", "outbound": "egress-wg-jp"}
    ],
    "final": "direct"
  }
}
```

VLESS users 为 `direct-<inboundID>` 和 `egress-<gatewayID>-<inboundID>`，每人独立 UUID；HY2 使用相同命名和独立 password。Direct 的地址族策略保留在自己的 `domain_resolver` 上，Gateway resolve rule 只匹配衍生用户。每个地址槽 X 在 A 使用 `10.66.X.2/32`，在 B 使用 `10.66.X.1/24`，删除其他出口不会重新编号。

## 5. 流量统计原理

A 的 `SBM_EGRESS_TX` / `SBM_EGRESS_RX` filter chain 仅使用无 target 的计数规则。按 B IPv4+UDP 端口匹配 TX 的 destination/dport 和 RX 的 source/sport；带自有 comment 的 jump 返回原 INPUT/OUTPUT 流程。不会 flush 用户链或改变 ACCEPT/DROP 策略；保留自有链中的陌生 verdict 时拒绝协调，避免激活未知规则。

每次协调保证 INPUT/OUTPUT 各有一个位于首位的自有 jump；防火墙 reload 把它移到 terminal verdict 后面或产生重复 jump 时，只修复自有入口，保留计数规则的累计和代次。全部关闭并清理成功后停止调用 iptables；关闭出口的月度周期仍推进，失败的最后一次清理仍会重试。

读取 `iptables-save -c -t filter` 原始字节 counter，不解析 locale 展示文本。每方向保存 boot ID + 随机规则代次，处理 reboot、规则重建和 counter 回退。改 peer 后重新基线但保留周期累计；删除只移除该出口的自有规则。5 秒采样、30 秒持久化，手动和月度重置更新基线而不清空内核计数。

出口采样使用独立循环。等待 iptables 或配置事务不会阻塞原有 1 秒核心采样及全局配额检查；正常关闭会等待采样循环退出再保存状态。入口全局用量继续统计该入口服务的全部代理流量，出口用量独立估算 B 的套餐，两者不相加。

```text
TunnelBytes = TX + RX
EstimatedProviderUsage = TunnelBytes × factor
single: factor=1; bidirectional: factor=2
```

套餐复用 GB/GiB、计费方式、预留比例及月度日期/时区。Gateway 只统计和预警，永不因单个 Gateway 达限调用全局 Stop。本机全局配额及其停机/恢复逻辑继续独立运行。

后来加入的流量历史使用 SQLite：默认保存于 state 同目录的 `traffic.db`，JSON 配置与 v1 state 兼容副本继续保留。每秒采样核心代理流量，每 30 秒把每日增量、完整全局／Gateway 计数基线以及最后成功采样时间放在同一个事务中提交；重置与正常退出也保存。重启以数据库 checkpoint 为准，JSON 兼容副本失败后重试不会重复提交已保存的历史增量。备份和恢复需把数据库一并处理。

历史记录的是入口代理流量，Gateway TX/RX 独立保存周期用量，两者不相加。每日归属使用数据库首次创建时固定的时区，每月按日历月汇总；修改计费方式不会重算旧历史。旧版本仅有周期累计，首次升级保留为导入汇总，不伪造每日明细；整个汇总在同一月时才加入该月。采样中断跨天时标注未完整记录，即使期间重置了套餐并重启也保留真实采样时间。重置任何一个 Gateway 不清除全局历史或其他 Gateway 用量。

## 6. 命名与凭据生命周期

优先手动位置，其次与当前 IP 一致的缓存 Geo；自动命名沿用安装器的两位大写国家代码与去空白城市，例如 `SG-Singapore`。

```text
<位置>-VLESS/HY2-<可选 Marker>
US-Boardman-VLESS-AWS
JP-Tokyo-HY2-JP1
JP-Tokyo-VLESS          # Marker 为空
VLESS-AWS               # 无位置，有 Marker
Gateway-<ID>-HY2        # 位置、Marker 均为空
```

Geo 查询仅发生在创建、修改 IP 和主动重检，最多 3 秒。失败仍可保存；换 IP 后不沿用旧 IP 的位置，同 IP 重检失败保留上次结果。热路径不查外部 Geo API。

Gateway 禁用时从 core、衍生卡片和订阅隐藏，UUID/password 保留；重新启用、改名、重排及修改套餐不会旋转凭据。删除才清理。衍生链接保留原入站 Reality/TLS/obfs 参数，仅替换凭据和显示名。

“中继出口”页面提供可展开的五步文档教程，覆盖 Ubuntu/Debian 安装、B 密钥生成、面板字段对应关系、B forwarding/NAT/服务启动和出口 IP 验证。教程只展示步骤与命令示例；公钥、地址槽 X、UDP 端口及网卡按文档说明手动替换。B 私钥保存在 B 的文件中，示例通过 PostUp 读取。教程的接口与服务名为 `sbm-egress`，与手动文档中的 `wg0` 示例择一使用。

## 7. 安装与升级兼容

保持 v4 config / v1 state，新增字段省略时默认没有 Gateway。现有 2.0.2 v4 实例可以直接加载，不恢复 1.x migration。安装器增加 `iptables-save` 依赖检查，A 无需安装 wireguard 包。官方核心继续使用 1.13.14。

新代码尚未发布为正式 Release；当前发布的 2.0.2 包不含该功能。使用源码构建的 Linux amd64/arm64 包部署，升级前备份业务配置、state 和历史数据库。含新字段的配置不保证由旧 2.0.2 二进制读取；降级应恢复原备份。

安装器备份短暂暂停面板，等待数据库关闭后打包，核心继续转发；打包失败仍恢复面板。恢复前必须成功读取归档并停止面板／核心；停止或解包失败不会继续启动混合状态。旧备份没有 `traffic.db` 时，先把现有数据库另存，再从恢复的 JSON 初始化；新备份恢复其数据库 checkpoint。

配置事务继续捕获流量、验证、持久化候选、渲染、执行官方 check、重启及验证 active，失败恢复原配置。apply/rollback 使用独立于 HTTP request cancellation 的超时上下文。位置、顺序、套餐及关闭出口的草稿不改变 core 内容时跳过重启；原有 Direct 入站编辑的核心恢复行为保留。

B 的安装、公钥交换、forwarding、NAT、云防火墙和排障步骤见 [中文指南](WIREGUARD-EXIT.md) / [English guide](WIREGUARD-EXIT.en.md)。

## 8. 验证结果（2026-10-06）

| 验证 | 结果 |
| --- | --- |
| `go test ./...` | 通过 |
| `go test -race ./...` | 通过 |
| `go vet ./...`、gofmt、`git diff --check` | 通过 |
| `npm run build` | Vue 类型检查与 Vite 构建通过 |
| 中英文类型、API、静态 i18n 引用及占位符 | 82 个中继及 22 个历史文案键校验通过；检查覆盖页面与组件 |
| 安装测试、bash syntax、shellcheck | 通过 |
| 官方 sing-box 1.13.14 `check` | 0/1/2/3 Gateway × 5 个 Direct 地址族策略，共 20 个组合通过 |
| 对比原始 `main` 的 Direct 配置 | 独立构建 `ef33404` 与当前二进制；五种地址族策略的无 Gateway 配置逐字节相同 |
| 可选扩展回归 | 草稿不重启、全部关闭停止内核采样、阻塞采样隔离、关闭周期推进、删除清理重试及 state 重启恢复通过 |
| 历史持久化与失败恢复 | 升级导入、跨天／跨月、计费方式变更、套餐重置、核心重启、并发读写、事务失败重试、JSON 副本失败重试及 SQLite 与 Gateway checkpoint 独立恢复通过 |
| Linux 原生 iptables accounting | 两个 peer 独立 TX/RX、入口顺序与去重修复、规则重建、删除通过；原用户 ACCEPT/DROP 规则保留 |
| 真实 VLESS/HY2 运行 | Direct、Gateway 1、Gateway 2 的六条路径全部通过；实际核心代理计数与历史一致，SQLite 重开后保留两个出口的 counter 和基线 |
| 单出口故障隔离 | 停止 Gateway 1 后，其 VLESS/HY2 请求失败，Direct 与 Gateway 2 的四条路径继续可用，无 Direct 回落 |
| 前端浏览器检查 | 中英文页面、编辑保存、隐藏私钥、只读衍生节点及 QR；历史切换等待期间保留表格与面板高度，全部控件高 44px，刷新按钮宽 44px；320/390px 历史页面无整体横向溢出；统计时区月末选择、输入校验与错误恢复通过 |
| release smoke | Linux amd64/arm64 打包、checksum 校验通过 |

Linux 测试经授权通过 SSH alias `anya` 执行，所有网络操作位于新 network namespace。B1/B2 是两个真实 sing-box userspace WireGuard peer 进程，目标 HTTP 服务观察到 Direct 为 `127.0.0.1`、Gateway 1 为 `203.0.113.1`、Gateway 2 为 `203.0.113.2`。每个 Gateway 观察到独立 TX=1916、RX=1616 字节。加入历史后的复测中，核心代理计数为上传 594、下载 762 字节，每日记录与其一致；关闭并重开 SQLite 后两个出口的累计与基线保持相同。测试使用隔离地址和随机凭据，没有替换宿主服务或修改宿主防火墙。真实云 B 的 kernel WireGuard/NAT 需要部署后验证。

可复现的官方核心检查与 Linux 集成测试已加入 CI。`scripts/sing-box-integration.sh` 下载并核验 GitHub SHA-256 后运行；`scripts/egress-linux-test.sh` 在新 namespace 内执行测试。普通 Go 测试默认跳过需要 root/Linux/官方二进制的集成项。

## 9. 当前限制

- Gateway 第一版只支持公网 IPv4 和 IPv4 出口；客户端直接传来 IPv6 目的地址时不能经这些出口访问。
- 固定地址池最多 254 个 Gateway；系统仍定位个人单入口实例。
- 主机计数包含隧道封装、握手、keepalive 和其他程序向同一 peer 地址端口发的流量，不能等同云账单；INPUT 计数也可能包含最终被防火墙丢弃的数据包。
- 启动、采样中断、规则重建、防火墙 reload 后到入口修复之前、周期边界附近可能少计；跨重置边界的离线累计无法精确拆到两个周期，恢复后的首个样本作为新周期基线。异常退出可能丢失最近 30 秒未持久化的数据。
- 依赖 root/CAP_NET_ADMIN 及主机当前活跃的 iptables backend；统计不可用时保留最后用量并显示中断，代理可继续运行。
- 尚未完成真实 AWS/GCP/普通 VPS B 的 kernel WireGuard、NAT 和云防火墙联网验证，也未做长期压力与故障注入测试。

## 10. 后续增强

优先补充真实两台云 B 的部署验证、长时间采样与防火墙 reload 测试，再考虑每 Gateway 独立暂停、握手/连通性状态、B 配置导出、IPv6、多后端计数及账单对账。任何 Gateway hard limit 都应只暂停对应出口，不停止全局 sing-box。
