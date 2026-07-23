package traffic

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func writeNetworkFixture(t *testing.T, root string, receive, transmit uint64) {
	t.Helper()
	netDir := filepath.Join(root, "net")
	if err := os.MkdirAll(netDir, 0700); err != nil {
		t.Fatal(err)
	}
	route := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\neth0 00000000 0100000A 0003 0 0 0 00000000 0 0 0\n"
	dev := "Inter-| Receive | Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n" +
		"eth0: " + strconv.FormatUint(receive, 10) + " 0 0 0 0 0 0 0 " + strconv.FormatUint(transmit, 10) + " 0 0 0 0 0 0 0\n"
	if err := os.WriteFile(filepath.Join(netDir, "route"), []byte(route), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "dev"), []byte(dev), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkAuditNormalAndDifferent(t *testing.T) {
	const base = uint64(1 << 30)
	const observed = uint64(minimumAuditBytes)
	root := t.TempDir()
	writeNetworkFixture(t, root, base, base)
	audit := NewNetworkAudit(root)
	if result := audit.Check(1000); result.Status != "collecting" {
		t.Fatalf("initial status=%q", result.Status)
	}
	writeNetworkFixture(t, root, base+observed, base+observed+observed/10)
	result := audit.Check(int64(1000 + observed))
	if result.Status != "normal" || result.ReceiveRatio != 1 || result.TransmitRatio != 1.1 {
		t.Fatalf("normal audit=%+v", result)
	}

	root = t.TempDir()
	writeNetworkFixture(t, root, base, base)
	audit = NewNetworkAudit(root)
	_ = audit.Check(1000)
	writeNetworkFixture(t, root, base+observed*2, base+observed)
	result = audit.Check(int64(1000 + observed))
	if result.Status != "different" {
		t.Fatalf("different audit=%+v", result)
	}
}

func TestNetworkAuditRestartsAfterCounterReset(t *testing.T) {
	root := t.TempDir()
	writeNetworkFixture(t, root, 1000, 1000)
	audit := NewNetworkAudit(root)
	_ = audit.Check(100)
	writeNetworkFixture(t, root, 10, 20)
	if result := audit.Check(110); result.Status != "collecting" || result.ProxyBytes != 0 {
		t.Fatalf("reset audit=%+v", result)
	}
}

func TestNetworkAuditUnavailableWithoutDefaultRoute(t *testing.T) {
	root := t.TempDir()
	if result := NewNetworkAudit(root).Check(0); result.Status != "unavailable" {
		t.Fatalf("status=%q", result.Status)
	}
}
