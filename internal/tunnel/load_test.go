package tunnel_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestTunnelLoad pushes concurrent HTTP traffic from the hub through the userspace WireGuard
// tunnel to a server on the node: small answers (a lookup) and 256 KiB ones (a document). Every
// request must succeed; it reports requests per second, latency and throughput. RID_LOAD_SECONDS
// makes it longer (default 3 s).
func TestTunnelLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("load test")
	}
	seconds := 3
	if s, err := strconv.Atoi(os.Getenv("RID_LOAD_SECONDS")); err == nil && s > 0 {
		seconds = s
	}
	node, dial := upNode(t)
	l, err := node.Listen(testPort)
	if err != nil {
		t.Fatal(err)
	}
	document := bytes.Repeat([]byte("x"), 256<<10)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/documento" {
			_, _ = w.Write(document)
			return
		}
		_, _ = io.WriteString(w, `{"numero_documento":"1000000001","estado":"activo"}`)
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	target := netip.AddrPortFrom(nodeAddr.Addr(), testPort)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext:         func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx, target) },
		MaxIdleConnsPerHost: 64,
	}}
	// Warm up: the first packet teaches the hub where the node is.
	if res, err := client.Get("http://node/"); err != nil {
		t.Fatal(err)
	} else {
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
	}

	const workers = 32
	var (
		mu        sync.Mutex
		latencies []time.Duration
		bytesIn   int64
		failures  int
		firstErr  error
	)
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				path := "/afiliados/1000000001"
				if i%10 == w%10 { // one request in ten carries a document
					path = "/documento"
				}
				t0 := time.Now()
				res, err := client.Get("http://node" + path)
				var n int64
				if err == nil {
					n, err = io.Copy(io.Discard, res.Body)
					res.Body.Close()
					if err == nil && res.StatusCode != http.StatusOK {
						err = fmt.Errorf("status %d", res.StatusCode)
					}
				}
				mu.Lock()
				if err != nil {
					failures++
					if firstErr == nil {
						firstErr = err
					}
				} else {
					latencies = append(latencies, time.Since(t0))
					bytesIn += n
				}
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	elapsed := time.Since(start)
	if failures > 0 {
		t.Fatalf("%d requests failed through the tunnel (first: %v)", failures, firstErr)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	pct := func(p float64) time.Duration { return latencies[int(float64(len(latencies)-1)*p)] }
	t.Logf("%d requests in %s with %d workers: %.0f req/s, p50 %s, p99 %s, %.1f MiB/s",
		len(latencies), elapsed.Round(time.Millisecond), workers, float64(len(latencies))/elapsed.Seconds(),
		pct(0.50).Round(time.Microsecond), pct(0.99).Round(time.Microsecond), float64(bytesIn)/elapsed.Seconds()/(1<<20))
	if len(latencies) < 100 {
		t.Fatalf("only %d requests in %s", len(latencies), elapsed)
	}
}
