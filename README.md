# SBM

[简体中文](README.zh-CN.md) | English

SBM is a lightweight web panel for a single sing-box server. A fresh installation creates and enables two inbounds:

- VLESS + Vision + REALITY on TCP/443
- Hysteria2 on UDP/443

Both can use port 443 because one is TCP and the other is UDP. The panel also provides one subscription URL containing every enabled inbound.

## Install

Use a Debian or Ubuntu VPS with amd64 or arm64 architecture. Before installation:

1. Create an A record pointing your domain to the VPS public IPv4 address.
2. If the domain uses Cloudflare, set the record to **DNS only** (grey cloud).
3. Allow these ports in the cloud firewall or security group:

| Port | Protocol | Used by |
| --- | --- | --- |
| 80 | TCP | Let's Encrypt certificate issuance and renewal |
| 443 | TCP | VLESS Reality |
| 443 | UDP | Hysteria2 |
| 2096 | TCP | Web panel and subscription |

The panel defaults to TCP/2096. You can enter another port during installation; allow that port instead of 2096.

Run as root:

```bash
sudo -i
bash <(curl -fsSL https://raw.githubusercontent.com/boltguo/sing-box/main/install.sh)
```

The installer asks for:

```text
Domain: node.example.com
面板端口 [2096]:
```

Press Enter to use 2096. When installation finishes, the terminal prints the panel URL, the `admin` username, a generated password, and the subscription URL.

Open the panel at:

```text
https://node.example.com:2096/
```

If you forget the password, run `sudo sbm` and choose `Reset administrator password`.

## Screenshots

### Overview

![SBM runtime overview](docs/screenshots/dashboard-en.jpg)

### Protocols

![SBM protocol management](docs/screenshots/protocols-en.jpg)

## Panel features

- Chinese and English UI with browser-language detection and a manual switch
- sing-box status, version, traffic usage, quota, reset period, and subscription QR code
- CPU, load, memory, disk, uptime, OS, kernel, and architecture status
- Add, edit, enable, disable, and delete VLESS Reality or Hysteria2 inbounds
- Copy a single-node URL or display its QR code
- Automatic UUID, Reality key pair, short ID, and Hysteria2 password generation
- Manual traffic reset or monthly reset on days 1–28
- Automatic sing-box validation and rollback when a protocol change fails

## The `sbm` command

```bash
sudo sbm
```

The menu includes:

1. Show panel URL and service status
2. Restart the panel
3. Restart sing-box
4. Reset the administrator password
5. View logs
6. Update the panel
7. Update sing-box
8. Back up configuration
9. Restore configuration
10. Uninstall

Backups are written to `/root`. Panel and sing-box updates download the latest matching GitHub Release and restart the corresponding service.

## Adding another inbound

Open the Protocols page, choose the protocol and port, then apply the change. If you use a new port, allow the matching TCP or UDP rule in the cloud security group.

The master subscription URL does not change when inbounds are added, edited, disabled, or removed. Regenerating its token in Settings invalidates the old URL.

## Troubleshooting

Panel status and logs:

```bash
systemctl status sbm-panel --no-pager
journalctl -u sbm-panel -e --no-pager
```

sing-box status and configuration check:

```bash
/usr/local/bin/sing-box check -c /etc/sing-box/config.json
systemctl status sing-box --no-pager
journalctl -u sing-box -e --no-pager
```

If certificate issuance fails, check the A record, Cloudflare grey-cloud mode, TCP/80, and whether another process is already using port 80.

## License

See [LICENSE](LICENSE).
