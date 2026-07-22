# SBM

[English](README.md) | 简体中文

SBM 是管理单台 sing-box 服务器的轻量 Web 面板。安装后默认创建并启用两个入站：

- VLESS + Vision + REALITY：TCP/443
- Hysteria2：UDP/443

两者可以同时使用 443，因为 TCP 与 UDP 互不冲突。面板会生成一个包含所有已启用入站的总订阅地址。

## 安装

支持 Debian、Ubuntu，以及 amd64、arm64 架构。安装前先处理好域名和安全组：

1. 添加域名 A 记录，指向 VPS 公网 IPv4。
2. 使用 Cloudflare 时，把代理状态设为 **DNS only / 灰云**。
3. 在云厂商安全组中放行以下端口：

| 端口 | 协议 | 用途 |
| --- | --- | --- |
| 80 | TCP | 申请和续期 Let's Encrypt 证书 |
| 443 | TCP | VLESS Reality |
| 443 | UDP | Hysteria2 |
| 2096 | TCP | Web 面板和总订阅 |

面板默认使用 TCP/2096。安装时可以输入其他端口，云安全组也要改为放行实际端口。

使用 root 运行：

```bash
sudo -i
bash <(curl -fsSL https://raw.githubusercontent.com/boltguo/sbm/main/install.sh)
```

安装脚本会询问：

```text
Domain: node.example.com
面板端口 [2096]:
```

端口直接回车即使用 2096。安装完成后，终端会显示面板地址、`admin` 用户名、随机密码和总订阅地址。

打开面板：

```text
https://node.example.com:2096/
```

忘记密码时运行 `sudo sbm`，选择“重置管理员密码”。

## 界面截图

### 运行概览

![SBM 运行概览](docs/screenshots/dashboard-zh-CN.jpg)

### 协议管理

![SBM 协议管理](docs/screenshots/protocols-zh-CN.jpg)

## 面板功能

- 中英文界面，按浏览器语言初始化并支持手动切换
- sing-box 状态、版本、流量、配额、重置周期和订阅二维码
- CPU、负载、内存、磁盘、运行时长、系统、内核和架构状态
- 新增、修改、启停和删除 VLESS Reality、Hysteria2 入站
- 复制单节点链接或显示二维码
- 自动生成 UUID、Reality 密钥、short ID 和 Hysteria2 密码
- 手动重置流量，或设置每月 1～28 日自动重置
- 协议变更前自动校验 sing-box，失败时恢复原配置

## `sbm` 命令

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

备份文件保存在 `/root`。更新面板或 sing-box 时，会下载对应架构的最新 GitHub Release 并重启服务。

## 添加其他入站

进入“协议”页面，选择协议和端口后应用配置。使用新端口时，需要在云安全组中放行对应的 TCP 或 UDP 规则。

新增、修改、停用或删除入站不会改变总订阅地址。在“设置”中重新生成 Token 后，旧订阅地址会立即失效。

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

## License

见 [LICENSE](LICENSE)。
