package traffic

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/protocol"
)

func TestParseRawAccountingCounters(t *testing.T) {
	raw := `*filter
:INPUT DROP [123:456]
:SBM_EGRESS_TX - [0:0]
:SBM_EGRESS_RX - [0:0]
[5:1234567890] -A SBM_EGRESS_TX -d 203.0.113.1/32 -p udp -m udp --dport 51820 -m comment --comment "sbm-egress:aws:tx:abc123"
[7:987654321] -A SBM_EGRESS_RX -s 203.0.113.1/32 -p udp -m udp --sport 51820 -m comment --comment "sbm-egress:aws:rx:def456"
[9:123] -A INPUT -m comment --comment "a user rule" -j DROP
COMMIT
`
	snapshot, err := parseAccounting([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.rules) != 2 || snapshot.rules[0].bytes != 1234567890 || snapshot.rules[1].bytes != 987654321 || !snapshot.chains[txChain] {
		t.Fatal("raw counters incorrectly parsed")
	}
	g := model.EgressGateway{ID: "aws", Server: "203.0.113.1", ServerPort: 51820}
	if !ruleMatches(snapshot.rules[0], g, "tx") || !ruleMatches(snapshot.rules[1], g, "rx") {
		t.Fatal("saved rule failed semantic match")
	}
	snapshot.rules[0].args = append(snapshot.rules[0].args, "-j", "ACCEPT")
	if ruleMatches(snapshot.rules[0], g, "tx") {
		t.Fatal("verdict rule accepted as accounting")
	}
}

type accountingCommander struct {
	calls []string
	raw   []byte
}

func (c *accountingCommander) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	c.calls = append(c.calls, name+" "+strings.Join(args, " "))
	if name == "iptables-save" {
		return c.raw, nil
	}
	return nil, nil
}
func TestAccountingPreservesUnchangedRules(t *testing.T) {
	k, _ := protocol.GenerateWireGuardKeys()
	g := model.EgressGateway{ID: "aws", Enabled: true, TunnelSlot: 1, Server: "203.0.113.1", ServerPort: 51820, PrivateKey: k.Private, PeerPublicKey: k.Public, TrafficQuota: model.DefaultConfig().TrafficQuota, Reset: model.DefaultConfig().Reset}
	c := &accountingCommander{raw: []byte(":SBM_EGRESS_TX - [0:0]\n:SBM_EGRESS_RX - [0:0]\n[1:100] -A SBM_EGRESS_TX -d 203.0.113.1/32 -p udp -m udp --dport 51820 -m comment --comment sbm-egress:aws:tx:one\n[2:200] -A SBM_EGRESS_RX -s 203.0.113.1/32 -p udp -m udp --sport 51820 -m comment --comment sbm-egress:aws:rx:two\n")}
	boot := filepath.Join(t.TempDir(), "boot")
	if err := os.WriteFile(boot, []byte("boot-one"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := (&Accounting{Commands: c, BootIDPath: boot}).Sample(context.Background(), []model.EgressGateway{g})
	if err != nil {
		t.Fatal(err)
	}
	if result["aws"].TX != 100 || result["aws"].RX != 200 || result["aws"].TXGeneration != "boot-one:one" {
		t.Fatal("wrong counters/generation")
	}
	for _, call := range c.calls {
		if strings.Contains(call, " -A ") || strings.Contains(call, " -D ") || strings.Contains(call, " -F ") {
			t.Fatal("unchanged counters were modified")
		}
	}
}
func linuxCommand(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("isolated command %s failed: %v", name, err)
	}
	return out
}
func TestLinuxAccountingIntegration(t *testing.T) {
	if os.Getenv("SBM_TEST_IPTABLES") != "1" {
		t.Skip("run scripts/egress-linux-test.sh in an isolated Linux network namespace")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("integration requires isolated Linux root")
	}
	linuxCommand(t, "ip", "link", "set", "lo", "up")
	for _, ip := range []string{"203.0.113.1", "203.0.113.2"} {
		linuxCommand(t, "ip", "addr", "add", ip+"/32", "dev", "lo")
	}
	// Existing DROP semantics for unrelated traffic must survive all operations.
	linuxCommand(t, "iptables", "-A", "INPUT", "-s", "192.0.2.77", "-m", "comment", "--comment", "user-test-rule", "-j", "DROP")
	cfg := model.DefaultConfig()
	gateways := []model.EgressGateway{}
	for i, id := range []string{"aws", "jp"} {
		k, _ := protocol.GenerateWireGuardKeys()
		gateways = append(gateways, model.EgressGateway{ID: id, Enabled: true, TunnelSlot: i + 1, Server: "203.0.113." + strconv.Itoa(i+1), ServerPort: 51820, PrivateKey: k.Private, PeerPublicKey: k.Public, TrafficQuota: cfg.TrafficQuota, Reset: cfg.Reset})
	}
	a := &Accounting{}
	baseline, err := a.Sample(context.Background(), gateways)
	if err != nil {
		t.Fatal(err)
	}
	exchange := func(g model.EgressGateway) {
		server, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(g.Server), Port: g.ServerPort})
		if err != nil {
			t.Fatal(err)
		}
		defer server.Close()
		client, err := net.DialUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")}, &net.UDPAddr{IP: net.ParseIP(g.Server), Port: g.ServerPort})
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		_ = client.SetDeadline(time.Now().Add(time.Second))
		if _, err := client.Write(make([]byte, 128)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 256)
		_, addr, err := server.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := server.WriteToUDP(make([]byte, 64), addr); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	exchange(gateways[0])
	first, err := a.Sample(context.Background(), gateways)
	if err != nil {
		t.Fatal(err)
	}
	if first["aws"].TX != 156 || first["aws"].RX != 92 || first["jp"].TX != 0 || first["jp"].RX != 0 {
		t.Fatalf("independent raw UDP/IP counters incorrect: aws=%d/%d jp=%d/%d", first["aws"].TX, first["aws"].RX, first["jp"].TX, first["jp"].RX)
	}
	exchange(gateways[1])
	second, err := a.Sample(context.Background(), gateways)
	if err != nil {
		t.Fatal(err)
	}
	if second["jp"].TX != 156 || second["jp"].RX != 92 || second["aws"].TX != first["aws"].TX {
		t.Fatal("second gateway counters cross-contaminated")
	}
	// Rebuild only TX: keep RX and the other gateway's counters/generation.
	snapshot, err := a.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snapshot.rules {
		if r.id == "aws" && r.direction == "tx" {
			linuxCommand(t, "iptables", append([]string{"-D", r.chain}, r.args...)...)
		}
	}
	rebuilt, err := a.Sample(context.Background(), gateways)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt["aws"].TXGeneration == baseline["aws"].TXGeneration || rebuilt["aws"].RXGeneration != baseline["aws"].RXGeneration || rebuilt["jp"] != second["jp"] {
		t.Fatal("rule rebuild did not preserve other counters")
	}
	if _, err := a.Sample(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	saved := string(linuxCommand(t, "iptables-save", "-c", "-t", "filter"))
	if strings.Contains(saved, "sbm-egress:") || strings.Contains(saved, "sbm-egress-jump") {
		t.Fatal("deleted accounting rules remain")
	}
	if !strings.Contains(saved, "user-test-rule") || !strings.Contains(saved, "-j DROP") {
		t.Fatal("existing user firewall changed")
	}
	t.Log(fmt.Sprintf("two gateways: TX/RX 156/92 bytes each; reconstruction/deletion preserved user DROP rule"))
}

func TestAccountingRejectsUnownedVerdictsInReservedChain(t *testing.T) {
	for _, raw := range []string{
		"[1:2] -A SBM_EGRESS_TX -j ACCEPT\n",
		"[1:2] -A SBM_EGRESS_TX -m comment --comment sbm-egress:aws:tx:one -j DROP\n",
	} {
		if _, err := parseAccounting([]byte(raw)); err == nil {
			t.Fatal("unsafe existing chain accepted")
		}
	}
}
