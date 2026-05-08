package netutil

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseIPResponseValidatesVersion(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		version int
		want    string
	}{
		{name: "ipv4", raw: "203.0.113.10", version: 4, want: "203.0.113.10"},
		{name: "ipv6", raw: "2001:db8::1", version: 6, want: "2001:db8::1"},
		{name: "reject ipv6 for ipv4", raw: "2001:db8::1", version: 4, want: ""},
		{name: "reject ipv4 for ipv6", raw: "203.0.113.10", version: 6, want: ""},
		{name: "reject html", raw: "<html>203.0.113.10</html>", version: 4, want: ""},
		{name: "reject empty", raw: "", version: 4, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseIPResponse(tt.raw, tt.version); got != tt.want {
				t.Fatalf("parseIPResponse(%q, %d) = %q, want %q", tt.raw, tt.version, got, tt.want)
			}
		})
	}
}

func TestFetchIPFromURLsFallsBackAndValidatesResponse(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not an ip</html>"))
	}))
	defer bad.Close()

	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("203.0.113.20\n"))
	}))
	defer good.Close()

	if got := fetchIPFromURLs([]string{bad.URL, good.URL}, 4); got != "203.0.113.20" {
		t.Fatalf("fetchIPFromURLs() = %q, want 203.0.113.20", got)
	}
}

func TestFetchIPFromURLRejectsNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	if got := fetchIPFromURL(srv.Client(), srv.URL, 4); got != "" {
		t.Fatalf("fetchIPFromURL() = %q, want empty", got)
	}
}
