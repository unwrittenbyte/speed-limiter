// SPDX-License-Identifier: GPL-3.0-only
package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Exercise the HTTP authorization decision independently of Windows TCP APIs.
// A checked browser connection can proceed without a 407; explicit invalid
// credentials and unverified connections still cannot use the proxy.
func TestPeerAuthorization(t *testing.T) {
	e, err := newEngine(1024*1024, "1 MB/s")
	if err != nil {
		t.Fatal(err)
	}
	defer e.close()
	e.dial = func(context.Context, string, string) (net.Conn, error) { return nil, errBlocked }
	e.transport.DialContext = e.dial
	for _, tc := range []struct {
		trusted bool
		header  string
		want    int
	}{{false, "", 407}, {true, "", 502}, {true, "Basic invalid", 407}, {false, "Basic invalid", 407}} {
		r := httptest.NewRequest(http.MethodGet, "http://test.example/", nil)
		r = r.WithContext(context.WithValue(r.Context(), peerContextKey{}, tc.trusted))
		if tc.header != "" {
			r.Header.Set("Proxy-Authorization", tc.header)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("trusted=%v header=%q: got %d want %d", tc.trusted, tc.header, w.Code, tc.want)
		}
	}
}
