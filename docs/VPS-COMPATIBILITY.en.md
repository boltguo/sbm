# VPS firewall and compatibility

[简体中文](VPS-COMPATIBILITY.md) | English

SBM needs four IPv4 inbound rules. The complete list is below. Port `2096` is only the default panel port; use the port entered during installation.

The installer can change the firewall inside the VPS. It cannot edit a provider's Security Group, Cloud Firewall, NSG, or Security List, so those rules still need to be checked in the provider console.

## Before installation

1. Assign a fixed public IPv4 address before pointing a domain at the server when possible. Dynamic addresses on EC2, Lightsail, GCP, and Azure can change after a console Stop/Start or Deallocate operation.
2. The domain's A record must point directly to the VPS. If you use Cloudflare, set the record to **DNS only (grey cloud)**. The regular orange-cloud service is an HTTP/HTTPS proxy and cannot directly carry SBM's Reality TCP and Hysteria2 UDP traffic.
3. Remove the AAAA record unless IPv6 is configured. Some clients otherwise prefer an IPv6 address whose firewall or services are not ready.
4. Add the four inbound rules below in the provider console and leave outbound traffic allowed. SBM is a proxy; a restrictive outbound protocol or destination policy makes only part of the internet reachable.

## Provider detection

When available, the installer reads `cloud-id` and DMI data to recognize OCI, AWS, GCP, Azure, Alibaba Cloud, Tencent Cloud, DigitalOcean, Hetzner, Vultr, and Linode. This only selects the provider-specific message shown during installation. Host firewall changes are based on the actual UFW, firewalld, and iptables configuration.

An unknown provider is treated as a regular KVM host and installation continues. AWS is a special case: EC2 and Lightsail often both identify themselves as Amazon EC2 from inside the VM, so the installer reports `AWS EC2 / Lightsail`. It never asks for cloud credentials or changes the provider console.

## Common providers

| Provider | Easy-to-miss setting | Public IP |
| --- | --- | --- |
| Oracle Cloud (OCI) | NSG and Security List allow rules form a union, but image iptables is another layer; ZPR also needs a policy when enabled | Stopping keeps an ephemeral public IP; terminating the instance does not |
| AWS EC2 | Add rules to the Security Group actually attached to the ENI; a custom Network ACL must also allow response traffic | A regular public IPv4 address usually changes after Stop/Start; use an Elastic IP for a fixed address |
| AWS Lightsail | IPv4 and IPv6 firewalls are independent, and the HTTPS preset does not include UDP/443 | The default public IPv4 changes after Stop/Start; attach a Static IP |
| Google Cloud | VPC ingress has an implied deny; a wrong target tag, service account, or priority prevents an allow rule from taking effect | An ephemeral external IP is released when the VM is stopped or suspended |
| Microsoft Azure | Both the subnet and NIC NSGs must allow the flow; a lower priority number is evaluated first | A Dynamic public IP may change after Stop/Deallocate |
| Alibaba Cloud ECS | Rules from every attached Security Group are sorted together; lower numbers win, and Deny wins a tie | Use an EIP when the domain needs a fixed address |
| Tencent Cloud CVM | Multiple Security Groups are evaluated by priority; a custom group may also deny outbound traffic by default | Check whether the instance's public IP or EIP stays fixed |
| DigitalOcean | The Cloud Firewall must be attached to the Droplet; no inbound rules blocks all ingress, and no outbound rules blocks all egress | The address normally stays with the Droplet; Reserved IPs are available for migration |
| Hetzner Cloud | Apply the Firewall to the target Server/Label; no matching inbound allow rule blocks the flow | Primary IPs are managed separately; check their assignment before replacing a server |
| Vultr | Attach the Firewall Group to the instance; the host firewall still applies | A Reserved IP can be moved to another instance |
| Akamai/Linode | The Cloud Firewall must be Enabled and attached; its default inbound policy is usually Drop | A Reserved IP can be reassigned within the same region |
| DMIT and other KVM providers | Some plans have a provider firewall, but there is no common console layout | The IP normally stays with the instance; check the provider's own policy |

SBM handles active UFW, firewalld, and restrictive iptables rules inside the VPS. OCI images use a more conservative iptables compatibility path. Existing rules are kept; the installer only adds the required ports.

## Ports to allow

Provider consoles use different names, but the required IPv4 inbound rules are the same. TCP and UDP must be configured separately. An `HTTPS/443` preset usually allows TCP only.

| Protocol | Destination port | Source |
| --- | --- | --- |
| TCP | `80` | `0.0.0.0/0` |
| TCP | `443` | `0.0.0.0/0` |
| UDP | `443` | `0.0.0.0/0` |
| TCP | The panel port entered during installation, such as `2096` | Start with `0.0.0.0/0`; restrict it to a fixed `/32` source when practical |

The panel and subscription URL share one port. If you restrict that port by source address, include every phone and computer that needs to refresh the subscription. Otherwise the panel may work while subscription updates fail. The Clash API listens only on `127.0.0.1:9090`, so port 9090 does not belong in the cloud firewall.

TCP/80 is used for the first certificate request and later automatic renewals, so leave it open after installation. If the provider firewall uses an outbound allowlist, change it back to allow all outbound traffic. Installation, updates, and certificate issuance need DNS, HTTP, and HTTPS, while proxy forwarding can use arbitrary remote addresses and ports.

## Provider console steps

### Oracle Cloud (OCI)

1. Open `Networking → Virtual Cloud Networks` and select the instance's VCN.
2. Add the rules either under the subnet's attached `Security Lists → Add Ingress Rules` or under the VNIC's `Network Security Groups → Security Rules → Add Ingress Rules`. Allow rules from both are combined, so they do not need to be duplicated.
3. Add the four rules from the table above. Keep them stateful by leaving Stateless unchecked. Set Source Type to CIDR, then enter the source, IP Protocol, and Destination Port Range.
4. OCI images can also include iptables or firewalld rules. The installer adds the SBM ports without clearing existing rules.
5. If traffic is still blocked, check the Attached VNIC and subnet details for the actual attachments. If the resource has a Zero Trust Packet Routing (ZPR) security attribute, a ZPR policy must allow the traffic too.

References: [OCI Security Rules](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securityrules.htm), [update a Security List](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/update-securitylist.htm).

### AWS EC2

1. Open `EC2 Console → Security Groups` and select the Security Group attached to the instance's network interface.
2. Open `Inbound rules → Edit inbound rules → Add rule`.
3. Add the Custom TCP and Custom UDP entries from the table, fill in Port range and Source, then select `Save rules`.
4. Security Groups are stateful, so they do not need a separate ephemeral-port rule for responses. Allow rules from multiple attached Security Groups are combined.
5. The default Network ACL allows all traffic. If it was replaced with a restrictive custom ACL, check both inbound and outbound rules. Network ACLs are stateless and must allow the service ports and the ephemeral ports used by response traffic.

References: [EC2 Security Groups](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-security-groups.html), [custom Network ACLs](https://docs.aws.amazon.com/vpc/latest/userguide/custom-network-acl.html).

### AWS Lightsail

1. Open `Lightsail Console → Instances → instance name → Networking`.
2. Under `IPv4 Firewall → Add rule`, add the Custom TCP and UDP rules.
3. If the domain has an AAAA record, add the same rules under `IPv6 Firewall`. The IPv4 and IPv6 firewalls are independent.
4. Attach a Static IP before pointing the domain's A record at the instance.

References: [Add Lightsail firewall rules](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-editing-firewall-rules.html), [create a Static IP](https://docs.aws.amazon.com/lightsail/latest/userguide/lightsail-create-static-ip.html).

### Google Cloud (GCP)

1. Open `Google Cloud Console → Firewall policies → Create firewall rule`.
2. Select the VM's VPC for Network, choose Ingress for Direction and Allow for Action, then enter the source under Source IPv4 ranges.
3. Targets can cover every instance in the VPC or only a target tag/service account. With the second option, make sure it matches this VM.
4. Under Protocols and ports, select TCP and UDP and enter the required ports. You may use separate rules or configure both protocols in one rule. Lower priority numbers are evaluated first; also check for a higher-priority deny in an organization or global Firewall Policy.
5. If the rule looks correct, verify the VM's Network interfaces, Network tags, and VPC. `Network Intelligence Center → Connectivity Tests` can help trace the decision.
6. Promote the ephemeral external IPv4 address to a static address before relying on it for a domain.

References: [Create a GCP firewall rule](https://docs.cloud.google.com/firewall/docs/using-firewalls), [reserve a static external IP](https://docs.cloud.google.com/compute/docs/ip-addresses/configure-static-external-ip-address).

### Microsoft Azure

1. Open `Virtual Machines → the VM → Networking → Network settings`.
2. Select `Create port rule → Inbound port rule`. Set Source, Destination port ranges, and Protocol, then choose Allow. Lower numbers are evaluated first, so put the rule before a conflicting deny.
3. Create the TCP and UDP rules from the table above.
4. When both the NIC and Subnet have an NSG, both must allow the traffic. Check `Effective security rules` on the NIC or use `Network Watcher → IP flow verify` to identify the blocking rule.
5. Set the Public IP assignment to Static before using it for a long-lived DNS record.

References: [Manage Azure NSG rules](https://learn.microsoft.com/en-us/azure/virtual-network/manage-network-security-group), [how Azure NSGs work](https://learn.microsoft.com/en-us/azure/virtual-network/network-security-group-how-it-works).

### Alibaba Cloud ECS

1. Open `ECS Console → Network & Security → Security Groups` and select the instance's region.
2. Open the Security Group attached to the instance, then choose `Inbound → Add Rule`.
3. Choose Allow, enter the source from the table, select Custom TCP or UDP, and enter the destination port.
4. HTTP/HTTPS presets cover TCP only. Add separate rules for UDP/443 and the custom panel port.
5. If the instance has more than one Security Group, Alibaba Cloud sorts all rules together. Lower priority numbers are evaluated first; at the same priority, Deny is evaluated before Allow.
6. Basic Security Groups normally allow all outbound traffic, while enterprise groups deny it by default. Confirm that the instance can reach the internet or installation, certificate issuance, and proxy forwarding will fail.

Reference: [Alibaba Cloud ECS Security Group rules](https://www.alibabacloud.com/help/en/ecs/user-guide/security-group-rules).

### Tencent Cloud CVM

1. Open the Security Group console and select the CVM's region.
2. Find the Security Group attached to the instance, then open `Modify Rules → Inbound Rules → Add Rule`.
3. Choose Custom, enter the source and separate TCP/UDP ports, and set the policy to Allow.
4. Tencent Cloud matches Security Group rules from top to bottom. Put an allow rule before any conflicting deny rule.
5. If a CVM has multiple Security Groups, the group listed first has the highest priority. A custom Security Group can deny outbound traffic by default, so verify its outbound policy.
6. If the subnet also has a Network ACL, check both directions; the ACL is stateless. Security Group changes take effect immediately and do not require a CVM restart.

References: [Add Tencent Cloud CVM Security Group rules](https://cloud.tencent.com/document/product/213/112614/), [Security Groups and Network ACLs](https://cloud.tencent.com/document/product/215/20088).

### DigitalOcean, Hetzner, Vultr, and Linode

- DigitalOcean: `Networking → Firewalls → target Firewall → Rules → Inbound Rules`; confirm that the Droplet is attached. A new Firewall without outbound rules also blocks all egress, so keep the suggested allow-all outbound rules.
- Hetzner: open the project's `Firewalls → target Firewall → Rules`, add the inbound rules, and check the target Server/Label under Apply to. With no outbound rules, all egress is allowed; adding any outbound rule changes unmatched egress to deny.
- Vultr: `Products → Network → Firewall → Firewall Group`; add separate protocol rules and confirm the instance under `Linked Instances`.
- Linode: `Network → Firewalls → target Firewall → Inbound Rules`; add Accept rules, then confirm the Firewall is Enabled and the device is attached.

These Cloud Firewalls and UFW/iptables inside the VPS are separate filters. Opening only one layer may still leave a port unreachable.

## If it is still unreachable

Check in this order before reinstalling:

1. Confirm that the domain's A record contains the VPS's current public IPv4 address and that Cloudflare is grey-clouded. Remove a stale AAAA record when IPv6 is not in use.
2. Confirm the provider rule's region, VPC, target tag, and attached instance. A correct rule attached to the wrong resource is one of the most common failures.
3. Check the services and listening sockets on the VPS:

   ```bash
   sudo systemctl --no-pager --full status sbm-panel sing-box sbm-firewall
   sudo ss -lntp
   sudo ss -lnup
   ```

4. Run `sudo sbm` and choose `11. Repair boot services and host firewall` to recheck the local firewall and systemd units. This does not change provider-console rules.
5. Test the panel from another machine or a mobile connection rather than from localhost on the VPS:

   ```bash
   curl -vk https://your-domain.example:panel-port/
   ```

6. If it still times out, capture packets on the VPS while connecting from outside. No incoming packets usually points to DNS, a cloud firewall, an ACL, or the upstream network. Incoming packets without a response point to the listener or host firewall.

   ```bash
   sudo tcpdump -ni any 'tcp port 80 or tcp port 443 or udp port 443 or tcp port 2096'
   ```

   Replace the last port with the panel port chosen during installation. UDP has no handshake, so a generic port scanner reporting “closed” does not by itself prove that Hysteria2 is unavailable.

## Reboot behavior

Installation verifies and enables three systemd units:

- `sbm-firewall.service` restores SBM-managed host firewall ports during boot.
- `sing-box.service` checks its configuration before starting and retries after an unexpected exit. If the traffic quota is exhausted, its boot condition keeps the core stopped.
- `sbm-panel.service` starts after the network and firewall units and retries after an unexpected exit.

Running `reboot` inside the operating system is not the same as Stop/Start or Deallocate in a provider console. The latter may change a dynamic public IP on EC2, Lightsail, GCP, or Azure while the domain's A record still points to the old address. OCI keeps an ephemeral public IP when an instance is stopped, but deletes it when the instance is terminated. The installer checks the A record during installation, but it does not update third-party DNS.

## Official references

- [OCI Security Rules and ZPR](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/securityrules.htm)
- [OCI network security groups and image firewall](https://docs.oracle.com/en-us/iaas/tools/oci-cli/latest/oci_cli_docs/cmdref/network/nsg.html)
- [OCI Security List ingress rules](https://docs.oracle.com/en-us/iaas/Content/Network/Concepts/update-securitylist.htm)
- [OCI public IP addresses](https://docs.oracle.com/en-us/iaas/Content/Network/Tasks/managingpublicIPs.htm)
- [AWS EC2 Security Groups](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ec2-security-groups.html)
- [AWS custom Network ACLs and ephemeral ports](https://docs.aws.amazon.com/vpc/latest/userguide/custom-network-acl.html)
- [AWS EC2 Stop/Start](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Stop_Start.html)
- [AWS Lightsail firewall](https://docs.aws.amazon.com/lightsail/latest/userguide/amazon-lightsail-editing-firewall-rules.html)
- [AWS Lightsail public and Static IPs](https://docs.aws.amazon.com/lightsail/latest/userguide/understanding-public-ip-and-private-ip-addresses-in-amazon-lightsail.html)
- [GCP VPC firewall](https://docs.cloud.google.com/firewall/docs/using-firewalls)
- [GCP Network Tags](https://docs.cloud.google.com/vpc/docs/add-remove-network-tags)
- [GCP static external IP](https://docs.cloud.google.com/compute/docs/ip-addresses/configure-static-external-ip-address)
- [Troubleshoot Azure NSG priority and dual-layer rules](https://learn.microsoft.com/en-us/troubleshoot/azure/virtual-network/virtual-network-troubleshoot-nsg-blocking-traffic)
- [Azure public IP addresses](https://learn.microsoft.com/en-us/azure/virtual-network/ip-services/public-ip-addresses)
- [Alibaba Cloud ECS Security Group rules](https://www.alibabacloud.com/help/en/ecs/user-guide/security-group-rules)
- [Tencent Cloud CVM Security Groups](https://cloud.tencent.com/document/product/213/112614/)
- [Tencent Cloud Security Groups and Network ACLs](https://cloud.tencent.com/document/product/215/20088)
- [DigitalOcean Cloud Firewall defaults](https://docs.digitalocean.com/products/networking/firewalls/getting-started/quickstart/)
- [Hetzner Cloud Firewall FAQ](https://docs.hetzner.com/cloud/firewalls/faq/)
- [Vultr Firewall Rules](https://docs.vultr.com/products/network/firewall-groups/management/rules)
- [Vultr Cloud Firewall FAQ](https://docs.vultr.com/products/network/firewall-groups/faq)
- [Akamai/Linode Cloud Firewall](https://techdocs.akamai.com/cloud-computing/docs/create-a-cloud-firewall)
- [Cloudflare proxy ports and protocols](https://developers.cloudflare.com/fundamentals/reference/network-ports/)
