# 多 WireGuard 中继出口

[English](WIREGUARD-EXIT.en.md) | 简体中文

SBM 仍然管理一台入口 VPS、一个 sing-box 和一个管理员。可选的 Gateway 层让客户端在同一个总订阅中选择最终出口。

```text
Client → VLESS / Hysteria2 → A / SBM Entry
                              ├─ Direct → Internet（A IP）
                              ├─ WireGuard → B / AWS → Internet（AWS IP）
                              ├─ WireGuard → B / JP  → Internet（JP IP）
                              └─ WireGuard → B / SG  → Internet（SG IP）
```

A 使用 sing-box **1.13.14 的 userspace WireGuard endpoint**，不安装系统 WireGuard 接口。B 可以使用普通 Linux WireGuard。入口域名、VLESS TCP 端口和 HY2 UDP 端口与原节点相同；每个出口使用独立 UUID 或 password，由 `auth_user` 路由到自己的 endpoint。

没有 Gateway 时，原配置、Direct 节点和订阅行为保持不变。

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

地址槽保存在配置中。删除或重排另一个 Gateway 不会改变现有 Gateway 的地址。第一版支持 254 个地址槽，个人使用通常只需要几个。

**每个 B 必须使用该 Gateway 卡片显示的 X，不能把所有 B 都照抄成 10.66.1.1。**

## 配置 B（Debian / Ubuntu）

以下命令在 **B** 上执行。使用实际值替换 `X`、`B_PUBLIC_INTERFACE`、`A_PUBLIC_KEY`、`A_PUBLIC_IPV4`。不要把 A 或 B 的私钥放进聊天、日志或公开文档。

安装工具、生成 B 密钥：

```bash
sudo apt update
sudo apt install -y wireguard iptables iproute2
sudo install -d -m 700 /etc/wireguard
sudo sh -c 'umask 077; wg genkey > /etc/wireguard/b-private.key; wg pubkey < /etc/wireguard/b-private.key > /etc/wireguard/b-public.key'
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

NAT 规则仍需按实际网络部署持久化。A 只需要原有入口端口；不必给每个 Gateway 开新 VLESS/HY2 端口。A 的出站策略必须允许到各 B 的 UDP，B 的出站策略必须允许访问互联网。

## 云平台差异

| B 平台 | 检查事项 |
| --- | --- |
| AWS EC2 | 绑定 Elastic IP；Security Group 放行 A 的 UDP；自定义 NACL 同时允许回应流量。 |
| AWS Lightsail | 绑定 Static IP；在 IPv4 Firewall 放行 A 的 UDP；注意实例与防火墙实际关联。 |
| GCP | 使用静态外部 IPv4；VPC 防火墙规则正确匹配 VM；需要作为 VPC 路由下一跳时检查 IP forwarding。 |
| 普通 VPS | 确认 IPv4 固定、商家防火墙及主机防火墙允许 UDP、系统支持 WireGuard。 |

云 IP 变化后必须更新 A 的 Gateway IPv4。A 到 B 的延迟叠加到目标连接；静态 IP 和低丢包路径通常比地理名称更影响稳定性。住宅公网地址需要正确的 UDP 端口转发；CGNAT 无法直接充当可达 B。

## 节点名称与位置

Direct 名称保留，例如 `US-LosAngeles-VLESS`、`US-LosAngeles-HY2`。

Gateway 使用 **最终出口位置**：

```text
US-Boardman-VLESS-AWS
US-Boardman-HY2-AWS
JP-Tokyo-VLESS-JP1
JP-Tokyo-HY2-JP1
SG-Singapore-VLESS-SG1
```

标记放在名称最后，位于协议之后，可为空。位置规则与安装器一致：国家代码大写，国家代码与城市用 `-` 连接，删除空白；`SG + Singapore` 保留为 `SG-Singapore`，不加入 emoji。

新增或修改 IPv4 时，后端最多用 3 秒向 `https://ipwho.is/<IPv4>` 查询并缓存国家、地区、城市。订阅和 Dashboard 不实时查 Geo。查询失败仍可保存；IP 更换失败时丢弃旧 IP 的位置。可点击“重新检测位置”，同一 IP 重查失败则保留上次结果。

“手动位置”优先于自动位置。没有位置时用 Marker，再退化为 `Gateway-<ID>`，始终有稳定名称。

改名、调整顺序、修改套餐与重置周期不改变节点 UUID/password。停用会从核心与订阅隐藏衍生节点，保留凭据和周期用量；重新启用继续使用原凭据。删除才清理关联凭据和流量状态。

总订阅顺序为所有 Direct 入站，然后按 Gateway 显示顺序分组输出其入站。顺序值相同时按保存顺序排列。订阅 URL 与 Profile Title 保持入口的名称和 Token。

## 流量估算与周期

A 上的 `SBM_EGRESS_TX`、`SBM_EGRESS_RX` filter accounting chain 使用带唯一 comment 的规则。规则只按 B 的 IPv4 + UDP 端口计数，没有 ACCEPT/DROP target，返回原 INPUT/OUTPUT 流程。SBM 不 flush 主机规则，不清理 UFW/firewalld 策略。套餐显示使用 `iptables-save -c -t filter` 的原始字节计数。

这个计数包含加密封装、IP/UDP 开销、握手和 keepalive；也包含主机上其他程序发往同一 IPv4/UDP 端口的流量。因此每个 Gateway 的 IPv4+UDP 端口组合必须唯一。

```text
TunnelBytes = TunnelTX + TunnelRX
EstimatedProviderUsage = TunnelBytes × ProviderUsageFactor
single → ×1
bidirectional → ×2
```

GB=1000³ 字节，GiB=1024³ 字节；直接按套餐显示输入，不预先除以二。预留比例决定警告阈值，每个 Gateway 有独立月度日期（1–28）和时区。

**“根据 WireGuard 隧道流量估算”不是云厂商官方账单。Gateway 达到预警阈值不会停止 sing-box、Direct 或其他 Gateway。** 本机现有全局套餐的停机逻辑仍然独立生效。

SBM 将周期累计、基线、boot ID/规则代次和下一次重置保存到现有 JSON state；定期持久化间隔仍是 30 秒。规则缺失时重建，计数回退或代次改变时按新 counter 累加。修改 B IP/端口重新建立基线，并保留已累计周期用量。周期重置不清空内核 counter。

若重启/采样中断跨过周期边界，无法把累计 counter 精确拆分到两个月；首次恢复采样作为新周期基线。采样失败时保留最后结果并显示中断，不让统计问题阻止代理保存或运行。最后一个 Gateway 删除后的未完成规则清理会记录并重试。

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
- **部分网站失败**：第一版 IPv4-only；客户端已解析为 IPv6 的目的地址无法通过这些出口。域名目的地址使用 IPv4 resolve rule，Direct 的 IPv4/IPv6 策略仍独立。
- **计数中断**：A 需要 root/CAP_NET_ADMIN、iptables 与 iptables-save，并使用当前主机活跃的 iptables backend。不要切换 legacy/nft backend 后混用两套防火墙。
- **保存时断开**：核心配置改变会重启 sing-box；SBM 使用独立 apply 上下文完成事务，等连接恢复后刷新确认结果。

A 的只读检查：

```bash
sudo /usr/local/bin/sing-box check -c /etc/sing-box/config.json
sudo iptables-save -c -t filter | grep -E 'SBM_EGRESS|sbm-egress'
sudo journalctl -u sbm-panel -e --no-pager
```

没有 A 系统 WireGuard 接口，因此不要用 A 上 `wg show` 的空结果判断连接失败。

## 升级与验证开发版本

保持 ConfigVersion=4、StateVersion=1。现有 2.0.2 v4 配置可直接加载，新字段省略时默认没有 Gateway。不恢复 1.x→2.x migration。升级前备份 `/etc/sbm` 与 `/var/lib/sbm`；含新字段的配置不保证可由旧 2.0.2 二进制读取，降级应恢复原备份。

构建 `make release` 生成 Linux amd64/arm64 包。尚未发布的新代码应使用本地构建产物部署；现有发布版 2.0.2 不包含这个功能。sing-box 保持官方 1.13.14，无需定制编译或 V2Ray API。

现有 2.0.2 实例测试源码版本时，可在开发机运行 `make release VERSION=egress-dev`，把对应架构的包及 `checksums.txt` 上传到 A 的临时目录。在该目录执行以下命令（amd64 示例；arm64 替换包名）：

```bash
sha256sum -c --ignore-missing checksums.txt
tar -xzf sbm-panel_egress-dev_linux_amd64.tar.gz
sudo systemctl stop sbm-panel.service
egress_backup_dir="$(sudo mktemp -d /root/sbm-egress-backup.XXXXXX)"
sudo tar -czf "$egress_backup_dir/config-state.tar.gz" -C / etc/sbm var/lib/sbm etc/sing-box/config.json
sudo cp -p /usr/local/bin/sbm-panel "$egress_backup_dir/sbm-panel"
sudo cp -p /usr/local/bin/sbm "$egress_backup_dir/sbm"
sudo install -m 755 sbm-panel /usr/local/bin/sbm-panel
sudo install -m 755 sbm /usr/local/bin/sbm
sudo /usr/local/bin/sbm-panel config apply --no-start
sudo systemctl start sbm-panel.service
sudo systemctl is-active sbm-panel.service
```

命令失败应先检查错误再继续，并记下 `egress_backup_dir` 指向的备份目录。原 sing-box 服务可在替换面板期间继续运行；开始添加 Gateway 后由面板事务应用核心配置。如果新面板不能启动，在添加 Gateway 前恢复备份的面板/管理脚本并启动服务；添加新字段后降级还需恢复配置与 state。备份含密钥和 Token，应保留在 root 私有目录。

开发版本标记 `egress-dev` 尚无安装器的正式版本绑定；管理菜单安装核心时使用 `sudo SING_BOX_VERSION=1.13.14 sbm`。正式 Release 发布前，不要用菜单“更新面板”把开发版本替换回不支持新字段的 2.0.2。

测试命令：

```bash
go test ./...
go test -race ./...
go vet ./...
npm --prefix web run build
bash scripts/install-unit.sh
bash scripts/frontend-style-unit.sh
shellcheck install.sh scripts/*.sh
SBM_TEST_SING_BOX=/path/to/sing-box-1.13.14 go test ./internal/core -run TestSingBoxEgressIntegration -v
bash scripts/egress-linux-test.sh
```

后一个命令在新的 Linux network namespace 中测试实际 iptables，不修改主机网络。`SBM_TEST_SING_BOX` 指定官方核心时，该脚本还会运行真实 VLESS/HY2 → A → 两个 userspace WireGuard B peer 的出口与计数测试。生产 B 的 NAT/云防火墙仍应在部署后按上面的 IP 检查确认。
