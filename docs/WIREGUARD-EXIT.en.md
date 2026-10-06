# Multiple WireGuard egress gateways

English | [简体中文](WIREGUARD-EXIT.md)

SBM manages one entry VPS, one sing-box process, and one administrator. Optional gateways let clients choose their final exit within one master subscription.

```text
Client → VLESS / Hysteria2 → A / SBM Entry
                              ├─ Direct → Internet (A IP)
                              ├─ WireGuard → B / AWS → Internet (AWS IP)
                              ├─ WireGuard → B / JP  → Internet (JP IP)
                              └─ WireGuard → B / SG  → Internet (SG IP)
```

A uses the **userspace WireGuard endpoint in official sing-box 1.13.14**, without installing a system WireGuard interface. B can use ordinary Linux WireGuard. Direct and gateway nodes share the entry domain and VLESS TCP / HY2 UDP ports. Each gateway uses independent UUIDs or passwords.

Direct nodes leave through A; gateway nodes use WireGuard to leave through B.

## Add a gateway on A

Open Egress:

1. Add a gateway and initially leave it disabled.
2. Enter B's static public IPv4 and UDP port (51820 by default). Hostnames and IPv6 are unsupported.
3. Generate A's key pair. Keep its private key on A and copy **A's public key** to B. The private input is hidden.
4. You can fill B's public key later. Save the disabled draft first to obtain stable addresses, for example:

   ```text
   A tunnel address: 10.66.1.2/32
   B interface address: 10.66.1.1/24
   ```

5. Configure B below, fill its public key, enable, and save.
6. Refresh the client subscription and select the gateway node.

Generate a separate A key pair for each gateway. Private keys stay on their own server; public keys are exchanged.

| A panel | B configuration |
| --- | --- |
| A private key stays on A | No A private key needed |
| A public key copied to B | `[Peer] PublicKey` |
| B public key entered on A | Derived from B private key |
| A tunnel address: 10.66.X.2/32 | `[Peer] AllowedIPs = 10.66.X.2/32` |
| B interface address: 10.66.X.1/24 | `[Interface] Address = 10.66.X.1/24` |

Supports 254 fixed tunnel slots, with addresses saved independently for each gateway.

**Use the X shown on each gateway card. Do not copy 10.66.1.1 to every B.**

## Configure B on Debian / Ubuntu

Run these commands on **B**. Replace `X`, `B_PUBLIC_INTERFACE`, `A_PUBLIC_KEY`, and `A_PUBLIC_IPV4` with real values. Keep private keys out of chat, logs, and public documentation.

Install tools and generate B's keys:

```bash
sudo apt update
sudo apt install -y wireguard iptables iproute2
sudo install -d -m 700 /etc/wireguard
sudo sh -c 'umask 077; test -s /etc/wireguard/b-private.key || wg genkey > /etc/wireguard/b-private.key; wg pubkey < /etc/wireguard/b-private.key > /etc/wireguard/b-public.key'
sudo cat /etc/wireguard/b-public.key
```

Enter B's public key on A. Run `ip route show default` and find the public interface after `dev`, such as `ens5`, `ens4`, or `eth0`.

Enable IPv4 forwarding:

```bash
echo 'net.ipv4.ip_forward=1' | sudo tee /etc/sysctl.d/70-wireguard-routing.conf
sudo sysctl -p /etc/sysctl.d/70-wireguard-routing.conf
```

Create `/etc/wireguard/wg0.conf`. The first gateway might use X=1:

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

This example targets a plain iptables host. With default DROP, UFW/firewalld, or custom chains, integrate FORWARD/NAT allowances into the existing policy. Appending an allowance after an earlier DROP will not work. Do not flush existing firewall rules.

```bash
sudo chmod 600 /etc/wireguard/wg0.conf /etc/wireguard/b-private.key
sudo systemctl enable --now wg-quick@wg0
sudo wg show
```

Both the cloud firewall and B's host firewall must allow **UDP/51820 from A_PUBLIC_IPV4/32**. For UFW:

```bash
sudo ufw allow from A_PUBLIC_IPV4 to any port 51820 proto udp
sudo ufw route allow in on wg0 out on B_PUBLIC_INTERFACE
```

Persist NAT according to your firewall setup. Gateways share A’s VLESS/HY2 entry ports. A must allow outbound UDP to every B; B must allow Internet egress.

## Cloud differences

| B platform | Check |
| --- | --- |
| AWS EC2 | Attach an Elastic IP; permit A's UDP in the Security Group; custom NACLs must also allow responses. |
| AWS Lightsail | Attach a Static IP; permit A's UDP in the IPv4 Firewall; confirm firewall/instance association. |
| GCP | Use static external IPv4; ensure VPC firewall rules target the VM; check IP forwarding when using it as a VPC route next hop. |
| Ordinary VPS | Confirm static IPv4, provider and host UDP allowances, and Linux WireGuard support. |

If B's cloud IP changes, update its gateway IPv4 on A. A-to-B latency adds to the destination path; stable addressing and low packet loss often matter more than geographic labels. Residential public IPv4 requires correct UDP forwarding; CGNAT cannot directly provide reachable B service.

## Naming, geolocation, and lifecycle

Direct node names include `US-LosAngeles-VLESS` and `US-LosAngeles-HY2`.

Gateway names describe the **final exit**:

```text
US-Boardman-VLESS-AWS
US-Boardman-HY2-AWS
JP-Tokyo-VLESS-JP1
JP-Tokyo-HY2-JP1
SG-Singapore-VLESS-SG1
```

The optional marker follows the protocol at the end of the node name. Automatic location follows the installer: uppercase country code, a hyphen before the city, whitespace removed, and no emoji. `SG + Singapore` remains `SG-Singapore`.

Adding or changing IPv4 automatically detects the exit location. If detection fails, enter a location override. A failed recheck of the same IP keeps its last result.

Location override wins over detection. Without a location, naming uses Marker, then `Gateway-<ID>`, so names never become empty.

Renaming, reordering, or editing plans/reset schedules preserves UUIDs/passwords. Disabling hides endpoints and variants while retaining credentials and period usage. Re-enabling uses the original credentials. Only deletion removes associated credentials and traffic state.

Subscriptions list all Direct inbounds first, followed by gateways grouped in display order. Equal positions preserve insertion order. The subscription URL and Profile Title continue to use the entry's name and token.

## Estimated traffic and periods

The entry uses vnStat RX and TX from its selected public interface, covering Direct, all gateways and system traffic. Each gateway’s usage is estimated from WireGuard traffic on A to B’s IPv4 + UDP port. Every address/port pair must be unique.

Tunnel counters include encryption, IP/UDP overhead, handshakes, keepalives and other host programs communicating with the same address and port. Gateway plan estimates use:

```text
Tunnel traffic = TunnelTX + TunnelRX
One-way plan estimate = Tunnel traffic
Two-way plan estimate = Tunnel traffic × 2
```

GB=1000³ bytes; GiB=1024³ bytes. The reserve sets the warning threshold; gateways keep running after reaching it. Entry quotas independently stop sing-box based on local vnStat usage. The provider dashboard is authoritative for billing.

The entry and each gateway have their own monthly reset day (1–28) and timezone. For example, resets on the 15th for the entry and the 1st for a gateway each reset their own current-period usage. Daily and calendar-month history is saved independently and retained after plan resets.

Gateway traffic is sampled every five seconds. Period usage and daily TX/RX are saved in `/var/lib/sbm/traffic.db`. Cards show current-plan usage. Disabling preserves credentials and traffic records for reactivation; changing B’s address or port retains accumulated usage.

Sampling interruptions show the last result. Failed saves display a warning and retry. Outages spanning a reset, host restarts or counter-rule recreation can leave gaps, which the interface marks as incomplete records.

## Verify and troubleshoot

Select a gateway in the client and visit `https://cloudflare.com/cdn-cgi/trace`: `ip=` should be B. Select Direct: it should be A. Test VLESS and HY2 separately.

On B:

```bash
sudo wg show
sudo systemctl status wg-quick@wg0 --no-pager
sudo journalctl -u wg-quick@wg0 -e --no-pager
sudo ss -lunp
sysctl net.ipv4.ip_forward
sudo iptables -t nat -S POSTROUTING
ip route show default
```

- **No handshake:** check B's current IPv4, UDP port, both public keys, and cloud/host firewalls.
- **Handshake but no Internet:** check B forwarding, FORWARD rules, NAT subnet X, and the public interface. Allow rules must precede applicable rejects.
- **Some sites fail:** gateways are IPv4-only. Client-resolved IPv6 destinations cannot use them. Domain targets receive an IPv4 resolve rule; Direct's address-family strategy remains independent.
- **Sampling interruption:** A needs root/CAP_NET_ADMIN, iptables and iptables-save, using the active firewall backend. Do not switch legacy/nft backends and mix two rule sets.
- **Disconnect while saving:** core changes restart sing-box and briefly disconnect proxy connections. Refresh the page once the connection recovers.

Read-only checks on A:

```bash
sudo /usr/local/bin/sing-box check -c /etc/sing-box/config.json
sudo iptables-save -c -t filter | grep -E 'SBM_EGRESS|sbm-egress'
sudo journalctl -u sbm-panel -e --no-pager
```

A has no system WireGuard interface, so an empty `wg show` on A is expected.

## Maintenance

For panel updates, backups and service management, see [SBM management](../README.md#manage-sbm-from-the-terminal). sing-box keeps running during panel updates; protocol or gateway network changes restart sing-box.

Use `sudo sbm` to back up configuration, keys and traffic history. Backups contain private keys and subscription tokens; keep them in a private directory.
