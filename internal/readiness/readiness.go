// Package readiness implements genuine application readiness checks:
// in-container listening-socket inspection and host-side HTTP probes.
package readiness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Listener is a TCP socket in LISTEN state inside the container.
type Listener struct {
	Addr string `json:"addr"`
	Port int    `json:"port"`
	// Loopback is true for 127.0.0.0/8 and ::1 — unreachable through
	// Docker port publishing.
	Loopback bool `json:"loopback"`
}

func (l Listener) String() string {
	if strings.Contains(l.Addr, ":") {
		return fmt.Sprintf("[%s]:%d", l.Addr, l.Port)
	}
	return fmt.Sprintf("%s:%d", l.Addr, l.Port)
}

// ProbeCommand reads the kernel socket tables. Run via exec as the
// container's unprivileged user; it needs no capabilities.
var ProbeCommand = []string{"cat", "/proc/net/tcp", "/proc/net/tcp6"}

const stateListen = "0A"

// ParseProcNetTCP parses /proc/net/tcp and /proc/net/tcp6 content
// (concatenated) and returns listening sockets, de-duplicated and sorted.
func ParseProcNetTCP(data []byte) []Listener {
	seen := map[string]bool{}
	var out []Listener
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] == "sl" || f[3] != stateListen {
			continue
		}
		hostHex, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		if err != nil {
			continue
		}
		addr, ok := decodeAddr(hostHex)
		if !ok {
			continue
		}
		l := Listener{Addr: addr.String(), Port: int(port), Loopback: addr.IsLoopback() || isMappedLoopback(addr)}
		if k := l.String(); !seen[k] {
			seen[k] = true
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Addr < out[j].Addr
	})
	return out
}

func isMappedLoopback(a netip.Addr) bool { return a.Is4In6() && a.Unmap().IsLoopback() }

// decodeAddr decodes the kernel's hex address: each 32-bit word is in host
// (little-endian on all platforms Docker runs on) byte order.
func decodeAddr(h string) (netip.Addr, bool) {
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return netip.Addr{}, false
	}
	for i := 0; i < len(b); i += 4 {
		b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
	}
	if len(b) == 4 {
		return netip.AddrFrom4([4]byte(b)), true
	}
	return netip.AddrFrom16([16]byte(b)), true
}

// WithoutDockerDNS drops Docker's embedded DNS resolver, which listens on
// 127.0.0.11 inside every container attached to a user-defined network.
// It is infrastructure, not the application.
func WithoutDockerDNS(ls []Listener) []Listener {
	out := ls[:0:0]
	for _, l := range ls {
		if l.Addr != "127.0.0.11" {
			out = append(out, l)
		}
	}
	return out
}

// PortStatus summarises listeners for an expected port.
type PortStatus int

const (
	PortNotListening PortStatus = iota
	PortLoopbackOnly
	PortReachable
)

// Evaluate classifies the expected port given the listeners.
func Evaluate(ls []Listener, port int) PortStatus {
	st := PortNotListening
	for _, l := range ls {
		if l.Port != port {
			continue
		}
		if !l.Loopback {
			return PortReachable
		}
		st = PortLoopbackOnly
	}
	return st
}

// HTTPResult is one HTTP probe outcome.
type HTTPResult struct {
	Status   int           `json:"status"`
	Body     string        `json:"-"`
	Duration time.Duration `json:"durationNs"`
	Err      string        `json:"error,omitempty"`
}

var probeClient = &http.Client{
	// Never follow redirects: a redirect response already proves the server
	// is up, and following could leave loopback.
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// ProbeHTTP performs a single GET against url with the given timeout.
func ProbeHTTP(ctx context.Context, url string, timeout time.Duration) HTTPResult {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return HTTPResult{Err: err.Error()}
	}
	req.Header.Set("User-Agent", "zerosetup-readiness/1")
	resp, err := probeClient.Do(req)
	if err != nil {
		return HTTPResult{Err: err.Error(), Duration: time.Since(start)}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return HTTPResult{Status: resp.StatusCode, Body: string(body), Duration: time.Since(start)}
}

// Healthy reports whether r satisfies the expectation: explicit status
// list, or any 2xx/3xx when none is given, plus optional body substring.
func Healthy(r HTTPResult, expect []int, bodyContains string) bool {
	if r.Status == 0 {
		return false
	}
	ok := false
	if len(expect) == 0 {
		ok = r.Status >= 200 && r.Status < 400
	}
	for _, s := range expect {
		if s == r.Status {
			ok = true
		}
	}
	if ok && bodyContains != "" {
		ok = strings.Contains(r.Body, bodyContains)
	}
	return ok
}

// Backoff returns bounded exponential delays: 250ms doubling to max.
type Backoff struct {
	cur, max time.Duration
}

// NewBackoff returns a backoff capped at max.
func NewBackoff(max time.Duration) *Backoff { return &Backoff{cur: 250 * time.Millisecond, max: max} }

// Next returns the next delay.
func (b *Backoff) Next() time.Duration {
	d := b.cur
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return d
}
