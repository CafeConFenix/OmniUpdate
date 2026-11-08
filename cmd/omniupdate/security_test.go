package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServerGuard(t *testing.T) {
	a := newApp(true)
	a.token = "secrettoken"
	const host = "127.0.0.1:5000"
	h := a.routes(host)

	do := func(method, target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		req.Host = host
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		name   string
		method string
		target string
		hdr    map[string]string
		want   int
	}{
		{"no token", "GET", "/api/state", nil, http.StatusForbidden},
		{"wrong token", "GET", "/api/state", map[string]string{"X-Token": "nope"}, http.StatusForbidden},
		{"good token header", "GET", "/api/state", map[string]string{"X-Token": "secrettoken"}, http.StatusOK},
		{"good token query", "GET", "/api/state?t=secrettoken", nil, http.StatusOK},
		{"foreign origin", "POST", "/api/scan", map[string]string{"X-Token": "secrettoken", "Origin": "https://evil.example"}, http.StatusForbidden},
		{"same origin", "POST", "/api/scan", map[string]string{"X-Token": "secrettoken", "Origin": "http://" + host}, http.StatusOK},
		{"no origin (not a browser)", "POST", "/api/scan", map[string]string{"X-Token": "secrettoken"}, http.StatusOK},
		{"page without token", "GET", "/", nil, http.StatusForbidden},
		{"page with token", "GET", "/?t=secrettoken", nil, http.StatusOK},
	}
	for _, c := range cases {
		if got := do(c.method, c.target, c.hdr).Code; got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}

	// DNS rebinding: right token but a different Host header.
	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Host = "evil.example:5000"
	req.Header.Set("X-Token", "secrettoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("foreign Host header: got %d, want 403", rec.Code)
	}

	// The page carries the hardening headers.
	page := do("GET", "/?t=secrettoken", nil)
	for _, k := range []string{"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if page.Header().Get(k) == "" {
			t.Errorf("missing header %s on the page", k)
		}
	}
}

func TestSignatureHelpers(t *testing.T) {
	if !signatureValid("Valid|CN=Example Corp\r\n") || signatureValid("NotSigned|") || signatureValid("HashMismatch|CN=x") {
		t.Error("signatureValid")
	}
	if !signatureTampered("HashMismatch|CN=x") || signatureTampered("Valid|CN=x") || signatureTampered("NotSigned|") {
		t.Error("signatureTampered")
	}
}

func TestGithubTrustCheck(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := []struct {
		name, origin, digest, sig string
		ok                        bool
	}{
		{"tracked, nothing to verify", "tracked", "", "NotSigned|", true},
		{"discovered with GitHub digest", "discovered", digest, "NotSigned|", true},
		{"discovered with valid signature", "discovered", "", "Valid|CN=Vendor", true},
		{"discovered, no proof at all", "discovered", "", "NotSigned|", false},
		{"discovered zip (no signature check)", "discovered", "", "", false},
		{"discovered, short digest is not a digest", "discovered", "abc", "", false},
	}
	for _, c := range cases {
		err := githubTrustCheck(c.origin, c.digest, c.sig)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.name, err, c.ok)
		}
	}
}
