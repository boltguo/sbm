package systeminfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcParsers(t *testing.T) {
	dir := t.TempDir()
	write := func(name, value string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	total, idle := readCPU(write("stat", "cpu  100 20 30 400 50 10 5 0\n"))
	if total != 615 || idle != 450 {
		t.Fatalf("cpu total=%d idle=%d", total, idle)
	}
	memoryTotal, memoryUsed := readMemory(write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 400 kB\n"))
	if memoryTotal != 1024000 || memoryUsed != 614400 {
		t.Fatalf("memory total=%d used=%d", memoryTotal, memoryUsed)
	}
	one, five, fifteen := readLoad(write("loadavg", "0.25 0.50 0.75 1/100 1\n"))
	if one != .25 || five != .5 || fifteen != .75 {
		t.Fatalf("load=%v/%v/%v", one, five, fifteen)
	}
	if uptime := readUptime(write("uptime", "12345.67 999.0\n")); uptime != 12345 {
		t.Fatalf("uptime=%d", uptime)
	}
	if osName := readOSName(write("os-release", "ID=test\nPRETTY_NAME=\"Example Linux 1\"\n")); osName != "Example Linux 1" {
		t.Fatalf("os=%q", osName)
	}
}

func TestPercent(t *testing.T) {
	if got := percent(60, 100); got != 60 {
		t.Fatalf("percent=%v", got)
	}
	if got := percent(120, 100); got != 100 {
		t.Fatalf("clamped percent=%v", got)
	}
}
