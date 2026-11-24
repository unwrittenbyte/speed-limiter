// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "192.168.0.1", "100.64.0.1", "0.1.2.3", "::1", "fc00::1", "::ffff:127.0.0.1", "64:ff9b::7f00:1"} {
		if publicIP(net.ParseIP(s)) {
			t.Fatal("allowed private/reserved address", s)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public IP rejected")
	}
}
func TestProxy(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 64*1024)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials leaked")
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Write(body)
	})
	plain := httptest.NewServer(h)
	defer plain.Close()
	secure := httptest.NewTLSServer(h)
	defer secure.Close()
	e, err := newEngine(65536, "64 KB/s")
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	// ONLY unit tests redirect outgoing connections to controlled loopback fixtures.
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		var a string
		switch address {
		case "test.example:80":
			a = plain.Listener.Addr().String()
		case "test.example:443":
			a = secure.Listener.Addr().String()
		default:
			return nil, fmt.Errorf("unknown fixture")
		}
		return (&net.Dialer{}).DialContext(ctx, network, a)
	}
	e.transport.DialContext = e.dial
	p, _ := url.Parse("http://csl:" + e.password + "@" + e.listener.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(p), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	start := time.Now()
	var wg sync.WaitGroup
	for _, u := range []string{"http://test.example/", "https://test.example/"} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			resp, err := client.Get(u)
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			b, err := io.ReadAll(resp.Body)
			if err != nil || resp.StatusCode != 200 || !bytes.Equal(b, body) {
				t.Errorf("bad response %d %d %v", resp.StatusCode, len(b), err)
			}
		}(u)
	}
	wg.Wait()
	elapsed := time.Since(start)
	t.Logf("Concurrent HTTP + HTTPS, 128 KiB, shared 64 KiB/s: %v", elapsed)
	if elapsed < 1800*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("unexpected shared cap timing: %v", elapsed)
	}
	e.limiter.set(1024 * 1024)
	start = time.Now()
	r, err := client.Get("http://test.example/")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, r.Body)
	r.Body.Close()
	if time.Since(start) > time.Second {
		t.Fatal("live cap did not update")
	}
}

// Small reads and periodic stalls model wake-up delays and TLS fragmentation.
// The scheduler must preserve the long-term cap without losing every late tick.
func TestSustainedPacing(t *testing.T) {
	const rate = 2 * 1024 * 1024
	const chunk = 4096
	const count = 1024
	l := newLimiter(rate)
	start := time.Now()
	for i := 0; i < count; i++ {
		n, err := l.take(context.Background(), chunk)
		if err != nil || n != chunk {
			t.Fatalf("take: %d %v", n, err)
		}
		if i%16 == 15 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	elapsed := time.Since(start)
	t.Logf("4 MiB at 2 MiB/s, with periodic 10ms stalls: %v (%.3f MiB/s)", elapsed, 4/elapsed.Seconds())
	if elapsed < 1950*time.Millisecond || elapsed > 2300*time.Millisecond {
		t.Fatalf("unexpected pacing: %v", elapsed)
	}
}

func TestIdleCreditBound(t *testing.T) {
	l := newLimiter(2 * 1024 * 1024)
	l.next = time.Now().Add(-time.Hour)
	start := time.Now()
	for i := 0; i < 16; i++ {
		if _, err := l.take(context.Background(), 32768); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(start)
	// 512 KiB is 250 ms at cap; idle credit cannot skip more than 50 ms plus one chunk.
	if elapsed < 175*time.Millisecond || elapsed > 400*time.Millisecond {
		t.Fatalf("idle burst unbounded or too slow: %v", elapsed)
	}
}

// Change cap on the SAME open HTTP response, without reconnecting.
func TestToggleDuringDownload(t *testing.T) {
	for _, encrypted := range []bool{false, true} {
		t.Run(fmt.Sprintf("TLS=%v", encrypted), func(t *testing.T) { testToggleDuringDownload(t, encrypted) })
	}
}
func testToggleDuringDownload(t *testing.T, encrypted bool) {
	const phase = 256 * 1024
	gates := []chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(phase*3))
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		for _, gate := range gates {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
			if _, err := w.Write(bytes.Repeat([]byte("z"), phase)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	var fixture *httptest.Server
	scheme := "http"
	if encrypted {
		fixture = httptest.NewTLSServer(handler)
		scheme = "https"
	} else {
		fixture = httptest.NewServer(handler)
	}
	defer fixture.Close()
	e, err := newEngine(256*1024, "256 KB/s")
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fixture.Listener.Addr().String())
	}
	e.transport.DialContext = e.dial
	e.limiter.set(0)
	p, _ := url.Parse("http://csl:" + e.password + "@" + e.listener.Addr().String())
	tr := &http.Transport{Proxy: http.ProxyURL(p), TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 8 * time.Second}
	resp, err := client.Get(scheme + "://test.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for i, rate := range []int64{0, 256 * 1024, 0} {
		e.limiter.set(rate)
		start := time.Now()
		close(gates[i])
		n, err := io.CopyN(io.Discard, resp.Body, phase)
		elapsed := time.Since(start)
		if err != nil || n != phase {
			t.Fatalf("phase %d interrupted: %v (%d)", i, err, n)
		}
		t.Logf("Same response phase %d rate %d: %v", i, rate, elapsed)
		if rate == 0 && elapsed > 500*time.Millisecond {
			t.Fatal("unlimited throughput too slow")
		}
		if rate > 0 && (elapsed < 850*time.Millisecond || elapsed > 2*time.Second) {
			t.Fatal("cap not applied")
		}
	}
}

func TestVPNDestinationPolicy(t *testing.T) {
	for _, tc := range []struct {
		ip, host     string
		port         int
		compat, want bool
	}{
		{"198.18.8.77", "www.microsoft.com", 443, false, false},
		{"198.18.8.77", "www.microsoft.com", 443, true, true},
		{"198.19.255.254", "www.microsoft.com.", 80, true, true},
		{"198.18.8.77", "198.18.8.77", 443, true, false},
		{"198.18.8.77", "198.18.8.77.", 443, true, false},
		{"198.18.8.77", "admin.local", 443, true, false},
		{"198.18.8.77", "internal", 443, true, false},
		{"198.18.8.77", "www.microsoft.com", 22, true, false},
		{"192.0.0.88", "www.google.com", 443, false, false},
		{"192.0.0.88", "www.google.com", 443, true, true},
		{"192.0.0.88", "GOOGLE.COM.", 80, true, true},
		{"::ffff:192.0.0.88", "www.google.com", 443, true, true},
		{"192.0.0.88", "192.0.0.88", 443, true, false},
		{"192.0.0.88", "192.0.0.88.", 443, true, false},
		{"192.0.0.88", "google.com.attacker.example.org", 443, true, false},
		{"192.0.0.88", "notgoogle.com", 443, true, false},
		{"192.0.0.88", "admin.local", 443, true, false},
		{"192.0.0.88", "www.google.com", 22, true, false},
		{"192.0.0.89", "www.google.com", 443, true, false},
		{"192.0.0.8", "www.google.com", 443, true, false},
		{"192.0.0.170", "www.google.com", 443, true, false},
		{"192.168.1.1", "www.microsoft.com", 443, true, false},
		{"127.0.0.1", "www.microsoft.com", 443, true, false},
		{"169.254.169.254", "www.microsoft.com", 80, true, false},
		{"100.64.1.1", "www.microsoft.com", 443, true, false},
		{"198.51.100.1", "www.microsoft.com", 443, true, false},
		{"::ffff:127.0.0.1", "www.microsoft.com", 443, true, false},
		{"64:ff9b::7f00:1", "www.microsoft.com", 443, true, false},
		{"2.23.246.101", "www.microsoft.com", 443, false, true},
	} {
		if got := allowedDestination(net.ParseIP(tc.ip), tc.host, tc.port, tc.compat); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}

// Closing the engine must immediately close an idle hijacked CONNECT tunnel.
func TestCloseActiveTunnel(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer fixture.Close()
	e, err := newEngine(1024*1024, "1 MB/s")
	if err != nil {
		t.Fatal(err)
	}
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, fixture.Listener.Addr().String())
	}
	c, err := net.Dial("tcp", e.listener.Addr().String())
	if err != nil {
		e.close()
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(c, "CONNECT www.example.com:443 HTTP/1.1\r\nHost: www.example.com:443\r\nProxy-Authorization: Basic %s\r\n\r\n", base64.StdEncoding.EncodeToString([]byte("csl:"+e.password)))
	b := bufio.NewReader(c)
	resp, err := http.ReadResponse(b, &http.Request{Method: "CONNECT"})
	if err != nil || resp.StatusCode != 200 {
		e.close()
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	start := time.Now()
	e.close()
	_, err = b.ReadByte()
	if err == nil {
		t.Fatal("tunnel remained open")
	}
	if time.Since(start) > time.Second {
		t.Fatal("slow tunnel shutdown")
	}
	if x, err := net.DialTimeout("tcp", e.listener.Addr().String(), 100*time.Millisecond); err == nil {
		x.Close()
		t.Fatal("listener remained open")
	}
}

func TestGoogleOSResolver(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		compat, fail4, want4, wantAll bool
	}{
		{"www.google.com", true, false, true, false},
		{"google.com", true, true, true, true},
		{"www.google.com", false, false, false, true},
		{"www.microsoft.com", true, false, false, true},
		{"notgoogle.com", true, false, false, true},
	} {
		var called4, calledAll bool
		all := func(ctx context.Context, host string) ([]net.IPAddr, error) {
			calledAll = true
			return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
		}
		family := func(ctx context.Context, network, host string) ([]net.IP, error) {
			called4 = true
			if network != "ip4" || host != tc.name {
				t.Fatal("wrong resolver arguments")
			}
			if tc.fail4 {
				return nil, &net.DNSError{Err: "no A record", Name: host, IsNotFound: true}
			}
			return []net.IP{net.ParseIP("192.0.0.88")}, nil
		}
		ips, err := resolveTarget(context.Background(), tc.name, tc.compat, all, family)
		if err != nil || called4 != tc.want4 || calledAll != tc.wantAll || len(ips) != 1 || !allowedDestination(ips[0].IP, tc.name, 443, tc.compat) {
			t.Fatalf("%+v: %v %v %v %v", tc, ips, err, called4, calledAll)
		}
		if !calledAll && !ips[0].IP.Equal(nordGoogleIP) {
			t.Fatal("lost synthetic mapping")
		}
	}
	if !syntheticCandidate(net.ParseIP("192.0.0.88")) || syntheticCandidate(net.ParseIP("192.0.0.89")) {
		t.Fatal("synthetic counters wrong")
	}
}
