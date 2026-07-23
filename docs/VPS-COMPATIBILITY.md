# VPS 防火墙与兼容性

[English](VPS-COMPATIBILITY.en.md) | 简体中文

SBM 要用到四条 IPv4 入站规则，完整列表在下文。面板端口以安装时填写的值为准，`2096` 只是默认值。

安装脚本只能修改 VPS 内部的防火墙。云厂商控制台里的 Security Group、Cloud Firewall、NSG 或 Security List 需要手动确认。

## 安装前先确认

1. 尽量先给实例绑定固定公网 IPv4，再设置域名。AWS EC2、Lightsail、GCP 和 Azure 的动态地址都可能在控制台 Stop/Start 或 Deallocate 后变化。
2. 域名的 A 记录要直接指向这台 VPS。使用 Cloudflare 时请设为 **仅 DNS（灰云）**；普通橙云代理是 HTTP/HTTPS 代理，不能直接转发 SBM 的 Reality TCP 和 Hysteria2 UDP。
3. 没有配置 IPv6 就不要保留 AAAA 记录。否则部分客户端会优先连到一个没有配置好防火墙或服务的 IPv6 地址。
4. 在云控制台放行下文的四条入站规则，并保持出站流量允许。SBM 是代理服务，严格限制出站协议或目的端口会让节点只能访问一部分网站。

## 厂商识别

安装器会读取 `cloud-id`（系统提供时）和 DMI 信息，用来识别 OCI、AWS、GCP、Azure、阿里云、腾讯云、DigitalOcean、Hetzner、Vultr 与 Linode。这个结果主要决定安装时显示哪一段提示。真正处理主机防火墙时，脚本还是以 UFW、firewalld 和 iptables 的现状为准。

认不出厂商也没关系，安装会按普通 KVM 继续。AWS 比较特殊：EC2 和 Lightsail 在系统里经常都显示为 Amazon EC2，单靠 VPS 内部信息分不清，所以安装器统一显示 `AWS EC2 / Lightsail`。它不会索要云账号密钥，也不会修改云控制台。

## 常见平台

| 平台 | 容易漏掉的地方 | 公网 IP |
| --- | --- | --- |
| Oracle Cloud (OCI) | NSG、Security List 的允许规则取并集，但镜像 iptables 仍是另一层；启用 ZPR 时还要有对应策略 | 停止实例不会释放临时公网 IP，终止实例另算 |
| AWS EC2 | 规则必须加在网卡实际关联的 Security Group；自定义 Network ACL 还要允许响应流量 | 普通公网 IPv4 在 Stop/Start 后通常会变，固定地址要用 Elastic IP |
| AWS Lightsail | IPv4 和 IPv6 防火墙互相独立，HTTPS 预设也不包含 UDP/443 | 默认公网 IPv4 在 Stop/Start 后会变，建议绑定 Static IP |
| Google Cloud | VPC 有隐式拒绝入站；target tag、service account 或规则优先级不对都会导致允许规则不生效 | 临时外部 IP 会在停止或挂起 VM 后释放 |
| Microsoft Azure | Subnet NSG 和 NIC NSG 都要允许；优先级数字越小越先匹配 | Dynamic 公网 IP 可能在 Stop/Deallocate 后变化 |
| 阿里云 ECS | 多个安全组的规则会汇总排序；数字越小优先级越高，同级拒绝先于允许 | 长期使用域名时建议绑定 EIP |
| 腾讯云 CVM | 多个安全组按优先级顺序执行；自定义安全组还可能默认拒绝出站 | 长期使用域名时建议确认公网 IP 或 EIP 是否固定 |
| DigitalOcean | Cloud Firewall 必须关联 Droplet；未配置入站规则时全部拒绝，未配置出站规则时也全部拒绝 | 一般随 Droplet 保留；迁移可使用 Reserved IP |
| Hetzner Cloud | Firewall 必须 Apply 到目标 Server/Label；没有入站允许规则时全部拒绝 | Primary IP 独立管理，替换服务器前先确认分配 |
| Vultr | Firewall Group 必须关联到实例，而且系统防火墙仍会继续过滤 | Reserved IP 可用于迁移实例 |
| Akamai/Linode | Cloud Firewall 要处于 Enabled 并关联设备，默认入站策略通常是 Drop | Reserved IP 可在同区域重新分配 |
| DMIT 和其他 KVM 商家 | 有些产品带控制台防火墙，但没有统一入口 | 通常随实例保留，具体看商家说明 |

SBM 会处理 VPS 内部已启用的 UFW、firewalld 和严格 iptables 规则。OCI 镜像会走更保守的 iptables 兼容路径。脚本只添加需要的端口，不会清空原规则。

## 要放行哪些端口

各家控制台叫法不同，实际要添加的就是下面四条 IPv4 入站允许规则。TCP 和 UDP 要分开，单独添加 `HTTPS/443` 一般只会放行 TCP。

| 协议 | 目标端口 | 来源 |
| --- | --- | --- |
| TCP | `80` | `0.0.0.0/0` |
| TCP | `443` | `0.0.0.0/0` |
| UDP | `443` | `0.0.0.0/0` |
| TCP | 安装时填写的面板端口，如 `2096` | 可先用 `0.0.0.0/0`；有固定出口 IP 时再限制为 `/32` |

总订阅和面板共用一个端口。限制来源 IP 时，别忘了需要更新订阅的手机和电脑，否则可能出现面板能打开、客户端却拉不到订阅的情况。Clash API 只监听 `127.0.0.1:9090`，不用在云防火墙里开放 9090。

TCP/80 用于首次申请和后续自动续期证书，安装完成后也建议保持开放。若云防火墙启用了出站白名单，请改回允许全部出站；至少安装、更新和签发证书需要 DNS、HTTP、HTTPS，代理转发本身还会访问任意外部地址和端口。

## 各平台怎么设置

### Oracle Cloud (OCI)

1. 打开 `Networking → Virtual Cloud Networks`，选择实例所在的 VCN。
2. 可以在子网实际关联的 `Security Lists → Add Ingress Rules` 中添加规则，也可以在 VNIC 所属的 `Network Security Groups → Security Rules → Add Ingress Rules` 中添加。两者的允许规则取并集，不需要重复添加两遍。
3. 添加上表中的四条规则。保持有状态，也就是不要勾选 Stateless。Source Type 选 CIDR，然后填写来源、IP Protocol 和 Destination Port Range。
4. OCI 官方镜像还可能带有系统 iptables/firewalld 规则。安装器会增量放行 SBM 端口，但不会清空原规则。
5. 仍不通时，在实例详情的 Attached VNIC 和子网详情中确认实际关联项。若资源启用了 Zero Trust Packet Routing（ZPR）安全属性，还必须有允许该流量的 ZPR policy。

参考：[OCI Security Rules](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securityrules.htm)、[更新 Security List 规则](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/update-securitylist.htm)。

### AWS EC2

1. 打开 `EC2 Console → Security Groups`，选择实例网卡实际关联的 Security Group。
2. 进入 `Inbound rules → Edit inbound rules → Add rule`。
3. 分别选择 Custom TCP、Custom UDP，按上表填写 Port range 与 Source，最后点击 `Save rules`。
4. Security Group 是有状态的，不需要额外为回包开放临时端口。多个 Security Group 的允许规则会合并生效。
5. 默认 Network ACL 允许全部流量；如果改成了自定义严格 ACL，则要同时检查入站和出站。Network ACL 是无状态的，除了服务端口，还要允许客户端响应流量使用的临时端口范围。

参考：[EC2 Security Group](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-security-groups.html)、[自定义 Network ACL](https://docs.aws.amazon.com/vpc/latest/userguide/custom-network-acl.html)。

### AWS Lightsail

1. 打开 `Lightsail Console → Instances → 实例名称 → Networking`。
2. 在 `IPv4 Firewall → Add rule` 中分别添加 Custom TCP/UDP 规则和端口。
3. 域名配置了 AAAA 记录时，还要在 `IPv6 Firewall` 添加相同规则。两套防火墙互不相通。
4. 建议先给实例附加 Static IP，再把域名 A 记录指向该地址。

参考：[添加 Lightsail 防火墙规则](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-editing-firewall-rules.html)、[创建 Static IP](https://docs.aws.amazon.com/lightsail/latest/userguide/lightsail-create-static-ip.html)。

### Google Cloud (GCP)

1. 打开 `Google Cloud Console → Firewall policies → Create firewall rule`。
2. Network 选择实例所在 VPC，Direction 选 Ingress，Action 选 Allow，Source IPv4 ranges 填来源。
3. Targets 可选择 VPC 中所有实例，或使用指定 target tag/service account；如果后者没有命中当前 VM，规则不会生效。
4. 在 Protocols and ports 中分别勾选 TCP、UDP 并填写端口。可以建多条规则，也可以在同一条规则中配置对应协议端口。数字越小优先级越高，还要留意组织级或全局 Firewall Policy 中更高优先级的拒绝规则。
5. 规则看起来正确但仍不通时，先核对 VM 的 Network interfaces、Network tags 与 VPC；需要时可用 `Network Intelligence Center → Connectivity Tests` 排查。
6. 域名长期使用时，把临时外部 IPv4 提升为静态地址。

参考：[创建 GCP 防火墙规则](https://docs.cloud.google.com/firewall/docs/using-firewalls)、[静态外部 IP](https://docs.cloud.google.com/compute/docs/ip-addresses/configure-static-external-ip-address)。

### Microsoft Azure

1. 打开 `Virtual Machines → 当前 VM → Networking → Network settings`。
2. 选择 `Create port rule → Inbound port rule`，设置 Source、Destination port ranges、Protocol，Action 选 Allow。优先级数字越小越先执行，新规则必须排在冲突的拒绝规则前。
3. 为上表中的 TCP、UDP 端口分别创建规则。
4. NIC 与 Subnet 都关联了 NSG 时，两边都要允许。可以在网卡页面查看 `Effective security rules`，或使用 `Network Watcher → IP flow verify` 找出实际拒绝流量的规则。
5. 域名长期使用时，把 Public IP 的 Assignment 设为 Static。

参考：[管理 Azure NSG 规则](https://learn.microsoft.com/en-us/azure/virtual-network/manage-network-security-group)、[NSG 工作方式](https://learn.microsoft.com/en-us/azure/virtual-network/network-security-group-how-it-works)。

### 阿里云 ECS

1. 打开 `云服务器 ECS 控制台 → 网络与安全 → 安全组`，选择实例所在地域。
2. 找到实例绑定的安全组，进入详情页，选择 `入方向 → 添加规则`。
3. 策略选允许，来源按上表填写，协议类型分别选择自定义 TCP、UDP，填写目的端口。
4. 预置 HTTP/HTTPS 规则只覆盖 TCP；必须另外添加 UDP/443 和自定义面板端口。
5. 如果一台实例绑定多个安全组，平台会汇总所有规则后匹配。优先级数字越小越先执行，同一优先级下拒绝规则先于允许规则。
6. 基础安全组通常允许全部出站，企业安全组默认拒绝出站。请确认实例可以访问公网，否则安装、证书签发和代理转发都会失败。

参考：[阿里云 ECS 安全组规则](https://www.alibabacloud.com/help/en/ecs/user-guide/security-group-rules)。

### 腾讯云 CVM

1. 打开 `安全组控制台`，选择 CVM 所在地域。
2. 找到实例绑定的安全组，点击 `修改规则 → 入站规则 → 添加规则`。
3. 类型选自定义，来源按上表填写，协议端口分别填写 TCP、UDP，策略选允许。
4. 腾讯云安全组从上到下匹配；如有冲突的拒绝规则，允许规则应放在它前面。
5. 一台 CVM 绑定多个安全组时，位置越靠上的组优先级越高。自定义安全组可能默认拒绝出站，请确认出站允许。
6. 如果子网还绑定了 Network ACL，也要检查入站和出站；ACL 是无状态的。安全组修改后立即生效，无需重启 CVM。

参考：[添加腾讯云 CVM 安全组规则](https://cloud.tencent.com/document/product/213/112614/)、[安全组与 Network ACL 的区别](https://cloud.tencent.com/document/product/215/20088)。

### DigitalOcean、Hetzner、Vultr 与 Linode

- DigitalOcean：`Networking → Firewalls → 目标 Firewall → Rules → Inbound Rules`，并确认 Droplet 已关联。新建 Firewall 如果没有出站规则也会阻止全部出站，请保留推荐的允许全部出站规则。
- Hetzner：项目的 `Firewalls → 目标 Firewall → Rules`，添加 Inbound Rules，并在 Apply to 中确认目标 Server/Label。没有任何 Outbound Rule 时默认允许全部出站；一旦添加出站规则，未匹配流量会被拒绝。
- Vultr：`Products → Network → Firewall → Firewall Group`，分别添加协议规则，并在 `Linked Instances` 中确认实例使用该组。
- Linode：`Network → Firewalls → 目标 Firewall → Inbound Rules`，添加 Accept 规则，并确认状态为 Enabled、目标设备已关联。

这些 Cloud Firewall 和服务器内部的 UFW/iptables 是两层过滤。只改其中一层，端口仍然可能不通。

## 还是连不上时

按下面的顺序查，通常比反复重装更快：

1. 检查域名 A 记录是不是当前 VPS 的公网 IPv4，Cloudflare 是否为灰云；没有使用 IPv6 时删除错误的 AAAA 记录。
2. 在云控制台确认规则的地域、VPC、目标标签和关联实例都正确。最常见的情况不是规则写错，而是规则没有应用到这台机器。
3. 在 VPS 上确认服务和监听端口：

   ```bash
   sudo systemctl --no-pager --full status sbm-panel sing-box sbm-firewall
   sudo ss -lntp
   sudo ss -lnup
   ```

4. 运行 `sudo sbm`，选择 `11. 修复开机启动与防火墙`，让安装器重新检查本机防火墙和 systemd 服务。它不会修改云控制台规则。
5. 从另一台机器或手机网络测试面板，不能只在 VPS 自己访问 localhost：

   ```bash
   curl -vk https://你的域名:面板端口/
   ```

6. 若仍超时，可在 VPS 上抓包后再从外部访问。完全看不到入站包时，问题通常在 DNS、云防火墙、ACL 或上游网络；能看到包却没有响应时，再查监听地址和系统防火墙。

   ```bash
   sudo tcpdump -ni any 'tcp port 80 or tcp port 443 or udp port 443 or tcp port 2096'
   ```

   最后一项要换成安装时填写的面板端口。UDP 没有握手，仅凭普通“端口扫描”显示关闭并不能判定 Hysteria2 不可用。

## 重启恢复

安装时会验证并启用三个 systemd 单元：

- `sbm-firewall.service`：在开机时恢复由 SBM 管理的主机防火墙端口。
- `sing-box.service`：配置检查通过后启动；意外退出持续重试。若流量配额已超限，开机条件会阻止核心启动。
- `sbm-panel.service`：等待网络与防火墙单元后启动，意外退出持续重试。

操作系统里的 `reboot` 不等于云控制台里的 Stop/Start 或 Deallocate。后者可能改变 EC2、Lightsail、GCP 或 Azure 的动态公网 IP，而域名 A 记录还指着旧地址，于是看起来就像“重启后失联”。OCI 的临时公网 IP 在停止实例时会保留，但终止实例时会删除。安装器会在安装时检查 A 记录，但不会替你修改 DNS。

## 官方资料

- [OCI Security Rules 与 ZPR](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securityrules.htm)
- [OCI 网络安全组与镜像防火墙](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/network/nsg.html)
- [OCI Security List 入站规则](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/update-securitylist.htm)
- [OCI 公网 IP](https://docs.oracle.com/en-us/iaas/Content/Network/Tasks/managingpublicIPs.htm)
- [AWS EC2 Security Group](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-security-groups.html)
- [AWS 自定义 Network ACL 与临时端口](https://docs.aws.amazon.com/vpc/latest/userguide/custom-network-acl.html)
- [AWS EC2 Stop/Start](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Stop_Start.html)
- [AWS Lightsail 防火墙](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-editing-firewall-rules.html)
- [AWS Lightsail 公网与 Static IP](https://docs.aws.amazon.com/lightsail/latest/userguide/understanding-public-ip-and-private-ip-addresses-in-amazon-lightsail.html)
- [GCP VPC 防火墙](https://docs.cloud.google.com/firewall/docs/using-firewalls)
- [GCP Network Tags](https://docs.cloud.google.com/vpc/docs/add-remove-network-tags)
- [GCP 静态外部 IP](https://docs.cloud.google.com/compute/docs/ip-addresses/configure-static-external-ip-address)
- [Azure NSG 优先级与双层规则排查](https://learn.microsoft.com/en-us/troubleshoot/azure/virtual-network/virtual-network-troubleshoot-nsg-blocking-traffic)
- [Azure 公网 IP](https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/public-ip-addresses)
- [阿里云 ECS 安全组规则](https://www.alibabacloud.com/help/en/ecs/user-guide/security-group-rules)
- [腾讯云 CVM 安全组](https://cloud.tencent.com/document/product/213/112614/)
- [腾讯云安全组与 Network ACL](https://cloud.tencent.com/document/product/215/20088)
- [DigitalOcean Cloud Firewall 默认策略](https://docs.digitalocean.com/products/networking/firewalls/getting-started/quickstart/)
- [Hetzner Cloud Firewall FAQ](https://docs.hetzner.com/cloud/firewalls/faq/)
- [Vultr Firewall Rules](https://docs.vultr.com/products/network/firewall-groups/management/rules)
- [Vultr Cloud Firewall FAQ](https://docs.vultr.com/products/network/firewall-groups/faq)
- [Akamai/Linode Cloud Firewall](https://techdocs.akamai.com/cloud-computing/docs/create-a-cloud-firewall)
- [Cloudflare 代理支持的端口与协议](https://developers.cloudflare.com/fundamentals/reference/network-ports/)
