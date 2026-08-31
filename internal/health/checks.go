package health

import (
	"bufio"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusOK      Status = "ok"
	StatusWarning Status = "warning"
	StatusError   Status = "error"
	StatusUnknown Status = "unknown"
)

type Check struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"`
	Status        Status     `json:"status"`
	Reason        string     `json:"reason"`
	CheckedAt     time.Time  `json:"checkedAt"`
	Protocol      string     `json:"protocol,omitempty"`
	Port          int        `json:"port,omitempty"`
	Percent       float64    `json:"percent,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	FailureSince  *time.Time `json:"failureSince,omitempty"`
	NextResetAt   *time.Time `json:"nextResetAt,omitempty"`
	Timezone      string     `json:"timezone,omitempty"`
}

type Endpoint struct {
	ID       string
	Kind     string
	Protocol string
	Port     int
}

func Disk(percent float64, total uint64, now time.Time) Check {
	check := Check{ID: "disk", Kind: "disk", Status: StatusOK, Reason: "disk_ok", CheckedAt: now, Percent: percent}
	switch {
	case total == 0:
		check.Status, check.Reason = StatusUnknown, "disk_unavailable"
	case percent >= 95:
		check.Status, check.Reason = StatusError, "disk_critical"
	case percent >= 85:
		check.Status, check.Reason = StatusWarning, "disk_high"
	}
	return check
}

func Certificate(path string, now time.Time) Check {
	check := Check{ID: "tls", Kind: "tls", Status: StatusUnknown, Reason: "certificate_unavailable", CheckedAt: now}
	if path == "" {
		return check
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return check
	}
	var certificate *x509.Certificate
	for rest := data; len(rest) > 0; {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		if block.Type != "CERTIFICATE" {
			continue
		}
		certificate, err = x509.ParseCertificate(block.Bytes)
		if err == nil {
			break
		}
	}
	if certificate == nil {
		check.Status, check.Reason = StatusError, "certificate_invalid"
		return check
	}
	expiresAt := certificate.NotAfter.UTC()
	check.ExpiresAt = &expiresAt
	remaining := certificate.NotAfter.Sub(now)
	switch {
	case remaining <= 0:
		check.Status, check.Reason = StatusError, "certificate_expired"
	case remaining <= 7*24*time.Hour:
		check.Status, check.Reason = StatusError, "certificate_critical"
	case remaining <= 30*24*time.Hour:
		check.Status, check.Reason = StatusWarning, "certificate_expiring"
	default:
		check.Status, check.Reason = StatusOK, "certificate_valid"
	}
	return check
}

func Listeners(procRoot string, endpoints []Endpoint, now time.Time) []Check {
	result := make([]Check, 0, len(endpoints))
	for _, endpoint := range endpoints {
		listening, err := isListening(procRoot, endpoint.Protocol, endpoint.Port)
		check := Check{
			ID: endpoint.ID, Kind: endpoint.Kind, Status: StatusOK, Reason: "listening", CheckedAt: now,
			Protocol: endpoint.Protocol, Port: endpoint.Port,
		}
		if err != nil {
			check.Status, check.Reason = StatusUnknown, "listener_unavailable"
		} else if !listening {
			check.Status, check.Reason = StatusError, "not_listening"
		}
		result = append(result, check)
	}
	return result
}

func isListening(procRoot, network string, port int) (bool, error) {
	if network != "tcp" && network != "udp" {
		return false, fmt.Errorf("unsupported network %q", network)
	}
	readAny := false
	for _, name := range []string{network, network + "6"} {
		file, err := os.Open(filepath.Join(procRoot, "net", name))
		if err != nil {
			continue
		}
		readAny = true
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 4 || fields[0] == "sl" {
				continue
			}
			colon := strings.LastIndexByte(fields[1], ':')
			if colon < 0 {
				continue
			}
			parsedPort, parseErr := strconv.ParseUint(fields[1][colon+1:], 16, 16)
			if parseErr != nil || int(parsedPort) != port {
				continue
			}
			state := strings.ToUpper(fields[3])
			if (network == "tcp" && state == "0A") || (network == "udp" && state == "07") {
				file.Close()
				return true, nil
			}
		}
		file.Close()
	}
	if !readAny {
		return false, fmt.Errorf("listener tables unavailable")
	}
	return false, nil
}

func Overall(checks []Check) Status {
	overall := StatusOK
	for _, check := range checks {
		switch check.Status {
		case StatusError:
			return StatusError
		case StatusWarning:
			overall = StatusWarning
		case StatusUnknown:
			if overall == StatusOK {
				overall = StatusUnknown
			}
		}
	}
	return overall
}
