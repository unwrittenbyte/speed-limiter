// SPDX-License-Identifier: GPL-3.0-only
// Chrome Speed Limiter: local authenticated forward proxy and native messaging host.
// No external dependencies, telemetry, TLS interception or persistent traffic logs.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const realm = "Chrome Speed Limiter"
const maxMessage = 1048576

var version = "1.0.6"
var numeric = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)$`)

func capBytes(v, u string) (int64, error) {
	m := map[string]float64{"KB/s": 1024, "MB/s": 1048576, "GB/s": 1073741824, "Mbps": 125000}
	v = strings.TrimSpace(v)
	n, e := strconv.ParseFloat(v, 64)
	b := n * m[u]
	if e != nil || len(v) > 24 || !numeric.MatchString(v) || math.IsNaN(b) || b < 1024 || b > 1073741824 {
		return 0, errors.New("Allowed range: 1 KB/s to 1 GB/s.")
	}
	return int64(b), nil
}

// One global paced scheduler. Allow bounded catch-up for timer/read/write delays.
// At most 50 ms of unused credit is retained, plus one small reservation.
type limiter struct {
	mu      sync.Mutex
	rate    int64
	next    time.Time
	changed chan struct{}
}

func newLimiter(rate int64) *limiter { return &limiter{rate: rate, changed: make(chan struct{})} }
func (l *limiter) set(rate int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rate == l.rate {
		return
	}
	l.rate = rate
	l.next = time.Time{}
	close(l.changed)
	l.changed = make(chan struct{})
}
func (l *limiter) take(ctx context.Context, max int) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		l.mu.Lock()
		rate := l.rate
		if rate == 0 {
			l.mu.Unlock()
			return max, nil
		}
		n := min(max, int(max64(1, min64(32768, rate/20))))
		now := time.Now()
		wait := l.next.Sub(now)
		ch := l.changed
		if wait <= 0 {
			// Keep the ideal timeline rather than resetting it after each
			// late wake-up. That would lose bandwidth every time the OS
			// schedules us a little late. Bound idle credit to 50 ms.
			base := l.next
			if base.IsZero() {
				base = now
			} else if earliest := now.Add(-50 * time.Millisecond); base.Before(earliest) {
				base = earliest
			}
			l.next = base.Add(time.Duration(float64(n) / float64(rate) * float64(time.Second)))
			l.mu.Unlock()
			return n, nil
		}
		l.mu.Unlock()
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ch:
			t.Stop()
		case <-ctx.Done():
			t.Stop()
			return 0, ctx.Err()
		}
	}
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type peerContextKey struct{}

type engine struct {
	peerCheck        func(net.Conn) bool
	peerCleanup      func()
	peerMode         bool
	verifiedPeers    atomic.Int64
	authChallenges   atomic.Int64
	dnsFailures      atomic.Int64
	connectFailures  atomic.Int64
	blockedFailures  atomic.Int64
	syntheticSeen    atomic.Int64
	syntheticAllowed atomic.Int64
	vpnCompat        bool

	server          *http.Server
	listener        net.Listener
	transport       *http.Transport
	dial            func(context.Context, string, string) (net.Conn, error)
	ctx             context.Context
	cancel          context.CancelFunc
	limiter         *limiter
	password, label string
	cap             int64
	limited         bool
	active          atomic.Int64
	received        atomic.Int64
	last            int64
	at              time.Time
	live            int64
	conns           sync.Map
	slots           chan struct{}
}

func newEngine(rate int64, label string) (*engine, error) {
	return newEngineWithVPN(rate, label, false)
}
func newEngineWithVPN(rate int64, label string, vpnCompat bool) (*engine, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	secret := make([]byte, 32)
	if _, err = rand.Read(secret); err != nil {
		ln.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &engine{listener: ln, ctx: ctx, cancel: cancel, limiter: newLimiter(rate), password: base64.RawURLEncoding.EncodeToString(secret), label: label, cap: rate, limited: true, at: time.Now(), slots: make(chan struct{}, 256)}
	e.vpnCompat = vpnCompat
	e.peerCheck, e.peerCleanup, e.peerMode = browserPeerCheck()
	e.dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		c, err := dialTarget(ctx, network, address, e.vpnCompat, func(allowed bool) {
			e.syntheticSeen.Add(1)
			if allowed {
				e.syntheticAllowed.Add(1)
			}
		})
		if err != nil {
			switch {
			case errors.Is(err, errDNS):
				e.dnsFailures.Add(1)
			case errors.Is(err, errBlocked):
				e.blockedFailures.Add(1)
			default:
				e.connectFailures.Add(1)
			}
		}
		return c, err
	}
	e.transport = &http.Transport{Proxy: nil, DialContext: e.dial, DisableCompression: true, MaxIdleConns: 32, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: 45 * time.Second, ExpectContinueTimeout: time.Second}
	e.server = &http.Server{Handler: e, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		trusted := e.peerCheck(c)
		if trusted {
			e.verifiedPeers.Add(1)
		}
		return context.WithValue(ctx, peerContextKey{}, trusted)
	}, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 65536, BaseContext: func(net.Listener) context.Context { return ctx }, ConnState: func(c net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			e.conns.Store(c, true)
		case http.StateClosed:
			e.conns.Delete(c)
		}
	}}
	go e.server.Serve(ln)
	return e, nil
}
func (e *engine) close() {
	e.cancel()
	// Stop accepting and close existing sockets before waiting on server cleanup.
	e.listener.Close()
	e.conns.Range(func(k, v any) bool { k.(net.Conn).Close(); return true })
	e.server.Close()
	e.transport.CloseIdleConnections()
	e.peerCleanup()
}
func (e *engine) status() map[string]any {
	now := time.Now()
	n := e.received.Load()
	if dt := now.Sub(e.at).Seconds(); dt >= 0.4 {
		e.live = int64(float64(n-e.last) / dt)
		e.last = n
		e.at = now
	}
	return map[string]any{"version": version, "engine_running": true, "limiting": e.limited, "cap_label": e.label, "cap_bytes_per_second": e.cap, "live_bytes_per_second": e.live, "active_connections": e.active.Load(), "total_received": n, "peer_verification": e.peerMode, "verified_peers": e.verifiedPeers.Load(), "auth_challenges": e.authChallenges.Load(), "dns_failures": e.dnsFailures.Load(), "connect_failures": e.connectFailures.Load(), "blocked_destinations": e.blockedFailures.Load(), "vpn_compat": e.vpnCompat, "synthetic_seen": e.syntheticSeen.Load(), "synthetic_allowed": e.syntheticAllowed.Load(), "port": e.listener.Addr().(*net.TCPAddr).Port}
}
func (e *engine) copyDown(ctx context.Context, w io.Writer, r io.Reader) error {
	buf := make([]byte, 32768)
	for {
		n, err := r.Read(buf)
		for off := 0; off < n; {
			size, x := e.limiter.take(ctx, n-off)
			if x != nil {
				return x
			}
			k, x := w.Write(buf[off : off+size])
			e.received.Add(int64(k))
			off += k
			if x != nil {
				return x
			}
			if k != size {
				return io.ErrShortWrite
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if err != nil {
			return err
		}
	}
}

// Public Internet only. Resolve once and dial the checked IP to avoid DNS rebinding.
var blocked = []string{"0.0.0.0/8", "100.64.0.0/10", "192.88.99.0/24", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"}
var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range blocked {
		_, n, _ := net.ParseCIDR(s)
		out = append(out, n)
	}
	return out
}()

func publicIP(ip net.IP) bool {
	if ip == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

var errDNS = errors.New("system DNS resolution failed")
var errBlocked = errors.New("destination is private or reserved")

// 198.18/15 is the IANA benchmarking range, not a public address range.
// Some local VPN/filtering products synthesize DNS answers in this range.
// NordVPN also documents 192.0.0.88 for Google (nordvpn-linux #1321).
// Do not allow the rest of 192.0.0.0/24.
// Keep it blocked by default; opt in only for DNS names on web ports, never
// literal IPs, private namespaces, or other private/reserved ranges.
var _, syntheticNet, _ = net.ParseCIDR("198.18.0.0/15")
var nordGoogleIP = net.IPv4(192, 0, 0, 88)
var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

func syntheticName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if len(host) > 253 || net.ParseIP(host) != nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	last := labels[len(labels)-1]
	if last == "local" || last == "localhost" || last == "internal" || last == "lan" || last == "home" || last == "corp" || last == "arpa" || last == "test" || last == "invalid" || last == "example" || last == "onion" {
		return false
	}
	return strings.ContainsAny(last, "abcdefghijklmnopqrstuvwxyz")
}
func googleName(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	return syntheticName(host) && (host == "google.com" || strings.HasSuffix(host, ".google.com"))
}
func syntheticCandidate(ip net.IP) bool {
	return syntheticNet.Contains(ip) || ip.Equal(nordGoogleIP)
}
func allowedDestination(ip net.IP, host string, port int, compat bool) bool {
	if publicIP(ip) {
		return true
	}
	if !compat || (port != 80 && port != 443) || !syntheticName(host) {
		return false
	}
	return syntheticNet.Contains(ip) || (ip.Equal(nordGoogleIP) && googleName(host))
}

// The documented Google mapping is IPv4 only. In opt-in mode, ask the
// OS for IPv4 first for Google names, then its normal resolver if needed.
// All answers still pass the destination filter; no DNS server override.
func resolveTarget(ctx context.Context, host string, compat bool, lookupAll func(context.Context, string) ([]net.IPAddr, error), lookupFamily func(context.Context, string, string) ([]net.IP, error)) ([]net.IPAddr, error) {
	if compat && googleName(host) {
		v4ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := lookupFamily(v4ctx, "ip4", host)
		cancel()
		if err == nil && len(ips) > 0 {
			var out []net.IPAddr
			for _, ip := range ips {
				if ip.To4() != nil {
					out = append(out, net.IPAddr{IP: ip})
				}
			}
			if len(out) > 0 {
				return out, nil
			}
		}
	}
	ips, err := lookupAll(ctx, host)
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && (de.IsTimeout || de.IsTemporary) && ctx.Err() == nil {
			ips, err = lookupAll(ctx, host)
		}
	}
	return ips, err
}
func dialTarget(ctx context.Context, network, address string, compat bool, observeSynthetic func(bool)) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return nil, errors.New("invalid port")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Use the OS resolver only; never switch to a public DNS server behind a VPN.
	ips, err := resolveTarget(ctx, host, compat, net.DefaultResolver.LookupIPAddr, net.DefaultResolver.LookupIP)
	if err != nil || len(ips) == 0 {
		return nil, errDNS
	}
	var v4, v6 []net.IP
	for _, a := range ips {
		allowed := allowedDestination(a.IP, host, p, compat)
		if syntheticCandidate(a.IP) && observeSynthetic != nil {
			observeSynthetic(allowed)
		}
		if allowed {
			if a.IP.To4() != nil {
				v4 = append(v4, a.IP)
			} else {
				v6 = append(v6, a.IP)
			}
		}
	}
	if len(v4)+len(v6) == 0 {
		return nil, errBlocked
	}
	// Interleave address families, race at most two dials, and close losers.
	var ordered []net.IP
	prefer6 := ips[0].IP.To4() == nil
	for len(v4)+len(v6) > 0 {
		if prefer6 && len(v6) > 0 {
			ordered = append(ordered, v6[0])
			v6 = v6[1:]
		} else if len(v4) > 0 {
			ordered = append(ordered, v4[0])
			v4 = v4[1:]
		} else {
			ordered = append(ordered, v6[0])
			v6 = v6[1:]
		}
		prefer6 = !prefer6
	}
	jobs := make(chan net.IP, len(ordered))
	for _, ip := range ordered {
		jobs <- ip
	}
	close(jobs)
	results := make(chan net.Conn, 2)
	n := min(2, len(ordered))
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i == 1 {
				select {
				case <-time.After(250 * time.Millisecond):
				case <-ctx.Done():
					return
				}
			}
			d := net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}
			for ip := range jobs {
				if ctx.Err() != nil {
					return
				}
				c, e := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if e == nil {
					select {
					case results <- c:
					case <-ctx.Done():
						c.Close()
					}
					return
				}
			}
		}(i)
	}
	go func() { wg.Wait(); close(results) }()
	for c := range results {
		if c != nil {
			cancel()
			go func() {
				for other := range results {
					other.Close()
				}
			}()
			return c, nil
		}
	}
	return nil, errors.New("destination connection failed")
}
func cleanHeaders(h http.Header) {
	for _, line := range h.Values("Connection") {
		for _, v := range strings.Split(line, ",") {
			h.Del(strings.TrimSpace(v))
		}
	}
	for _, v := range []string{"Connection", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Keep-Alive", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		h.Del(v)
	}
}
func (e *engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("csl:"+e.password))
	candidate := r.Header.Get("Proxy-Authorization")
	trusted, _ := r.Context().Value(peerContextKey{}).(bool)
	if !(candidate == "" && trusted) && subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) != 1 {
		e.authChallenges.Add(1)
		w.Header().Set("Proxy-Authenticate", `Basic realm="`+realm+`"`)
		w.Header().Set("Connection", "close")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusProxyAuthRequired)
		return
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		http.Error(w, "Too many connections", 503)
		return
	}
	e.active.Add(1)
	defer e.active.Add(-1)
	if r.Method == http.MethodConnect {
		e.connect(w, r)
		return
	}
	if r.URL.Scheme != "http" || r.URL.Host == "" || r.URL.User != nil {
		http.Error(w, "Expected absolute HTTP URL", 400)
		return
	}
	if r.Header.Get("Upgrade") != "" {
		http.Error(w, "Plain HTTP upgrades are unsupported", 501)
		return
	}
	out := r.Clone(r.Context())
	out.RequestURI = ""
	out.Host = r.URL.Host
	out.Close = false
	cleanHeaders(out.Header)
	resp, err := e.transport.RoundTrip(out)
	if err != nil {
		http.Error(w, "Destination unavailable", 502)
		return
	}
	defer resp.Body.Close()
	cleanHeaders(resp.Header)
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if err = e.copyDown(r.Context(), w, resp.Body); err != nil && err != io.EOF {
		panic(http.ErrAbortHandler)
	}
}
func (e *engine) connect(w http.ResponseWriter, r *http.Request) {
	remote, err := e.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, "Destination unavailable", 502)
		return
	}
	defer remote.Close()
	client, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	defer e.conns.Delete(client)
	e.conns.Store(client, true)
	if _, err = buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	if err = buf.Flush(); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	// Hijacked tunnels are not closed by http.Server.Close. Cancel both ends
	// immediately even if a copy goroutine is blocked in a socket read/write.
	stopClose := context.AfterFunc(ctx, func() { client.Close(); remote.Close() })
	defer stopClose()
	done := make(chan struct{}, 1)
	go func() {
		io.Copy(remote, buf)
		if tcp, ok := remote.(*net.TCPConn); ok {
			tcp.CloseWrite()
		}
		done <- struct{}{}
	}()
	e.copyDown(ctx, client, remote)
	client.Close()
	remote.Close()
	<-done
}

type command struct {
	ID        int    `json:"id"`
	Command   string `json:"command"`
	Value     string `json:"value"`
	Unit      string `json:"unit"`
	Enabled   *bool  `json:"enabled"`
	VPNCompat bool   `json:"vpnCompat"`
}

func readCommand(r io.Reader) (command, error) {
	var c command
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return c, err
	}
	if n == 0 || n > maxMessage {
		return c, errors.New("invalid message size")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return c, err
	}
	err := json.Unmarshal(b, &c)
	return c, err
}
func writeReply(w io.Writer, r map[string]any) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err = binary.Write(w, binary.LittleEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
func run(in io.Reader, out io.Writer) {
	var e *engine
	defer func() {
		if e != nil {
			e.close()
		}
	}()
	for {
		c, err := readCommand(in)
		if err != nil {
			return
		}
		reply := map[string]any{"id": c.ID, "ok": true}
		stop := false
		switch c.Command {
		case "hello":
			reply["state"] = map[string]any{"version": version}
		case "start", "setLimit":
			var rate int64
			rate, err = capBytes(c.Value, c.Unit)
			if err == nil {
				label := strings.TrimSpace(c.Value) + " " + c.Unit
				if e == nil && c.Command == "start" {
					if err = checkSystemProxy(); err == nil {
						e, err = newEngineWithVPN(rate, label, c.VPNCompat)
					}
				} else if e != nil {
					e.cap = rate
					e.label = label
				} else {
					err = errors.New("engine not running")
				}
				if err == nil {
					if c.Enabled != nil {
						e.limited = *c.Enabled
					}
					if e.limited {
						e.limiter.set(e.cap)
					} else {
						e.limiter.set(0)
					}
					reply["state"] = e.status()
					if c.Command == "start" {
						reply["auth"] = map[string]string{"username": "csl", "password": e.password}
					}
				}
			}
		case "setEnabled":
			if e == nil {
				err = errors.New("engine not running")
			} else if c.Enabled == nil {
				err = errors.New("enabled is required")
			} else {
				e.limited = *c.Enabled
				if e.limited {
					e.limiter.set(e.cap)
				} else {
					e.limiter.set(0)
				}
				reply["state"] = e.status()
			}
		case "status":
			if e == nil {
				err = errors.New("engine not running")
			} else {
				reply["state"] = e.status()
			}
		case "stop":
			if e != nil {
				e.close()
				e = nil
			}
			stop = true
		default:
			err = errors.New("unknown command")
		}
		if err != nil {
			reply = map[string]any{"id": c.ID, "ok": false, "error": err.Error()}
		}
		if writeReply(out, reply) != nil || stop {
			return
		}
	}
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Fprintln(os.Stdout, version)
		return
	}
	run(os.Stdin, os.Stdout)
}
