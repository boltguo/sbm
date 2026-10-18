package core

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

// gatewayDNSServer answers gateway traffic through the tunnel. Its anycast
// address reaches a resolver near the gateway, wherever that is.
const gatewayDNSServer = "1.1.1.1"

type Renderer struct {
	Registry     *protocol.Registry
	BuildContext protocol.BuildContext
}

func (r Renderer) Render(cfg model.Config) ([]byte, error) {
	if err := r.Registry.ValidateConfig(cfg); err != nil {
		return nil, err
	}
	buildContext := r.BuildContext
	buildContext.EgressGateways = model.OrderedGateways(cfg.EgressGateways)
	// Display order must not change authentication, routes, or core bytes.
	sort.Slice(buildContext.EgressGateways, func(i, j int) bool {
		return buildContext.EgressGateways[i].ID < buildContext.EgressGateways[j].ID
	})
	inbounds := make([]any, 0, len(cfg.Inbounds))
	for _, inbound := range cfg.Inbounds {
		if !inbound.Enabled {
			continue
		}
		driver, _ := r.Registry.Get(inbound.Type)
		built, err := driver.Build(inbound, buildContext)
		if err != nil {
			return nil, fmt.Errorf("生成 %s 配置: %w", inbound.Name, err)
		}
		inbounds = append(inbounds, built)
	}
	direct := map[string]any{"type": "direct", "tag": "direct"}
	// Sniff only IP destinations. Sniffing replaces a client-supplied domain
	// with the TLS SNI or HTTP Host, so an ECH outer name (cloudflare-ech.com)
	// or any SNI that differs from the requested host would miss app routes
	// and leave through the node's exit instead.
	rules := []any{map[string]any{"ip_cidr": []string{"0.0.0.0/0", "::/0"}, "action": "sniff", "timeout": "300ms"}}
	route := map[string]any{"rules": rules, "final": "direct"}
	doc := map[string]any{
		"log":       map[string]any{"level": "warn", "timestamp": true},
		"inbounds":  inbounds,
		"outbounds": []any{direct},
		"route":     route,
		"experimental": map[string]any{"clash_api": map[string]any{
			"external_controller": "127.0.0.1:9090", "secret": cfg.ClashAPISecret,
		}},
	}
	localDNS := map[string]any{"type": "local", "tag": "local"}
	dnsServers := []any{localDNS}
	endpoints := []any{}
	gatewayRules := []any{}
	enabledGateways := make(map[string]model.EgressGateway, len(buildContext.EgressGateways))
	for _, g := range buildContext.EgressGateways {
		if !g.Enabled {
			continue
		}
		enabledGateways[g.ID] = g
		endpoints = append(endpoints, map[string]any{
			"type": "wireguard", "tag": g.EndpointTag(), "system": false, "mtu": 1408,
			"address": []string{g.TunnelAddress()}, "private_key": g.PrivateKey,
			"peers": []any{map[string]any{"address": g.Server, "port": g.ServerPort, "public_key": g.PeerPublicKey, "allowed_ips": []string{"0.0.0.0/0"}, "persistent_keepalive_interval": 25}},
		})
		// Resolve through the tunnel so geo-aware DNS answers for the gateway's
		// location, not the entry's.
		dnsServers = append(dnsServers, map[string]any{"type": "udp", "tag": g.DNSTag(), "server": gatewayDNSServer, "detour": g.EndpointTag()})
		users := []string{}
		for _, in := range cfg.Inbounds {
			if in.Enabled {
				users = append(users, protocol.EgressAuthUser(in, g.ID))
			}
		}
		if len(users) > 0 {
			gatewayRules = append(gatewayRules, map[string]any{"auth_user": users, "action": "resolve", "server": g.DNSTag(), "strategy": "ipv4_only"}, map[string]any{"auth_user": users, "action": "route", "outbound": g.EndpointTag()})
		}
	}
	if len(endpoints) > 0 {
		rules = append(rules, buildAppRouteRules(cfg.AppRoutes, enabledGateways)...)
		rules = append(rules, gatewayRules...)
		doc["endpoints"] = endpoints
		doc["dns"] = map[string]any{"servers": dnsServers}
		// Outbounds without their own resolver keep using the entry's system
		// DNS; sing-box requires naming it once there is more than one server.
		route["default_domain_resolver"] = "local"
		route["rules"] = rules
	}

	if cfg.OutboundStrategy != "" && cfg.OutboundStrategy != model.OutboundStrategyAuto {
		if _, ok := doc["dns"]; !ok {
			doc["dns"] = map[string]any{"servers": []any{localDNS}}
		}
		direct["domain_resolver"] = map[string]any{"server": "local", "strategy": cfg.OutboundStrategy}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func buildAppRouteRules(appRoutes map[string]string, enabledGateways map[string]model.EgressGateway) []any {
	if len(appRoutes) == 0 {
		return nil
	}
	resolveTarget := func(target string) (outboundTag string, isGateway bool, ok bool) {
		if target == model.AppRouteDirect {
			return "direct", false, true
		}
		if g, exists := enabledGateways[target]; exists {
			return g.EndpointTag(), true, true
		}
		return "", false, false
	}
	dnsTag := func(target string) string { return enabledGateways[target].DNSTag() }

	type routeGroup struct {
		target string
		items  []string
	}

	specs := model.AppRuleSpecs()
	var domainSteps []routeGroup
	for _, spec := range specs {
		target := appRoutes[spec.ID]
		if _, _, ok := resolveTarget(target); ok && len(spec.Subdomains) > 0 {
			domainSteps = append(domainSteps, routeGroup{target: target, items: spec.Subdomains})
		}
	}
	for _, spec := range specs {
		if spec.Category == "ecosystem" || spec.Category == "cdn" {
			continue
		}
		target := appRoutes[spec.ID]
		if _, _, ok := resolveTarget(target); ok && len(spec.Domains) > 0 {
			domainSteps = append(domainSteps, routeGroup{target: target, items: spec.Domains})
		}
	}
	var siteKeys []string
	for k, target := range appRoutes {
		if strings.HasPrefix(k, "site:") {
			if _, _, ok := resolveTarget(target); ok {
				siteKeys = append(siteKeys, k)
			}
		}
	}
	sort.Strings(siteKeys)
	for _, k := range siteKeys {
		domain := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(k, "site:")))
		if domain != "" {
			domainSteps = append(domainSteps, routeGroup{target: appRoutes[k], items: []string{domain}})
		}
	}
	for _, spec := range specs {
		if spec.Category != "ecosystem" && spec.Category != "cdn" {
			continue
		}
		target := appRoutes[spec.ID]
		if _, _, ok := resolveTarget(target); ok && len(spec.Domains) > 0 {
			domainSteps = append(domainSteps, routeGroup{target: target, items: spec.Domains})
		}
	}

	var mergedDomains []routeGroup
	seenDomains := map[string]bool{}
	for _, step := range domainSteps {
		var unique []string
		for _, d := range step.items {
			d = strings.ToLower(strings.TrimSpace(d))
			if d != "" && !seenDomains[d] {
				seenDomains[d] = true
				unique = append(unique, d)
			}
		}
		if len(unique) == 0 {
			continue
		}
		if n := len(mergedDomains); n > 0 && mergedDomains[n-1].target == step.target {
			mergedDomains[n-1].items = append(mergedDomains[n-1].items, unique...)
		} else {
			mergedDomains = append(mergedDomains, routeGroup{target: step.target, items: unique})
		}
	}

	var out []any
	for _, g := range mergedDomains {
		outboundTag, isGateway, _ := resolveTarget(g.target)
		if isGateway {
			out = append(out,
				map[string]any{"domain_suffix": g.items, "action": "resolve", "server": dnsTag(g.target), "strategy": "ipv4_only"},
				map[string]any{"domain_suffix": g.items, "action": "route", "outbound": outboundTag},
			)
		} else {
			out = append(out,
				map[string]any{"domain_suffix": g.items, "action": "route", "outbound": outboundTag},
			)
		}
	}

	// Gateway tunnels carry IPv4 only, so a gateway-routed app's IPv6 ranges
	// are rejected (target "") instead of falling through to the node's own
	// exit: the app never leaves through an egress other than the chosen one.
	var mergedCIDRs []routeGroup
	addCIDRs := func(target string, items []string) {
		if len(items) == 0 {
			return
		}
		if n := len(mergedCIDRs); n > 0 && mergedCIDRs[n-1].target == target {
			mergedCIDRs[n-1].items = append(mergedCIDRs[n-1].items, items...)
		} else {
			mergedCIDRs = append(mergedCIDRs, routeGroup{target: target, items: items})
		}
	}
	seenCIDRs := map[string]bool{}
	for _, spec := range specs {
		target := appRoutes[spec.ID]
		_, isGateway, ok := resolveTarget(target)
		if !ok || len(spec.CIDRs) == 0 {
			continue
		}
		var routed, rejected []string
		for _, p := range spec.CIDRs {
			cidr := p.String()
			if seenCIDRs[cidr] {
				continue
			}
			seenCIDRs[cidr] = true
			if isGateway && !p.Addr().Is4() {
				rejected = append(rejected, cidr)
			} else {
				routed = append(routed, cidr)
			}
		}
		addCIDRs(target, routed)
		addCIDRs("", rejected)
	}
	for _, g := range mergedCIDRs {
		if g.target == "" {
			out = append(out, map[string]any{"ip_cidr": g.items, "action": "reject"})
			continue
		}
		outboundTag, _, _ := resolveTarget(g.target)
		out = append(out, map[string]any{
			"ip_cidr":  g.items,
			"action":   "route",
			"outbound": outboundTag,
		})
	}
	return out
}
