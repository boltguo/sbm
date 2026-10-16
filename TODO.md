# 单人自用面板后续演进待办 (Roadmap & TODO)

定位原则：**保持极简、克制、免维护的单人自用体验**，拒绝多用户商业面板的冗余复杂度，仅围绕「单人日常使用、双机解锁、换机迁移、多端订阅、安全防超额」沉淀后续按需实现的能力。

---

## 1. 按应用/域名自动分流到指定出口网关 B（App-based Egress Routing）

- **痛点与场景**：
  - 目前已支持「本机直出 + WireGuard 出口网关 B（双链路分流）」和「应用与站点流量识别（`app_rules.json`）」，但哪些流量走 A 直出、哪些走 B 出口仍需在客户端维护规则或手动切换节点。
  - 当 VPS A 速度极佳但 IP 被 OpenAI / Claude / Gemini / Netflix 封锁或风控时，希望只在面板勾选对应应用，服务端直接将特定应用送往解锁机 B，其余流量走 A 本机直出，所有客户端零配置生效。
- **实现思路**：
  - 复用 [`internal/traffic/app_rules.json`](internal/traffic/app_rules.json) 已有的应用分类与域名规则库（`domains`、`suffixes`、`keywords`）。
  - 在「出口（Egress）」配置页增加「按应用分流到出口 B」多选开关（如 `OpenAI / ChatGPT`、`Claude`、`Google Gemini`、`Netflix`、`YouTube`、`TikTok` 等，以及自定义追加域名后缀）。
  - 在 [`internal/singbox/`](internal/singbox/) 渲染 `sing-box` 配置时，自动生成对应的 `route.rules` 规则，匹配目标域名并路由至 `wg-exit-out` 出站。

---

## 2. 配置一键备份与恢复（Backup & Restore / 无感换机迁移）

- **痛点与场景**：
  - 单人自用最烦的是 VPS 到期、IP 被封或切换云厂商（AWS Lightsail / 搬瓦工 / DMIT）后，重新安装面板会导致 UUID、Reality 密钥、HY2 密码、订阅 Token 全部重新生成，手机、电脑、平板、路由器上的所有客户端都要重新扫码或更新订阅。
- **实现思路**：
  - 在「配置」页提供 **「导出迁移包（JSON）」** 与 **「导入恢复」** 按钮。
  - 导出内容包含：
    - 全部节点核心凭证（VLESS UUID、Reality Private/Public Key & ShortID & SNI、Hysteria2 密码与端口跳跃范围、SOCKS5 账号密码、Shadowsocks 密码）。
    - 订阅 Token（`/sub/<token>` 保持不变，换机后只需把域名解析切到新 IP，客户端刷新订阅即可无缝切换）。
    - WireGuard 出口网关密钥对与对端配置、流量配额与重置时区设置。
  - 可选支持附带导出/导入历史流量统计快照。

---

## 3. 多端订阅格式自适应（Native Clash Meta YAML & Sing-box JSON）

- **痛点与场景**：
  - 目前 `/sub/<token>` 返回标准 Base64 节点 URI 列表，适合 Shadowrocket / v2rayNG / NekoBox，但在 Clash Verge Rev / Mihomo (Clash Meta) / Stash / 软路由 OpenClash 或原生 Sing-box 客户端中，直接粘贴往往需要借助第三方 Subconverter 转换（存在隐私泄露风险且经常不支持最新 Reality / Hysteria2 参数）。
- **实现思路**：
  - 在订阅处理逻辑中识别请求 `User-Agent`（如 `clash-verge`、`mihomo`、`Stash`、`sing-box`、`SFA`、`SFM`）或显式查询参数：
    - `/sub/<token>?target=v2ray`（默认 Base64 URI）
    - `/sub/<token>?target=clash`（本地直接渲染带自动测速组、AI/流媒体分组的 Clash Meta / Mihomo YAML）
    - `/sub/<token>?target=singbox`（本地直接渲染客户端即插即用的 Sing-box JSON）
  - 面板「订阅」弹窗可提供一键复制不同客户端专属订阅链接的快捷按钮。

---

## 4. 轻量被动告警通知（Webhook / Bark / Telegram 配额与停机提醒）

- **痛点与场景**：
  - 单人面板平时几乎不会主动打开看，只有突然断网时才会排查是节点挂了还是流量超额触发了安全停机。
- **实现思路**：
  - 在「配置」页增加一个极简的可选通知 URL（支持 iOS `Bark` 推送 URL、Telegram Bot API URL 或通用 Webhook）。
  - 仅在关键生命周期事件触发一次推送：
    1. 月度流量达到 **80% / 90% 预警线**。
    2. 达到 100% 触发 **超额安全停机（Quota Shutdown）**。
    3. 新计费周期 **月度流量自动重置并恢复代理服务**。
    4. WireGuard 出口网关 B 连续多次握手超时离线（可选）。

