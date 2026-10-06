package traffic

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/boltguo/sbm/internal/model"
	"github.com/boltguo/sbm/internal/nettraffic"
)

func TestNativeVnStatAccounting(t *testing.T) {
	config, daemon := os.Getenv("SBM_VNSTAT_TEST_CONFIG"), os.Getenv("SBM_VNSTAT_TEST_DAEMON")
	if config == "" || daemon == "" {
		t.Skip("requires isolated native vnStat fixture")
	}
	cfg := model.DefaultConfig()
	cfg.VnStatInterface = "stat0"
	cfg.TrafficQuota = model.TrafficQuotaConfig{Amount: 0.000064, Unit: "GB", BillingMode: "single"}
	source := &configSource{cfg: cfg}
	core := &fakeCore{running: true}
	dir := t.TempDir()
	tracker, err := OpenWithHistory(filepath.Join(dir, "state.json"), filepath.Join(dir, "traffic.db"), source, core, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer tracker.Close()
	tracker.NetworkReader = nettraffic.Collector{ConfigPath: config}
	if err := tracker.SampleNetwork(context.Background(), "entry"); err != nil {
		t.Fatal(err)
	}
	if tracker.NetworkState("entry").TX != 0 {
		t.Fatal("fresh baseline imported prior source traffic")
	}
	cmd := exec.Command(daemon, "-n", "--config", config)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	time.Sleep(3 * time.Second)
	if out, err := exec.Command("python3", "-c", `import socket,json,subprocess,time
links={x['ifname']:bytes.fromhex(x['address'].replace(':','')) for x in json.loads(subprocess.check_output(['ip','-j','link','show']))}
for src,dst in [('stat0','stat1'),('stat1','stat0')]:
 s=socket.socket(socket.AF_PACKET,socket.SOCK_RAW);s.bind((src,0))
 frame=links[dst]+links[src]+b'\x88\xb5'+b'x'*1024
 for i in range(200): s.send(frame);time.sleep(.0005)
 s.close()
`).CombinedOutput(); err != nil {
		t.Fatalf("real frame transfer: %s %v", out, err)
	}
	time.Sleep(3 * time.Second)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := tracker.SampleNetwork(context.Background(), "entry"); err != nil {
		t.Fatal(err)
	}
	st := tracker.NetworkState("entry")
	if st.RX < 200_000 || st.TX < 200_000 || !tracker.State().QuotaExceeded || core.running {
		t.Fatalf("native quota path: %+v core=%+v", st, core)
	}
	date := st.UpdatedAt.UTC().Format(time.DateOnly)
	h, err := tracker.NetworkHistory(context.Background(), "entry", "day", date, date)
	if err != nil || len(h.Rows) != 1 || h.Rows[0].NetworkTotal != st.RX+st.TX {
		t.Fatalf("native history differs from baseline: %+v %v", h, err)
	}
	source.cfg.TrafficQuota.Amount = 0
	if err := tracker.ReconcileQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tracker.State().QuotaExceeded || !core.running {
		t.Fatal("unlimited did not restore core")
	}
	t.Logf("native vnStat persisted RX=%d TX=%d; quota stopped and unlimited restored core", st.RX, st.TX)
}
