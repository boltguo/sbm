package traffic

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/boltguo/sbm/internal/core"
	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

type GatewayCounters struct {
	TX, RX                     int64
	TXGeneration, RXGeneration string
}
type GatewaySampler interface {
	Sample(context.Context, []model.EgressGateway) (map[string]GatewayCounters, error)
}

// Accounting owns only two filter chains and their commented jump/rules.
// Every counter rule has no target, and the chain returns to INPUT/OUTPUT:
// neither ACCEPT/DROP decisions nor any existing firewall rule are changed.
type Accounting struct {
	Commands   core.Commander
	BootIDPath string
}

const txChain = "SBM_EGRESS_TX"
const rxChain = "SBM_EGRESS_RX"

type accountingRule struct {
	chain, id, direction, generation string
	bytes                            int64
	args                             []string
}
type accountingSnapshot struct {
	chains map[string]bool
	rules  []accountingRule
}

func parseAccounting(data []byte) (accountingSnapshot, error) {
	result := accountingSnapshot{chains: map[string]bool{}}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		if fields[0] == ":"+txChain || fields[0] == ":"+rxChain {
			result.chains[strings.TrimPrefix(fields[0], ":")] = true
			continue
		}
		if len(fields) < 5 || fields[1] != "-A" || (fields[2] != txChain && fields[2] != rxChain) {
			continue
		}
		args := append([]string(nil), fields[3:]...)
		for i := range args {
			args[i] = strings.Trim(args[i], `"`)
		}
		comment := ruleValue(args, "--comment")
		parts := strings.Split(comment, ":")
		if len(parts) != 4 || parts[0] != "sbm-egress" || (parts[2] != "tx" && parts[2] != "rx") || ruleValue(args, "-j") != "" || ruleValue(args, "-g") != "" {
			return result, errors.New("reserved accounting chain contains an unowned rule")
		}
		counters := strings.Split(strings.Trim(fields[0], "[]"), ":")
		if len(counters) != 2 {
			return result, errors.New("invalid accounting counters")
		}
		n, err := strconv.ParseInt(counters[1], 10, 64)
		if err != nil || n < 0 {
			return result, errors.New("invalid accounting counters")
		}
		result.rules = append(result.rules, accountingRule{chain: fields[2], id: parts[1], direction: parts[2], generation: parts[3], bytes: n, args: args})
	}
	return result, scanner.Err()
}
func ruleValue(args []string, key string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key {
			return args[i+1]
		}
	}
	return ""
}
func ruleMatches(r accountingRule, g model.EgressGateway, direction string) bool {
	chain, addr, port := txChain, "-d", "--dport"
	if direction == "rx" {
		chain, addr, port = rxChain, "-s", "--sport"
	}
	ip := strings.TrimSuffix(ruleValue(r.args, addr), "/32")
	return r.chain == chain && r.id == g.ID && r.direction == direction && ip == g.Server && ruleValue(r.args, port) == strconv.Itoa(g.ServerPort) && ruleValue(r.args, "-p") == "udp" && ruleValue(r.args, "-j") == "" && ruleValue(r.args, "-g") == ""
}
func (a *Accounting) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	commander := a.Commands
	if commander == nil {
		commander = core.ExecCommander{}
	}
	out, err := commander.Run(ctx, name, args...)
	if err != nil {
		return nil, errors.New("WireGuard accounting unavailable")
	}
	return out, nil
}
func (a *Accounting) snapshot(ctx context.Context) (accountingSnapshot, error) {
	data, err := a.run(ctx, "iptables-save", "-c", "-t", "filter")
	if err != nil {
		return accountingSnapshot{}, err
	}
	return parseAccounting(data)
}
func (a *Accounting) iptables(ctx context.Context, args ...string) error {
	_, err := a.run(ctx, "iptables", append([]string{"-w", "2", "-t", "filter"}, args...)...)
	return err
}
func (a *Accounting) Sample(ctx context.Context, gateways []model.EgressGateway) (map[string]GatewayCounters, error) {
	result := map[string]GatewayCounters{}
	desired := map[string]model.EgressGateway{}
	for _, g := range gateways {
		if !g.Enabled {
			continue
		}
		if err := protocol.ValidateGateway(g); err != nil {
			return nil, errors.New("invalid accounting gateway")
		}
		desired[g.ID] = g
	}
	snapshot, err := a.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	if len(desired) > 0 {
		for _, chain := range []string{txChain, rxChain} {
			if !snapshot.chains[chain] {
				if err := a.iptables(ctx, "-N", chain); err != nil {
					return nil, err
				}
			}
		}
	}
	// Preserve unchanged rules/counters; remove only our uniquely marked rules.
	kept := map[string]bool{}
	for _, r := range snapshot.rules {
		g, exists := desired[r.id]
		key := r.id + ":" + r.direction
		if exists && ruleMatches(r, g, r.direction) && !kept[key] {
			kept[key] = true
			continue
		}
		if err := a.iptables(ctx, append([]string{"-D", r.chain}, r.args...)...); err != nil {
			return nil, err
		}
	}
	for _, g := range gateways {
		if !g.Enabled {
			continue
		}
		for _, direction := range []string{"tx", "rx"} {
			if kept[g.ID+":"+direction] {
				continue
			}
			generation, err := protocol.RandomHex(8)
			if err != nil {
				return nil, errors.New("accounting generation unavailable")
			}
			chain, addr, port := txChain, "-d", "--dport"
			if direction == "rx" {
				chain, addr, port = rxChain, "-s", "--sport"
			}
			args := []string{"-A", chain, "-p", "udp", addr, g.Server, port, strconv.Itoa(g.ServerPort), "-m", "comment", "--comment", "sbm-egress:" + g.ID + ":" + direction + ":" + generation}
			if err := a.iptables(ctx, args...); err != nil {
				return nil, err
			}
		}
	}
	for i, chain := range []string{txChain, rxChain} {
		parent, direction := "OUTPUT", "tx"
		if i == 1 {
			parent, direction = "INPUT", "rx"
		}
		jump := []string{parent, "-m", "comment", "--comment", "sbm-egress-jump-" + direction, "-j", chain}
		// -C checks semantic equality and does not read locale-dependent output.
		checkErr := a.iptables(ctx, append([]string{"-C"}, jump...)...)
		if len(desired) > 0 && checkErr != nil {
			insert := append([]string{"-I", parent, "1"}, jump[1:]...)
			if err := a.iptables(ctx, insert...); err != nil {
				return nil, err
			}
		} else if len(desired) == 0 && checkErr == nil {
			if err := a.iptables(ctx, append([]string{"-D"}, jump...)...); err != nil {
				return nil, err
			}
		}
	}
	if len(desired) == 0 {
		return result, nil
	}
	snapshot, err = a.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	bootPath := a.BootIDPath
	if bootPath == "" {
		bootPath = "/proc/sys/kernel/random/boot_id"
	}
	boot, err := os.ReadFile(bootPath)
	if err != nil {
		return nil, errors.New("accounting boot generation unavailable")
	}
	seen := map[string]bool{}
	for _, r := range snapshot.rules {
		g, exists := desired[r.id]
		if !exists || !ruleMatches(r, g, r.direction) {
			continue
		}
		key := r.id + ":" + r.direction
		if seen[key] {
			return nil, errors.New("duplicate accounting rule")
		}
		seen[key] = true
		counters := result[r.id]
		generation := strings.TrimSpace(string(boot)) + ":" + r.generation
		if r.direction == "tx" {
			counters.TX = r.bytes
			counters.TXGeneration = generation
		} else {
			counters.RX = r.bytes
			counters.RXGeneration = generation
		}
		result[r.id] = counters
	}
	for id := range desired {
		if !seen[id+":tx"] || !seen[id+":rx"] {
			return nil, errors.New("accounting rule unavailable")
		}
	}
	return result, nil
}
