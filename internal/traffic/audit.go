package traffic

import (
	"bufio"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const minimumAuditBytes = 64 << 20

type AuditResult struct {
	Status        string    `json:"status"`
	Interface     string    `json:"interface,omitempty"`
	ProxyBytes    uint64    `json:"proxyBytes"`
	ReceiveBytes  uint64    `json:"receiveBytes"`
	TransmitBytes uint64    `json:"transmitBytes"`
	ReceiveRatio  float64   `json:"receiveRatio"`
	TransmitRatio float64   `json:"transmitRatio"`
	StartedAt     time.Time `json:"startedAt,omitempty"`
}

type networkSample struct {
	Interface     string
	ReceiveBytes  uint64
	TransmitBytes uint64
}

// NetworkAudit compares sing-box payload deltas with the public interface.
// It is deliberately advisory and never participates in quota enforcement.
type NetworkAudit struct {
	mu        sync.Mutex
	procRoot  string
	startedAt time.Time
	baseProxy uint64
	base      networkSample
	ready     bool
	now       func() time.Time
}

func NewNetworkAudit(procRoot string) *NetworkAudit {
	if procRoot == "" {
		procRoot = "/proc"
	}
	return &NetworkAudit{procRoot: procRoot, now: time.Now}
}

func (a *NetworkAudit) Check(proxyTotal int64) AuditResult {
	if proxyTotal < 0 {
		return AuditResult{Status: "unavailable"}
	}
	sample, err := readPublicInterface(a.procRoot)
	if err != nil {
		return AuditResult{Status: "unavailable"}
	}
	proxy := uint64(proxyTotal)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.ready || sample.Interface != a.base.Interface || sample.ReceiveBytes < a.base.ReceiveBytes || sample.TransmitBytes < a.base.TransmitBytes || proxy < a.baseProxy {
		a.base = sample
		a.baseProxy = proxy
		a.startedAt = a.now()
		a.ready = true
		return AuditResult{Status: "collecting", Interface: sample.Interface, StartedAt: a.startedAt}
	}
	result := AuditResult{
		Status:        "collecting",
		Interface:     sample.Interface,
		ProxyBytes:    proxy - a.baseProxy,
		ReceiveBytes:  sample.ReceiveBytes - a.base.ReceiveBytes,
		TransmitBytes: sample.TransmitBytes - a.base.TransmitBytes,
		StartedAt:     a.startedAt,
	}
	if result.ProxyBytes < minimumAuditBytes {
		return result
	}
	result.ReceiveRatio = roundedRatio(result.ReceiveBytes, result.ProxyBytes)
	result.TransmitRatio = roundedRatio(result.TransmitBytes, result.ProxyBytes)
	result.Status = "normal"
	if outsideAuditRange(result.ReceiveRatio) || outsideAuditRange(result.TransmitRatio) {
		result.Status = "different"
	}
	return result
}

func outsideAuditRange(value float64) bool { return value < .65 || value > 1.5 }

func roundedRatio(value, base uint64) float64 {
	if base == 0 {
		return 0
	}
	return math.Round(float64(value)/float64(base)*100) / 100
}

func readPublicInterface(procRoot string) (networkSample, error) {
	name, err := readDefaultInterface(filepath.Join(procRoot, "net", "route"))
	if err != nil {
		name, err = readDefaultIPv6Interface(filepath.Join(procRoot, "net", "ipv6_route"))
		if err != nil {
			return networkSample{}, err
		}
	}
	return readInterfaceCounters(filepath.Join(procRoot, "net", "dev"), name)
}

func readDefaultInterface(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 64)
		if err == nil && flags&1 != 0 {
			return fields[0], nil
		}
	}
	return "", errors.New("default IPv4 interface not found")
}

func readDefaultIPv6Interface(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 10 || fields[0] != strings.Repeat("0", 32) || fields[1] != "00" {
			continue
		}
		return fields[len(fields)-1], nil
	}
	return "", errors.New("default IPv6 interface not found")
}

func readInterfaceCounters(path, name string) (networkSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return networkSample{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		before, after, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(before) != name {
			continue
		}
		fields := strings.Fields(after)
		if len(fields) < 16 {
			break
		}
		receive, receiveErr := strconv.ParseUint(fields[0], 10, 64)
		transmit, transmitErr := strconv.ParseUint(fields[8], 10, 64)
		if receiveErr != nil || transmitErr != nil {
			break
		}
		return networkSample{Interface: name, ReceiveBytes: receive, TransmitBytes: transmit}, nil
	}
	return networkSample{}, errors.New("interface counters not found")
}
