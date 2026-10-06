# 多 WireGuard 中继出口

[English](WIREGUARD-EXIT.en.md) | 简体中文

SBM 管理一台入口 VPS、一个 sing-box 和一个管理员。可选的 Gateway 层让客户端在同一个总订阅中选择最终出口。

```text
Client → VLESS / Hysteria2 → A / SBM Entry
                              ├─ Direct → Internet（A IP）
                              ├─ WireGuard → B / AWS → Internet（AWS IP）
                              ├─ WireGuard → B / JP  → Internet（JP IP）
                              └─ WireGuard → B / SG  → Internet（SG IP）
```

A 使用 sing-box **1.13.14 的 userspace WireGuard endpoint**，不安装系统 WireGuard 接口。B 可以使用普通 Linux WireGuard。Direct 与各 Gateway 共用入口域名、VLESS TCP 端口和 HY2 UDP 端口；每个出口使用独立 UUID 或 password。

Direct 节点从 A 出网，Gateway 节点通过 WireGuard 从 B 出网。

## 在 A 添加 Gateway

打开“中继出口”页面：

1. 新增 Gateway，暂时保持停用。
2. 填写 B 的固定公网 IPv4 和 UDP 端口（默认 51820）。不能填写域名或 IPv6。
3. 生成 A 端密钥，保存 A 私钥；复制 **A 公钥**给 B。A 私钥由隐藏输入框保护。
4. B 公钥可以稍后补填。先保存停用的草稿，获得固定地址，例如：

   ```text
   A tunnel address: 10.66.1.2/32
   B Interface address: 10.66.1.1/24
   ```

5. 按下文配置 B，再填写 B 公钥、启用并保存。
6. 刷新客户端总订阅，选择对应 Gateway 节点。

每个 Gateway 单独生成 A 密钥。私钥只留在所属服务器；公钥交给对端。

| A 面板 | B 配置 |
| --- | --- |
| A private key：留在 A | 不需要 A 私钥 |
| A public key：复制到 B | `[Peer] PublicKey` |
| B public key：填写在 A | 从 B private key 派生 |
| A tunnel address：10.66.X.2/32 | `[Peer] AllowedIPs = 10.66.X.2/32` |
| B Interface address：10.66.X.1/24 | `[Interface] Address = 10.66.X.1/24` |

支持 254 个固定地址槽，每个 Gateway 的隧道地址独立保存。

**每个 B 必须使用该 Gateway 卡片显示的 X，不能把所有 B 都照抄成 10.66.1.1。**

## 配置 B（Debian / Ubuntu）

以下命令在 **B** 上执行。使用实际值替换 `X`、`B_PUBLIC_INTERFACE`、`A_PUBLIC_KEY`、`A_PUBLIC_IPV4`。不要把 A 或 B 的私钥放进聊天、日志或公开文档。

安装工具、生成 B 密钥：

```bash
sudo apt update
sudo apt install -y wireguard iptables iproute2
sudo install -d -m 700 /etc/wireguard
sudo sh -c 'umask 077; test -s /etc/wireguard/b-private.key || wg genkey > /etc/wireguard/b-private.key; wg pubkey < /etc/wireguard/b-private.key > /etc/wireguard/b-public.key'
sudo cat /etc/wireguard/b-public.key
```

将显示的 B 公钥填入 A。执行 `ip route show default`，找出 `dev` 后的公网网卡，例如 `ens5`、`ens4` 或 `eth0`。

开启 IPv4 forwarding：

```bash
echo 'net.ipv4.ip_forward=1' | sudo tee /etc/sysctl.d/70-wireguard-routing.conf
sudo sysctl -p /etc/sysctl.d/70-wireguard-routing.conf
```

创建 `/etc/wireguard/wg0.conf`，例如第一个 Gateway 的 X=1：

```ini
[Interface]
Address = 10.66.X.1/24
ListenPort = 51820
MTU = 1408
PostUp = wg set %i private-key /etc/wireguard/b-private.key
PostUp = iptables -A FORWARD -i %i -o B_PUBLIC_INTERFACE -j ACCEPT
PostUp = iptables -A FORWARD -i B_PUBLIC_INTERFACE -o %i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
PostUp = iptables -t nat -A POSTROUTING -s 10.66.X.0/24 -o B_PUBLIC_INTERFACE -j MASQUERADE
PostDown = iptables -D FORWARD -i %i -o B_PUBLIC_INTERFACE -j ACCEPT
PostDown = iptables -D FORWARD -i B_PUBLIC_INTERFACE -o %i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
PostDown = iptables -t nat -D POSTROUTING -s 10.66.X.0/24 -o B_PUBLIC_INTERFACE -j MASQUERADE

[Peer]
PublicKey = A_PUBLIC_KEY
AllowedIPs = 10.66.X.2/32
```

以上是普通 iptables 主机的示例。已有默认 DROP、UFW/firewalld 或自定义链时，应把 FORWARD/NAT 放行集成进现有策略；追加在早先的 DROP 之后不会生效。不要 flush 现有防火墙。

```bash
sudo chmod 600 /etc/wireguard/wg0.conf /etc/wireguard/b-private.key
sudo systemctl enable --now wg-quick@wg0
sudo wg show
```

云防火墙与 B 的主机防火墙都需要允许 **来自 A_PUBLIC_IPV4/32 的 UDP/51820**。UFW 示例：

```bash
sudo ufw allow from A_PUBLIC_IPV4 to any port 51820 proto udp
sudo ufw route allow in on wg0 out on B_PUBLIC_INTERFACE
```

NAT 规则仍需按实际网络部署持久化。各 Gateway 共用 A 的 VLESS/HY2 入站端口。A 的出站策略必须允许到各 B 的 UDP，B 的出站策略必须允许访问互联网。

## 云平台差异

| B 平台 | 检查事项 |
| --- | --- |
| AWS EC2 | 绑定 Elastic IP；Security Group 放行 A 的 UDP；自定义 NACL 同时允许回应流量。 |
| AWS Lightsail | 绑定 Static IP；在 IPv4 Firewall 放行 A 的 UDP；注意实例与防火墙实际关联。 |
| GCP | 使用静态外部 IPv4；VPC 防火墙规则正确匹配 VM；需要作为 VPC 路由下一跳时检查 IP forwarding。 |
| 普通 VPS | 确认 IPv4 固定、商家防火墙及主机防火墙允许 UDP、系统支持 WireGuard。 |

云 IP 变化后必须更新 A 的 Gateway IPv4。A 到 B 的延迟叠加到目标连接；静态 IP 和低丢包路径通常比地理名称更影响稳定性。住宅公网地址需要正确的 UDP 端口转发；CGNAT 无法直接充当可达 B。

## 节点名称与位置

Direct 节点名称例如 `US-LosAngeles-VLESS`、`US-LosAngeles-HY2`。

Gateway 使用 **最终出口位置**：

```text
US-Boardman-VLESS-AWS
US-Boardman-HY2-AWS
JP-Tokyo-VLESS-JP1
JP-Tokyo-HY2-JP1
SG-Singapore-VLESS-SG1
```

标记放在名称最后，位于协议之后，可为空。位置规则与安装器一致：国家代码大写，国家代码与城市用 `-` 连接，删除空白；`SG + Singapore` 保留为 `SG-Singapore`，不加入 emoji。

新增或修改 IPv4 时自动检测出口位置。查询失败时可手动填写；同一 IP 重新检测失败时显示上次结果。

“手动位置”优先于自动位置。没有位置时用 Marker，再退化为 `Gateway-<ID>`，始终有稳定名称。

改名、调整顺序、修改套餐与重置周期不改变节点 UUID/password。停用会从核心与订阅隐藏衍生节点，保留凭据和周期用量；重新启用继续使用原凭据。删除才清理关联凭据和流量状态。

总订阅顺序为所有 Direct 入站，然后按 Gateway 显示顺序分组输出其入站。顺序值相同时按保存顺序排列。订阅 URL 与 Profile Title 保持入口的名称和 Token。

## 流量估算与周期

入口总流量由 vnStat 统计所选公网网卡的 RX 和 TX，包含 Direct、各 Gateway 及系统通信。Gateway 用量由 A 上对应 B IPv4 + UDP 端口的 WireGuard 流量估算，每个组合须唯一。

隧道计数包含加密封装、IP/UDP 开销、握手和 keepalive，以及主机上其他程序与同一地址及端口的通信。出口套餐估算规则为：

```text
隧道流量 = TunnelTX + TunnelRX
单向套餐估算 = 隧道流量
双向套餐估算 = 隧道流量 × 2
```

GB=1000³ 字节，GiB=1024³ 字节。预留比例决定预警阈值；Gateway 达到阈值后继续运行。入口全局套餐按本机 vnStat 用量独立执行停机，云厂商账单以其后台为准。

入口与各 Gateway 分别设置每月重置日（1–28）和时区。例如入口每月 15 日、出口每月 1 日重置，各自仅重置当前套餐用量。每日和自然月历史独立保存，套餐重置后保留。

出口每 5 秒采样，周期用量和每日 TX/RX 保存在 `/var/lib/sbm/traffic.db`。卡片展示当前套餐周期用量。停用保留凭据和统计记录，重新启用可继续使用；修改 B 地址或端口时保留已累计用量。

采样中断时显示最后结果，保存失败时提示并重试。跨周期中断、主机重启或计数规则重建可能造成数据缺口，界面会标记记录不完整。

## 验证与排查

客户端选 Gateway 节点后访问 `https://cloudflare.com/cdn-cgi/trace`，`ip=` 应为 B；选 Direct 应为 A。VLESS/HY2 都应分别测试。

B 上检查：

```bash
sudo wg show
sudo systemctl status wg-quick@wg0 --no-pager
sudo journalctl -u wg-quick@wg0 -e --no-pager
sudo ss -lunp
sysctl net.ipv4.ip_forward
sudo iptables -t nat -S POSTROUTING
ip route show default
```

- **无握手**：核对当前 B IPv4、UDP 端口、两端公钥、云和主机防火墙。
- **有握手无互联网**：核对 B forwarding、FORWARD 规则、NAT 的 X 和实际公网网卡。确认规则没有排在拒绝规则之后。
- **部分网站失败**：Gateway 支持 IPv4；客户端已解析为 IPv6 的目的地址无法通过这些出口。域名目的地址使用 IPv4 resolve rule，Direct 的 IPv4/IPv6 策略仍独立。
- **计数中断**：A 需要 root/CAP_NET_ADMIN、iptables 与 iptables-save，并使用当前主机活跃的 iptables backend。不要切换 legacy/nft backend 后混用两套防火墙。
- **保存时断开**：核心配置改变会重启 sing-box，代理连接会短暂断开；恢复后刷新页面确认结果。

A 的只读检查：

```bash
sudo /usr/local/bin/sing-box check -c /etc/sing-box/config.json
sudo iptables-save -c -t filter | grep -E 'SBM_EGRESS|sbm-egress'
sudo journalctl -u sbm-panel -e --no-pager
```

没有 A 系统 WireGuard 接口，因此不要用 A 上 `wg show` 的空结果判断连接失败。

## 维护

面板更新、备份和服务管理见 [SBM 管理说明](../README.zh-CN.md#用-sbm-管理服务)。面板更新时 sing-box 持续运行；协议或出口网络配置改变时重启 sing-box。

使用 `sudo sbm` 备份配置、密钥和流量历史。备份包含私钥和订阅 Token，应保存在私有目录。
