# SBM

[English](README.md) | 简体中文

SBM 是给单台 sing-box 服务器用的小面板，适合自用 VPS，不做多节点和多用户运营。

全新安装会启用两个入站：

- VLESS + Vision + REALITY：TCP/443
- Hysteria2：UDP/443

面板会生成一个总订阅地址，包含所有已启用的入站。

## 安装

系统需要是 Debian 或 Ubuntu，架构支持 amd64 和 arm64。安装前先处理域名和安全组：

1. 添加域名 A 记录，指向 VPS 公网 IPv4。
2. 使用 Cloudflare 时，把代理状态设为 **DNS only / 灰云**。
3. 在云厂商安全组中放行以下端口：

| 端口 | 协议 | 用途 |
| --- | --- | --- |
| 80 | TCP | 申请和续期 Let's Encrypt 证书 |
| 443 | TCP | VLESS Reality |
| 443 | UDP | Hysteria2 |
| 2096 | TCP | Web 面板和总订阅 |

TCP/2096 只是默认值。安装时如果换了端口，安全组也要放行你实际填写的端口。

分两步执行。先切换到 root：

```bash
sudo -i
```

已经在 root shell 里就跳过这一步。然后运行安装脚本：

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/boltguo/sbm/main/install.sh)
```

安装脚本会询问：

```text
Domain: node.example.com
面板端口 [2096]:
节点名称 [JP-Tokyo]:
```

端口直接回车就是 2096。节点名称按服务器公网 IP 生成，国家用两位大写代码，据此生成 `JP-Tokyo-VLESS` 和 `JP-Tokyo-HY2`。安装结束后终端会打印面板地址、`admin` 用户名、随机密码和总订阅地址。

TCP/80、TCP/443、UDP/443 或面板端口被占用时安装会中止。服务起来后脚本会检查监听端口，并从本机请求一次面板 HTTPS。

VPS 内部的 UFW、firewalld 和 iptables 会自动处理，已有 iptables 规则不会被清空。云安全组在 VPS 外面，脚本改不了，各平台控制台入口见 [VPS 防火墙指南](docs/VPS-COMPATIBILITY.md)。

打开面板：

```text
https://node.example.com:2096/
```

忘记密码时运行 `sudo sbm`，选择 `重置管理员密码`。

## 界面截图

### 运行概览

![SBM 运行概览](docs/screenshots/dashboard-zh-CN.jpg)

### 协议管理

![SBM 协议管理](docs/screenshots/protocols-zh-CN.jpg)

## 面板能做什么

- 中英文界面，可手动切换语言
- SBM 与 sing-box 版本、面板更新检测、流量、配额、重置周期和订阅二维码
- CPU、负载、内存、磁盘、运行时长、系统、内核和架构状态
- 新增、修改、启停和删除 VLESS Reality、Hysteria2 入站
- 复制单节点链接或显示二维码
- 自动生成 UUID、Reality 密钥、short ID 和 Hysteria2 密码
- 手动重置流量，或设置每月 1～28 日自动重置
- 可选自动、优先 IPv4、优先 IPv6、仅 IPv4 或仅 IPv6 的代理出口策略
- 可为每个协议增加同端口的 WireGuard IPv4 出口节点，A 端无需安装 WireGuard
- 协议变更前自动校验 sing-box，失败时恢复原配置

### IPv4 / IPv6 出口

在 `设置 → 代理出口网络` 中可选择地址族策略。IPv6 地区归属更准确时建议使用“优先 IPv6”：目标有 AAAA 记录时优先走 IPv6，没有时仍回退 IPv4；“仅 IPv6”会让 IPv4-only 目标无法访问。

该策略要求 sing-box 1.12 或更高版本，并且只影响 sing-box 收到的域名目标。客户端已经把域名解析成 IP 时，服务端无法再切换地址族。客户端通过 IPv6 接入还需要域名有正确的 AAAA 记录，并在云防火墙和主机防火墙中放行对应端口。

### WireGuard 出口节点

从 v1.2.0 开始，可以在 `设置 → WireGuard 附加节点` 中配置 B。开关关闭时，订阅只包含原来的直连节点；打开后，每个已启用协议都会在相同域名和端口下增加一组独立凭据，例如 `DMIT VLESS · via GCP` 和 `DMIT HY2 · via GCP`。原直连节点始终保留。

附加节点通过认证用户单独路由到用户态 WireGuard endpoint，目标域名按 IPv4 解析；其他节点继续使用 `代理出口网络` 中的地址族策略并从 A 直连。关闭开关只会从 sing-box 和订阅中隐藏附加凭据，重新打开后 UUID 和密码保持不变。

面板可以生成 A 的 WireGuard 密钥；A 的隧道地址（`10.66.0.2/32`）、MTU（`1408`）和保活（`25s`）已经内置。B 仍需单独安装 WireGuard、开启 IPv4 转发和 NAT，并在云防火墙中只允许 A 访问 WireGuard UDP 端口。B 或隧道故障时，只有 `via GCP` 节点不可用，客户端可直接切回原节点。

## 用 `sbm` 管理服务

```bash
sudo sbm
```

菜单包含：

1. 查看面板地址和运行状态
2. 重启面板
3. 重启 sing-box
4. 重置管理员密码
5. 查看日志
6. 更新面板
7. 更新 sing-box
8. 备份配置
9. 恢复配置
10. 卸载
11. 修复开机启动与防火墙

备份文件放在 `/root`。更新面板或 sing-box 时会校验 GitHub Release 的 SHA-256 摘要；新版本没有通过健康检查就恢复旧版本。

概览页的版本卡片会检查最新 GitHub Release，有新版本时显示红点。需要安装时通过 SSH 运行 `sudo sbm`，选择第 6 项即可。

登录成功、失败和被限流事件会写入 systemd journal，可通过以下命令查看：

```bash
journalctl -u sbm-panel -g 'audit event=login'
```

面板能直接管理宿主机服务，端口不要放得太宽。总订阅共用这个端口，按来源 IP 限制时要把需要更新订阅的设备算进去。

## 添加其他入站

进入 `协议` 页面，选好协议和端口后应用配置。新端口还要在云安全组里放行对应的 TCP 或 UDP。

新增、修改、停用或删除入站不会改变总订阅地址。在 `设置` 中重新生成 Token 后，旧订阅地址会立即失效。

## 排查问题

查看面板状态和日志：

```bash
systemctl status sbm-panel --no-pager
journalctl -u sbm-panel -e --no-pager
```

检查 sing-box：

```bash
/usr/local/bin/sing-box check -c /etc/sing-box/config.json
systemctl status sing-box --no-pager
journalctl -u sing-box -e --no-pager
```

证书申请失败时，检查 A 记录、Cloudflare 灰云、TCP/80，以及 80 端口是否已被其他程序占用。

`debconf: delaying package configuration, since apt-utils is not installed` 是 Debian 精简系统的普通提示，不代表安装失败。

## License

见 [LICENSE](LICENSE)。
