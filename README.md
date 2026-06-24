# sing-box 一键脚本 (sbm)

个人自用的 sing-box 一键安装 / 管理脚本，一条命令搭好两种主流协议节点并生成通用订阅。

- **协议**：VLESS + Vision + REALITY（TCP）/ Hysteria2（UDP）
- **证书**：acme.sh 通过 HTTP-01 申请 Let's Encrypt 真证书（自动续期）
- **附带**：BBR 加速、vnstat 流量统计、base64 订阅（小火箭 / v2rayNG / NekoBox / Hiddify 通用）、订阅二维码

---

## 一、环境要求

| 项目 | 要求 |
| --- | --- |
| 系统 | Debian / Ubuntu（仅支持 `apt`） |
| 架构 | x86_64 (amd64) 或 aarch64 (arm64) |
| 权限 | root（`sudo`） |
| 内核 | BBR 需 ≥ 4.9（一般现代发行版已满足） |
| 域名 | 一个已解析到本机公网 IP 的域名 |

---

## 二、安装前准备（重要）

1. **解析域名到本机**
   - 在 DNS 服务商把域名 A 记录指向本机公网 IP。
   - 若使用 **Cloudflare，必须设为「灰云 / DNS only」**（关闭代理小云朵），否则 HTTP-01 申证会失败。

2. **放行端口（云服务器安全组）**

   脚本只会在系统自带 `ufw` 处于启用状态时自动放行端口，**无法操作云厂商控制台的安全组**。请在云控制台手动放行以下端口：

   | 端口 | 协议 | 用途 |
   | --- | --- | --- |
   | 80 | TCP | 申请 / 续期证书（HTTP-01，必须空闲且可达） |
   | VLESS 端口（默认 443） | TCP | VLESS-REALITY 节点 |
   | Hysteria2 端口（默认 443） | UDP | Hysteria2 节点 |
   | 订阅端口（默认 2096） | TCP | HTTPS 订阅服务 |

   > 80 端口在安装与每次自动续期时都需要短暂空闲；脚本已让 nginx 仅监听订阅端口、不占用 80。

3. **甲骨文 Oracle Cloud 专属坑（其它云可跳过）**

   甲骨文有**两层**防火墙：云端安全列表 + **实例自带 iptables（默认 REJECT，只放行 22）**。光在控制台开安全列表没用，必须再进 SSH 改 iptables，否则证书申请会卡在 80 端口验证失败。

   先看 REJECT 在第几行：

   ```bash
   iptables -L INPUT -n --line-numbers
   ```

   把放行规则插到 `REJECT` 那一行**之前**（下例假设 REJECT 在第 5 行，按实际行号替换）：

   ```bash
   iptables -I INPUT 5 -p tcp --dport 80 -j ACCEPT
   iptables -I INPUT 5 -p tcp --dport 443 -j ACCEPT
   iptables -I INPUT 5 -p udp --dport 443 -j ACCEPT
   iptables -I INPUT 5 -p tcp --dport 2096 -j ACCEPT
   netfilter-persistent save
   ```

   > `netfilter-persistent save` 把规则写入磁盘，重启后不丢失（缺命令则先 `apt install -y iptables-persistent`）。
   >
   > 各云对比：**AWS / GCP / Azure / 阿里云 / 腾讯云**实例自带防火墙通常关闭，只需在控制台开安全组；**搬瓦工 / Vultr / DO / Linode**默认全开，开箱即用；**只有甲骨文**需要额外做这步 iptables。

---

## 三、安装步骤

**方式一：一键运行（推荐）**

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/boltguo/sing-box/main/install.sh)
```

> 若当前不是 root，请先 `sudo -i` 切到 root 再执行上面的命令。
> 脚本会在安装时自动从仓库拉取自身并安装为 `sbm` 命令，之后直接运行 `sbm` 即可管理。

**方式二：先下载再运行**

```bash
curl -fsSL -o install.sh https://raw.githubusercontent.com/boltguo/sing-box/main/install.sh
sudo bash install.sh
```

按提示依次输入（方括号内为默认值，回车即用默认）：

| 提示 | 说明 | 默认 |
| --- | --- | --- |
| 域名 | 已灰云解析到本机的域名（**必填**） | 无 |
| 节点名称 | 显示在客户端里的名字 | 自动按 IP 归属生成，如 `🇯🇵Japan-Tokyo`；国家与城市相同时只保留一个（如 `🇸🇬Singapore`），探测失败回退 `MyNode` |
| REALITY 借用的 SNI | 用来伪装的大站域名 | `www.apple.com` |
| VLESS TCP 端口 | VLESS-REALITY 监听端口 | `443` |
| Hysteria2 UDP 端口 | Hysteria2 监听端口 | `443` |
| 订阅 TCP 端口 | HTTPS 订阅端口（勿与 VLESS 端口相同） | `2096` |
| Hysteria2 密码 | 留空则随机生成 | 随机 |

安装过程会自动完成：

1. 安装依赖（curl / socat / qrencode / nginx / vnstat / openssl）
2. 开启 BBR
3. 下载安装最新版 sing-box，并注册 systemd 服务
4. 生成 UUID / REALITY 密钥对 / short-id /（随机）HY2 密码 / 订阅 token
5. 用 acme.sh 申请 Let's Encrypt 证书
6. 写入 sing-box 配置、nginx 订阅站点
7. 启动服务并打印节点信息 + 订阅链接 + 二维码

安装成功后，会把脚本自拷贝为系统命令 `sbm`，之后直接运行即可进入管理菜单。

---

## 四、获取节点 / 订阅

安装结束会直接打印：

- **VLESS-REALITY 链接**（`vless://...`）
- **Hysteria2 链接**（`hysteria2://...`）
- **订阅链接**：`https://你的域名:订阅端口/随机token#节点名`（末尾 `#节点名` 是给客户端自动命名用的，小火箭 / v2rayNG 会据此命名订阅；`#` 部分不发往服务器，不影响拉取）
- 订阅二维码（终端 ANSI 图）

把链接或订阅地址导入客户端即可：

- 单条链接：复制 `vless://` / `hysteria2://` 链接，在客户端「从剪贴板导入」。
- 订阅：在客户端添加订阅，填入上面的 `https://...` 订阅地址（小火箭 / v2rayNG / NekoBox / Hiddify 均支持 base64 订阅）。

> 🔒 订阅链接内含密钥，**切勿外传**。

随时可用 `sudo sbm` → 菜单第 `1` 项重新查看链接、订阅与二维码。

---

## 五、日常管理（`sbm` 菜单）

安装完成后，运行：

```bash
sudo sbm
```

进入管理菜单：

| 选项 | 功能 |
| --- | --- |
| 1 | 查看节点链接 / 订阅 / 二维码 |
| 2 | 查看流量统计（vnstat） |
| 3 | 重启 sing-box |
| 4 | 更新 sing-box 内核（拉取最新版并重启） |
| 5 | 查看运行状态 / 日志 |
| 6 | 卸载 |
| 0 | 退出 |

流量统计常用命令：

```bash
vnstat        # 概览
vnstat -m     # 按月
vnstat -d     # 按日
vnstat -l     # 实时
```

---

## 六、卸载

`sudo sbm` → 选 `6`，确认后会：

- 停用并删除 sing-box 服务与二进制
- 删除 nginx 订阅站点配置
- 删除证书申请记录（acme `--remove`）
- 删除配置目录 `/etc/sing-box` 与订阅文件

> 保留项：**vnstat、BBR 设置、acme.sh 本体**（如需彻底清理请手动处理）。

---

## 七、目录与文件

| 路径 | 说明 |
| --- | --- |
| `/etc/sing-box/config.json` | sing-box 主配置 |
| `/etc/sing-box/sbm.env` | 脚本参数（含密钥，权限 600） |
| `/etc/sing-box/cert/` | 证书 `cert.pem` / `key.pem` |
| `/usr/local/bin/sing-box` | sing-box 二进制 |
| `/usr/local/bin/sbm` | 管理命令（脚本自拷贝） |
| `/var/www/sub/` | 订阅文件目录 |
| `/etc/systemd/system/sing-box.service` | systemd 服务 |

---

## 八、常见问题

**证书申请失败？**
- 确认域名 A 记录已指向本机，Cloudflare 为「灰云」。
- 确认 80 端口在云安全组中已放行且未被其他程序占用。
- 报错含 `acme-challenge ... Error getting validation data`：80 端口公网不可达，**甲骨文 Oracle 需按上面第二节做实例 iptables 放行**。
- 域名只能用字母 / 数字 / `.` / `-`，**不能有下划线 `_`**（Let's Encrypt 不签）。
- 重试：`sudo sbm`（若已安装则需先卸载重装），或手动检查 `~/.acme.sh/`。

**提示 `Could not get lock /var/lib/dpkg/lock-frontend`？**
- 系统自动更新（unattended-upgrades）正占用 apt，等其跑完（`pgrep unattended-upgr` 无输出）再重试，或 `systemctl stop unattended-upgrades`。

**装完连不上？**
- 检查云安全组是否放行了 VLESS(TCP) / HY2(UDP) / 订阅(TCP) 端口。
- `sudo sbm` → 选 `5` 看运行状态与日志，或 `journalctl -u sing-box -e`。

**`sbm` 命令不存在？**
- 正常情况下安装时会自动安装 `sbm`（本地运行则拷贝脚本，一键运行则从仓库拉取）。
- 若仍提示找不到，手动安装：
  ```bash
  curl -fsSL -o /usr/local/bin/sbm https://raw.githubusercontent.com/boltguo/sing-box/main/install.sh
  chmod +x /usr/local/bin/sbm
  ```

**BBR 没生效？**
- 提示「需更新内核后重启」时，升级内核（≥4.9）后 `reboot`，再 `sudo sbm` 重启服务。

---

## 九、安全提示

- 订阅地址与分享链接均包含密钥/凭据，请勿公开分享。
- `sbm.env` 已设为 `600` 权限，请勿改动或泄露。
- 仅在你有权使用的服务器与合法合规的用途下使用本脚本。
