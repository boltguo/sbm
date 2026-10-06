# Multiple WireGuard egress gateways

English | [简体中文](WIREGUARD-EXIT.md)

SBM still manages one entry VPS, one sing-box process, and one administrator. Optional gateways let clients choose their final exit within one master subscription.

```text
Client → VLESS / Hysteria2 → A / SBM Entry
                              ├─ Direct → Internet (A IP)
                              ├─ WireGuard → B / AWS → Internet (AWS IP)
                              ├─ WireGuard → B / JP  → Internet (JP IP)
                              └─ WireGuard → B / SG  → Internet (SG IP)
```

A uses the **userspace WireGuard endpoint in official sing-box 1.13.14**, without installing a system WireGuard interface. B can use ordinary Linux WireGuard. All variants retain the entry domain and VLESS TCP / HY2 UDP ports. Independent UUIDs or passwords identify `auth_user` routes to distinct endpoints.

With no gateways, the original configuration, Direct nodes, and subscription behavior are unchanged.

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

Tunnel slots are persisted. Removing or reordering another gateway never renumbers an existing gateway. The first version offers 254 slots; personal use usually needs just a few.

**Use the X shown on each gateway card. Do not copy 10.66.1.1 to every B.**

## Configure B on Debian / Ubuntu

Run these commands on **B**. Replace `X`, `B_PUBLIC_INTERFACE`, `A_PUBLIC_KEY`, and `A_PUBLIC_IPV4` with real values. Keep private keys out of chat, logs, and public documentation.

Install tools and generate B's keys:

```bash
sudo apt update
sudo apt install -y wireguard iptables iproute2
sudo install -d -m 700 /etc/wireguard
sudo sh -c 'umask 077; wg genkey > /etc/wireguard/b-private.key; wg pubkey < /etc/wireguard/b-private.key > /etc/wireguard/b-public.key'
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

Persist NAT according to your firewall setup. A only needs its original entry ports; gateway variants require no additional VLESS/HY2 ports. A must allow outbound UDP to every B; B must allow Internet egress.

## Cloud differences

| B platform | Check |
| --- | --- |
| AWS EC2 | Attach an Elastic IP; permit A's UDP in the Security Group; custom NACLs must also allow responses. |
| AWS Lightsail | Attach a Static IP; permit A's UDP in the IPv4 Firewall; confirm firewall/instance association. |
| GCP | Use static external IPv4; ensure VPC firewall rules target the VM; check IP forwarding when using it as a VPC route next hop. |
| Ordinary VPS | Confirm static IPv4, provider and host UDP allowances, and Linux WireGuard support. |

If B's cloud IP changes, update its gateway IPv4 on A. A-to-B latency adds to the destination path; stable addressing and low packet loss often matter more than geographic labels. Residential public IPv4 requires correct UDP forwarding; CGNAT cannot directly provide reachable B service.

## Naming, geolocation, and lifecycle

Direct names remain as configured, such as `US-LosAngeles-VLESS` and `US-LosAngeles-HY2`.

Gateway names describe the **final exit**:

```text
US-Boardman-VLESS-AWS
US-Boardman-HY2-AWS
JP-Tokyo-VLESS-JP1
JP-Tokyo-HY2-JP1
SG-Singapore-VLESS-SG1
```

The optional marker follows the protocol at the end of the node name. Automatic location follows the installer: uppercase country code, a hyphen before the city, whitespace removed, and no emoji. `SG + Singapore` remains `SG-Singapore`.

Adding or changing an IPv4 queries `https://ipwho.is/<IPv4>` with a maximum three-second timeout and stores country, region, and city. Subscription and Dashboard reads never query Geo. Failure still allows saving; changing an IP discards the old IP's location. Detect location refreshes the cache; failure for the same IP retains the previous result.

Location override wins over detection. Without a location, naming uses Marker, then `Gateway-<ID>`, so names never become empty.

Renaming, reordering, or editing plans/reset schedules preserves UUIDs/passwords. Disabling hides endpoints and variants while retaining credentials and period usage. Re-enabling uses the original credentials. Only deletion removes associated credentials and traffic state.

Subscriptions list all Direct inbounds first, followed by gateways grouped in display order. Equal positions preserve insertion order. The subscription URL and Profile Title continue to use the entry's name and token.

## Estimated traffic and periods

On A, `SBM_EGRESS_TX` and `SBM_EGRESS_RX` filter accounting chains contain uniquely commented rules for each B IPv4 + UDP port. Counter rules have no ACCEPT/DROP target and return to the existing INPUT/OUTPUT flow. SBM never flushes host rules or removes UFW/firewalld policies. Parsing uses raw byte counters from `iptables-save -c -t filter`.

Sampling checks the position and number of owned jumps, maintaining one first-position jump per parent chain while preserving tunnel counters and user rules. After all gateways are disabled and cleanup succeeds, polling stops invoking iptables; inactive monthly periods still advance. Gateway sampling runs independently of the entry's global counters and quota checks. Global usage includes all proxy traffic served by the entry, while each gateway independently estimates B's plan usage; these figures must not be added together.

Counters include encrypted tunnel packets, IP/UDP overhead, handshakes, and keepalives. They also include other programs talking to that same peer address/port, so each gateway's IPv4 + UDP port pair must be unique.

```text
TunnelBytes = TunnelTX + TunnelRX
EstimatedProviderUsage = TunnelBytes × ProviderUsageFactor
single → ×1
bidirectional → ×2
```

GB=1000³ bytes; GiB=1024³. Enter the advertised allowance directly rather than dividing it by two. Headroom determines the warning threshold. Each gateway has its own monthly day (1–28) and timezone.

**Estimated from WireGuard tunnel traffic is not a provider invoice. A gateway warning never stops sing-box, Direct, or other gateways.** Existing global local-plan enforcement remains independent.

Period totals, baselines, boot/rule generations, and reset times use the existing JSON state file, with the existing 30-second persistence interval. Missing rules are recreated. Counter rollback or a generation change folds in the new counter. Updating peer IP/port establishes a new baseline while retaining period usage. Resetting a period never clears kernel counters.

If an outage spans a reset boundary, cumulative counters cannot split bytes exactly between months; the first recovered sample becomes the new period baseline. Sampling failure retains the last result and displays an interruption. Statistics problems do not prevent saving or running the proxy. Incomplete rule cleanup after deleting the last gateway is recorded and retried.

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
- **Disconnect while saving:** core changes restart sing-box. The independent apply context finishes the persisted transaction; refresh once the connection recovers.

Read-only checks on A:

```bash
sudo /usr/local/bin/sing-box check -c /etc/sing-box/config.json
sudo iptables-save -c -t filter | grep -E 'SBM_EGRESS|sbm-egress'
sudo journalctl -u sbm-panel -e --no-pager
```

A has no system WireGuard interface, so an empty `wg show` on A is expected.

## Upgrade and test development builds

ConfigVersion remains 4 and StateVersion remains 1. Existing 2.0.2 v4 configurations load directly; omitted optional fields mean no gateways. No 1.x→2.x migration is restored. Back up `/etc/sbm` and `/var/lib/sbm` before upgrading. Old 2.0.2 binaries are not guaranteed to read added fields, so restore original backups when downgrading.

`make release` produces Linux amd64/arm64 archives. Deploy a locally built artifact until the new code is released; the published 2.0.2 release lacks this feature. Official sing-box remains pinned to 1.13.14; no custom build or V2Ray API is needed.

To test the source version on an existing 2.0.2 instance, run `make release VERSION=egress-dev` on the development machine. Upload the matching archive and `checksums.txt` to a temporary directory on A. Run there (amd64 example; replace the archive name for arm64):

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

Investigate any command failure before continuing and retain the backup directory named by `egress_backup_dir`. Existing sing-box can keep running while the panel is replaced; gateway additions subsequently apply core configuration through the panel transaction. If the new panel cannot start, restore the backed-up panel and management script before adding gateways. Downgrading after adding new fields also requires restoring configuration and state. Backups contain keys and tokens; keep them private to root.

The development version `egress-dev` has no formal installer core-version mapping. When installing the core from the management menu, use `sudo SING_BOX_VERSION=1.13.14 sbm`. Before a supporting release is published, avoid replacing the development panel with 2.0.2 through “Update panel”.

Test commands:

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

The final script tests real iptables inside a new Linux network namespace, without changing host networking. Setting `SBM_TEST_SING_BOX` to the official binary also runs genuine VLESS/HY2 → A → two userspace WireGuard B peers, verifying observed exits and counters. Production B NAT and cloud firewalls still need the deployment IP checks above.
