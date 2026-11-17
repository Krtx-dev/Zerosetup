package readiness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Real /proc/net/tcp and tcp6 excerpts (header + rows).
const procNet = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000000000000000 100 0 0 10 0
   1: 00000000:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0100007F:1435 0200007F:A000 01 00000000:00000000 00:00000000 00000000  1000        0 12347 1 0000000000000000 100 0 0 10 0
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:1F40 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 2 1 0000000000000000 100 0 0 10 0
   1: 00000000000000000000000001000000:2328 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 3 1 0000000000000000 100 0 0 10 0
`

func TestParseProcNetTCP(t *testing.T) {
	ls := ParseProcNetTCP([]byte(procNet))
	want := []Listener{
		{Addr: "127.0.0.1", Port: 3000, Loopback: true},
		{Addr: "0.0.0.0", Port: 5173},
		{Addr: "::", Port: 8000},
		{Addr: "::1", Port: 9000, Loopback: true},
	}
	if len(ls) != len(want) {
		t.Fatalf("got %+v", ls)
	}
	for i := range want {
		if ls[i] != want[i] {
			t.Errorf("listener %d: got %+v want %+v", i, ls[i], want[i])
		}
	}
	if Evaluate(ls, 3000) != PortLoopbackOnly {
		t.Error("3000 should be loopback-only")
	}
	if Evaluate(ls, 5173) != PortReachable || Evaluate(ls, 8000) != PortReachable {
		t.Error("5173 and 8000 should be reachable")
	}
	if Evaluate(ls, 4000) != PortNotListening {
		t.Error("4000 should not be listening")
	}
	// The established connection on 5173 (state 01) must not count.
	if len(ParseProcNetTCP([]byte("   2: 0100007F:1435 0200007F:A000 01 0 0 0 0 0"))) != 0 {
		t.Error("non-LISTEN sockets must be ignored")
	}
}

func TestProbeHTTPAndHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("hello world"))
		case "/redirect":
			http.Redirect(w, r, "https://example.com/", http.StatusFound)
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	r := ProbeHTTP(ctx, srv.URL+"/ok", time.Second)
	if r.Status != 200 || !Healthy(r, nil, "") || !Healthy(r, nil, "world") || Healthy(r, nil, "absent") {
		t.Fatalf("ok probe: %+v", r)
	}
	if r := ProbeHTTP(ctx, srv.URL+"/redirect", time.Second); r.Status != 302 {
		t.Fatalf("redirects must not be followed: %+v", r)
	}
	r = ProbeHTTP(ctx, srv.URL+"/fail", time.Second)
	if r.Status != 500 || Healthy(r, nil, "") {
		t.Fatalf("500 must be unhealthy: %+v", r)
	}
	if !Healthy(r, []int{500}, "") {
		t.Fatal("explicit expected status must be honoured")
	}
	if r := ProbeHTTP(ctx, "http://127.0.0.1:1/", 200*time.Millisecond); r.Err == "" || r.Status != 0 {
		t.Fatalf("connection refused must be an error: %+v", r)
	}
}

func TestBackoff(t *testing.T) {
	b := NewBackoff(time.Second)
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second, time.Second}
	for i, w := range want {
		if got := b.Next(); got != w {
			t.Fatalf("step %d: %v want %v", i, got, w)
		}
	}
}
