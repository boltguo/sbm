package systeminfo

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Snapshot struct {
	Hostname      string    `json:"hostname"`
	OS            string    `json:"os"`
	Kernel        string    `json:"kernel"`
	Architecture  string    `json:"architecture"`
	CPUCores      int       `json:"cpuCores"`
	CPUPercent    float64   `json:"cpuPercent"`
	Load1         float64   `json:"load1"`
	Load5         float64   `json:"load5"`
	Load15        float64   `json:"load15"`
	MemoryTotal   uint64    `json:"memoryTotal"`
	MemoryUsed    uint64    `json:"memoryUsed"`
	MemoryPercent float64   `json:"memoryPercent"`
	DiskTotal     uint64    `json:"diskTotal"`
	DiskUsed      uint64    `json:"diskUsed"`
	DiskPercent   float64   `json:"diskPercent"`
	UptimeSeconds int64     `json:"uptimeSeconds"`
	CollectedAt   time.Time `json:"collectedAt"`
}

type Collector struct {
	ProcRoot        string
	OSReleasePath   string
	DiskPath        string
	mu              sync.Mutex
	previousTotal   uint64
	previousIdle    uint64
	previousPercent float64
	staticOnce      sync.Once
	hostname        string
	osName          string
	kernel          string
}

func New() *Collector {
	return &Collector{ProcRoot: "/proc", OSReleasePath: "/etc/os-release", DiskPath: "/"}
}

func (c *Collector) Snapshot(ctx context.Context) Snapshot {
	c.staticOnce.Do(func() { c.loadStatic(ctx) })
	result := Snapshot{Hostname: c.hostname, OS: c.osName, Kernel: c.kernel, Architecture: runtime.GOARCH, CPUCores: runtime.NumCPU(), CollectedAt: time.Now()}
	result.CPUPercent = c.cpuPercent()
	result.Load1, result.Load5, result.Load15 = readLoad(filepath.Join(c.ProcRoot, "loadavg"))
	result.MemoryTotal, result.MemoryUsed = readMemory(filepath.Join(c.ProcRoot, "meminfo"))
	result.MemoryPercent = percent(result.MemoryUsed, result.MemoryTotal)
	result.UptimeSeconds = readUptime(filepath.Join(c.ProcRoot, "uptime"))
	result.DiskTotal, result.DiskUsed = readDisk(c.DiskPath)
	result.DiskPercent = percent(result.DiskUsed, result.DiskTotal)
	return result
}

func (c *Collector) loadStatic(ctx context.Context) {
	c.hostname, _ = os.Hostname()
	c.osName = readOSName(c.OSReleasePath)
	if c.osName == "" {
		c.osName = runtime.GOOS
	}
	commandCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(commandCtx, "uname", "-r").Output(); err == nil {
		c.kernel = strings.TrimSpace(string(output))
	}
}

func (c *Collector) cpuPercent() float64 {
	total, idle := readCPU(filepath.Join(c.ProcRoot, "stat"))
	if total == 0 {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var used, span uint64
	if c.previousTotal > 0 && total >= c.previousTotal && idle >= c.previousIdle {
		span = total - c.previousTotal
		if span == 0 {
			return c.previousPercent
		}
		idleSpan := idle - c.previousIdle
		if span >= idleSpan {
			used = span - idleSpan
		}
	} else if total >= idle {
		span, used = total, total-idle
	}
	c.previousTotal, c.previousIdle = total, idle
	c.previousPercent = percent(used, span)
	return c.previousPercent
}

func readCPU(path string) (uint64, uint64) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return 0, 0
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	values := make([]uint64, 0, len(fields)-1)
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return 0, 0
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return total, idle
}

func readMemory(path string) (uint64, uint64) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer file.Close()
	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		if key != "MemTotal" && key != "MemAvailable" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			values[key] = value * 1024
		}
	}
	total, available := values["MemTotal"], values["MemAvailable"]
	if total < available {
		return total, 0
	}
	return total, total - available
}

func readLoad(path string) (float64, float64, float64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0
	}
	one, _ := strconv.ParseFloat(fields[0], 64)
	five, _ := strconv.ParseFloat(fields[1], 64)
	fifteen, _ := strconv.ParseFloat(fields[2], 64)
	return one, five, fifteen
}

func readUptime(path string) int64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	value, _ := strconv.ParseFloat(fields[0], 64)
	return int64(value)
}

func readOSName(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			value := strings.TrimPrefix(line, "PRETTY_NAME=")
			if unquoted, err := strconv.Unquote(value); err == nil {
				return unquoted
			}
			return strings.Trim(value, "'\"")
		}
	}
	return ""
}

func readDisk(path string) (uint64, uint64) {
	var stat syscall.Statfs_t
	if syscall.Statfs(path, &stat) != nil {
		return 0, 0
	}
	blockSize := uint64(stat.Bsize)
	total := stat.Blocks * blockSize
	available := stat.Bavail * blockSize
	if total < available {
		return total, 0
	}
	return total, total - available
}

func percent(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	value := float64(used) / float64(total) * 100
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	parsed, _ := strconv.ParseFloat(fmt.Sprintf("%.1f", value), 64)
	return parsed
}
