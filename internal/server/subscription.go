package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

const (
	subTargetBase64  = "base64"
	subTargetClash   = "clash"
	subTargetSingBox = "singbox"
)

// resolveSubscriptionTarget determines the desired subscription output format
// from the query string (?target= or ?format=) or the client User-Agent header.
// Returns (target, explicit).
func resolveSubscriptionTarget(r *http.Request) (string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("target"))
	if raw == "" {
		raw = strings.TrimSpace(r.URL.Query().Get("format"))
	}
	if raw != "" {
		switch strings.ToLower(raw) {
		case "clash", "mihomo", "clash-meta", "clash.meta", "stash", "yaml":
			return subTargetClash, true
		case "singbox", "sing-box", "sfa", "sfm", "sfi", "json":
			return subTargetSingBox, true
		case "v2ray", "base64", "uri", "shadowrocket", "plain":
			return subTargetBase64, true
		}
	}

	ua := strings.ToLower(r.Header.Get("User-Agent"))
	switch {
	case strings.Contains(ua, "clash"),
		strings.Contains(ua, "mihomo"),
		strings.Contains(ua, "stash"),
		strings.Contains(ua, "flclash"):
		return subTargetClash, false
	case strings.Contains(ua, "sing-box"),
		strings.Contains(ua, "singbox"),
		strings.Contains(ua, "sfa"),
		strings.Contains(ua, "sfm"),
		strings.Contains(ua, "sfi"):
		return subTargetSingBox, false
	default:
		return subTargetBase64, false
	}
}

func collectSubscriptionNodes(cfg model.Config, registry *protocol.Registry) ([]model.Inbound, []string) {
	nodes := make([]model.Inbound, 0, len(cfg.Inbounds))
	links := make([]string, 0, len(cfg.Inbounds))

	for _, inbound := range cfg.Inbounds {
		if !inbound.Enabled {
			continue
		}
		d, ok := registry.Get(inbound.Type)
		if !ok {
			continue
		}
		link, err := d.ShareLink(inbound, protocol.ShareContext{Domain: cfg.Domain})
		if err == nil {
			nodes = append(nodes, inbound)
			links = append(links, link)
		}
	}

	for _, node := range protocol.EgressNodes(cfg) {
		d, ok := registry.Get(node.Inbound.Type)
		if !ok {
			continue
		}
		if link, err := d.ShareLink(node.Inbound, protocol.ShareContext{Domain: cfg.Domain}); err == nil {
			nodes = append(nodes, node.Inbound)
			links = append(links, link)
		}
	}

	// Ensure node names are unique for Clash / sing-box outbound tags
	seen := make(map[string]int, len(nodes))
	for i := range nodes {
		name := strings.TrimSpace(nodes[i].Name)
		if name == "" {
			name = fmt.Sprintf("Node-%d", i+1)
		}
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s #%d", name, seen[name])
		}
		nodes[i].Name = name
	}
	return nodes, links
}

func renderClashSubscription(domain string, nodes []model.Inbound) string {
	var b strings.Builder
	b.WriteString("mixed-port: 7890\n")
	b.WriteString("allow-lan: false\n")
	b.WriteString("mode: rule\n")
	b.WriteString("log-level: info\n")
	b.WriteString("ipv6: true\n")
	b.WriteString("unified-delay: true\n")
	b.WriteString("tcp-concurrent: true\n")
	b.WriteString("find-process-mode: strict\n")
	b.WriteString("global-client-fingerprint: chrome\n")
	b.WriteString("dns:\n")
	b.WriteString("  enable: true\n")
	b.WriteString("  ipv6: true\n")
	b.WriteString("  enhanced-mode: fake-ip\n")
	b.WriteString("  fake-ip-range: 198.18.0.1/16\n")
	b.WriteString("  default-nameserver:\n")
	b.WriteString("    - 223.5.5.5\n")
	b.WriteString("    - 119.29.29.29\n")
	b.WriteString("  nameserver:\n")
	b.WriteString("    - https://dns.alidns.com/dns-query\n")
	b.WriteString("    - https://doh.pub/dns-query\n")

	if len(nodes) == 0 {
		b.WriteString("proxies: []\n")
	} else {
		b.WriteString("proxies:\n")
		for _, n := range nodes {
			switch n.Type {
			case protocol.TypeVLESSReality:
				if n.VLESS == nil {
					continue
				}
				v := n.VLESS
				fmt.Fprintf(&b, "  - name: %s\n", strconv.Quote(n.Name))
				b.WriteString("    type: vless\n")
				fmt.Fprintf(&b, "    server: %s\n", strconv.Quote(domain))
				fmt.Fprintf(&b, "    port: %d\n", n.Port)
				fmt.Fprintf(&b, "    uuid: %s\n", strconv.Quote(v.UUID))
				b.WriteString("    network: tcp\n")
				b.WriteString("    tls: true\n")
				b.WriteString("    udp: true\n")
				b.WriteString("    flow: xtls-rprx-vision\n")
				fmt.Fprintf(&b, "    servername: %s\n", strconv.Quote(v.SNI))
				b.WriteString("    client-fingerprint: chrome\n")
				b.WriteString("    reality-opts:\n")
				fmt.Fprintf(&b, "      public-key: %s\n", strconv.Quote(v.PublicKey))
				fmt.Fprintf(&b, "      short-id: %s\n", strconv.Quote(v.ShortID))
			case protocol.TypeHysteria2:
				if n.Hysteria2 == nil {
					continue
				}
				h := n.Hysteria2
				fmt.Fprintf(&b, "  - name: %s\n", strconv.Quote(n.Name))
				b.WriteString("    type: hysteria2\n")
				fmt.Fprintf(&b, "    server: %s\n", strconv.Quote(domain))
				fmt.Fprintf(&b, "    port: %d\n", n.Port)
				fmt.Fprintf(&b, "    password: %s\n", strconv.Quote(h.Password))
				fmt.Fprintf(&b, "    sni: %s\n", strconv.Quote(domain))
				b.WriteString("    alpn:\n")
				b.WriteString("      - h3\n")
				if h.Obfs != "" {
					fmt.Fprintf(&b, "    obfs: %s\n", strconv.Quote(h.Obfs))
					fmt.Fprintf(&b, "    obfs-password: %s\n", strconv.Quote(h.ObfsPassword))
				}
			}
		}
	}

	b.WriteString("proxy-groups:\n")
	b.WriteString("  - name: \"🚀 节点选择\"\n")
	b.WriteString("    type: select\n")
	b.WriteString("    proxies:\n")
	if len(nodes) > 0 {
		b.WriteString("      - \"⚡ 自动优选\"\n")
		for _, n := range nodes {
			fmt.Fprintf(&b, "      - %s\n", strconv.Quote(n.Name))
		}
	}
	b.WriteString("      - DIRECT\n")

	if len(nodes) > 0 {
		b.WriteString("  - name: \"⚡ 自动优选\"\n")
		b.WriteString("    type: url-test\n")
		b.WriteString("    url: https://www.gstatic.com/generate_204\n")
		b.WriteString("    interval: 300\n")
		b.WriteString("    tolerance: 50\n")
		b.WriteString("    proxies:\n")
		for _, n := range nodes {
			fmt.Fprintf(&b, "      - %s\n", strconv.Quote(n.Name))
		}
	}

	b.WriteString("rules:\n")
	b.WriteString("  - IP-CIDR,127.0.0.0/8,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR,10.0.0.0/8,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR,172.16.0.0/12,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR,192.168.0.0/16,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR,100.64.0.0/10,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR6,::1/128,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR6,fc00::/7,DIRECT,no-resolve\n")
	b.WriteString("  - IP-CIDR6,fe80::/10,DIRECT,no-resolve\n")
	b.WriteString("  - GEOIP,CN,DIRECT,no-resolve\n")
	b.WriteString("  - MATCH,🚀 节点选择\n")

	return b.String()
}

func renderSingBoxSubscription(domain string, nodes []model.Inbound) ([]byte, error) {
	nodeTags := make([]string, 0, len(nodes))
	nodeOutbounds := make([]map[string]any, 0, len(nodes))

	for _, n := range nodes {
		switch n.Type {
		case protocol.TypeVLESSReality:
			if n.VLESS == nil {
				continue
			}
			v := n.VLESS
			nodeTags = append(nodeTags, n.Name)
			nodeOutbounds = append(nodeOutbounds, map[string]any{
				"type":            "vless",
				"tag":             n.Name,
				"server":          domain,
				"server_port":     n.Port,
				"uuid":            v.UUID,
				"flow":            "xtls-rprx-vision",
				"packet_encoding": "xudp",
				"tls": map[string]any{
					"enabled":     true,
					"server_name": v.SNI,
					"utls": map[string]any{
						"enabled":     true,
						"fingerprint": "chrome",
					},
					"reality": map[string]any{
						"enabled":    true,
						"public_key": v.PublicKey,
						"short_id":   v.ShortID,
					},
				},
			})
		case protocol.TypeHysteria2:
			if n.Hysteria2 == nil {
				continue
			}
			h := n.Hysteria2
			nodeTags = append(nodeTags, n.Name)
			out := map[string]any{
				"type":        "hysteria2",
				"tag":         n.Name,
				"server":      domain,
				"server_port": n.Port,
				"password":    h.Password,
				"tls": map[string]any{
					"enabled":     true,
					"server_name": domain,
					"alpn":        []string{"h3"},
				},
			}
			if h.Obfs != "" {
				out["obfs"] = map[string]any{
					"type":     h.Obfs,
					"password": h.ObfsPassword,
				}
			}
			nodeOutbounds = append(nodeOutbounds, out)
		}
	}

	outbounds := make([]any, 0, len(nodeOutbounds)+3)
	if len(nodeTags) > 0 {
		selectChoices := make([]string, 0, len(nodeTags)+2)
		selectChoices = append(selectChoices, "⚡ 自动优选")
		selectChoices = append(selectChoices, nodeTags...)
		selectChoices = append(selectChoices, "direct")
		outbounds = append(outbounds,
			map[string]any{
				"type":      "selector",
				"tag":       "🚀 节点选择",
				"outbounds": selectChoices,
				"default":   "⚡ 自动优选",
			},
			map[string]any{
				"type":      "urltest",
				"tag":       "⚡ 自动优选",
				"outbounds": nodeTags,
				"url":       "https://www.gstatic.com/generate_204",
				"interval":  "5m",
				"tolerance": 50,
			},
		)
	} else {
		outbounds = append(outbounds, map[string]any{
			"type":      "selector",
			"tag":       "🚀 节点选择",
			"outbounds": []string{"direct"},
			"default":   "direct",
		})
	}
	for _, item := range nodeOutbounds {
		outbounds = append(outbounds, item)
	}
	outbounds = append(outbounds, map[string]any{
		"type": "direct",
		"tag":  "direct",
	})

	doc := map[string]any{
		"log": map[string]any{
			"level":     "info",
			"timestamp": true,
		},
		// sing-box 1.13 rejects legacy DNS servers and inbound sniff fields, so
		// this client profile uses the 1.12+ formats.
		"dns": map[string]any{
			"servers": []any{
				map[string]any{
					"type":   "https",
					"tag":    "dns-remote",
					"server": "1.1.1.1",
					"detour": "🚀 节点选择",
				},
				map[string]any{
					"type":   "udp",
					"tag":    "dns-direct",
					"server": "223.5.5.5",
				},
			},
			"final":    "dns-remote",
			"strategy": "prefer_ipv4",
		},
		"inbounds": []any{
			map[string]any{
				"type":         "tun",
				"tag":          "tun-in",
				"address":      []string{"172.19.0.1/30", "fdfe:dcba:9876::1/126"},
				"auto_route":   true,
				"strict_route": true,
			},
			map[string]any{
				"type":        "mixed",
				"tag":         "mixed-in",
				"listen":      "127.0.0.1",
				"listen_port": 7890,
			},
		},
		"outbounds": outbounds,
		"route": map[string]any{
			"auto_detect_interface": true,
			// Node domains resolve directly; through dns-remote they would need
			// the node itself.
			"default_domain_resolver": "dns-direct",
			"final":                   "🚀 节点选择",
			"rules": []any{
				map[string]any{"action": "sniff"},
				map[string]any{
					"protocol": "dns",
					"action":   "hijack-dns",
				},
				map[string]any{
					"ip_is_private": true,
					"outbound":      "direct",
				},
			},
		},
	}

	return json.MarshalIndent(doc, "", "  ")
}
