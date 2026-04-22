package updater

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchLatestVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases/latest" {
			w.Header().Set("Location", "/releases/tag/v1.2.3")
			w.WriteHeader(http.StatusFound)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}

	version, err := fetchLatestVersionWithClient(server.URL+"/releases/latest", client)
	if err != nil {
		t.Fatalf("fetchLatestVersion: %v", err)
	}
	if version != "v1.2.3" {
		t.Fatalf("expected v1.2.3, got %q", version)
	}
}

func TestBuildDownloadURL(t *testing.T) {
	got := buildDownloadURL(ChannelGitHub, "v1.2.3", "linux", "amd64")
	want := "https://github.com/zsio/netsgo/releases/download/v1.2.3/netsgo_v1.2.3_linux_amd64.tar.gz"
	if got != want {
		t.Fatalf("buildDownloadURL = %q, want %q", got, want)
	}

	got2 := buildDownloadURL(ChannelGhproxy, "v1.2.3", "linux", "amd64")
	want2 := "https://ghproxy.com/https://github.com/zsio/netsgo/releases/download/v1.2.3/netsgo_v1.2.3_linux_amd64.tar.gz"
	if got2 != want2 {
		t.Fatalf("buildDownloadURL ghproxy = %q, want %q", got2, want2)
	}
}
