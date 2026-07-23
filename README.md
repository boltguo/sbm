# SBM

[简体中文](README.zh-CN.md) | English

SBM is a small web panel for one sing-box server. It is meant for a personal VPS, not a multi-node or multi-user service.

A fresh install starts two inbounds:

- VLESS + Vision + REALITY on TCP/443
- Hysteria2 on UDP/443

They can share port 443 because one uses TCP and the other uses UDP. The panel also gives you one subscription URL for all enabled inbounds.

## Install

You need a Debian or Ubuntu VPS running on amd64 or arm64. Before you install:

1. Create an A record pointing your domain to the VPS public IPv4 address.
2. If the domain uses Cloudflare, set the record to **DNS only** (grey cloud).
3. Allow these ports in the cloud firewall or security group:

| Port | Protocol | Used by |
| --- | --- | --- |
| 80 | TCP | Let's Encrypt certificate issuance and renewal |
| 443 | TCP | VLESS Reality |
| 443 | UDP | Hysteria2 |
| 2096 | TCP | Web panel and subscription |

TCP/2096 is only the default panel port. If you choose another one during installation, open that port instead.

Run as root:

```bash
sudo -i
bash <(curl -fsSL https://raw.githubusercontent.com/boltguo/sbm/main/install.sh)
```

The installer asks for:

```text
Domain: node.example.com
面板端口 [2096]:
节点名称 [Japan-Tokyo]:
```

Press Enter to keep port 2096. The suggested node name comes from the server's public IP and looks like `Japan-Tokyo`; you can replace it. SBM creates `Japan-Tokyo-VLESS` and `Japan-Tokyo-HY2`. At the end, the installer prints the panel address, the `admin` username, a random password, and the subscription URL.

The script installs missing packages, then checks the architecture, free disk space, systemd, DNS, certificate services, and the ports you selected. It refuses to continue if TCP/80, TCP/443, UDP/443, or the panel port is already in use. Once the services start, it checks the listeners and makes a local HTTPS request to the panel.

Cloud firewalls are outside the VPS, so the script cannot test or edit them. It can handle UFW, firewalld, and restrictive iptables rules inside the server. Existing iptables rules are kept; SBM only adds its own ports and restores them after a reboot. The [VPS firewall guide](docs/VPS-COMPATIBILITY.en.md) has the console paths for OCI, AWS, GCP, Azure, Alibaba Cloud, Tencent Cloud, and several common VPS providers.

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

## What the panel does

- Chinese and English UI, with a manual language switch
- SBM and sing-box versions, panel update checks, traffic usage, quota, reset period, and subscription QR code
- CPU, load, memory, disk, uptime, OS, kernel, and architecture status
- Add, edit, enable, disable, and delete VLESS Reality or Hysteria2 inbounds
- Copy a single-node URL or display its QR code
- Automatic UUID, Reality key pair, short ID, and Hysteria2 password generation
- Manual traffic reset or monthly reset on days 1–28
- Automatic sing-box validation and rollback when a protocol change fails

## Manage SBM from the terminal

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
11. Repair boot services and host firewall

Backups are saved in `/root`. Updates verify the SHA-256 digest from GitHub Releases. If the new binary fails its health check, the old one is restored.

The version card on the Overview page checks the latest GitHub Release and shows a red dot when an update is available. To install it, connect over SSH, run `sudo sbm`, and choose option 6.

Successful, failed, and rate-limited sign-in attempts are recorded in the systemd journal:

```bash
journalctl -u sbm-panel -g 'audit event=login'
```

The panel manages host services, so do not expose its port more widely than needed. Remember that subscriptions use the same port: if you restrict it by source IP, every device that updates the subscription must be allowed.

## Adding another inbound

Open the Protocols page, choose a protocol and port, and apply the change. New ports also need a matching TCP or UDP rule in the cloud firewall.

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

The message `debconf: delaying package configuration, since apt-utils is not installed` is normal on minimal Debian images.

## License

See [LICENSE](LICENSE).
