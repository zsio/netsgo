package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestDownloadAndExtract(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte("fake binary")
	tw.WriteHeader(&tar.Header{Name: "netsgo", Mode: 0o755, Size: int64(len(content))})
	tw.Write(content)
	tw.Close()
	gw.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		w.Write(buf.Bytes())
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "netsgo")

	client := &http.Client{Timeout: 5 * time.Second}
	err := downloadAndExtract(server.URL, binaryPath, client)
	if err != nil {
		t.Fatalf("downloadAndExtract: %v", err)
	}

	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake binary" {
		t.Fatalf("unexpected content: %q", string(data))
	}
	info, _ := os.Stat(binaryPath)
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("not executable")
	}
}

func TestDownloadAndExtractNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "netsgo")

	client := &http.Client{Timeout: 5 * time.Second}
	err := downloadAndExtract(server.URL, binaryPath, client)
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestReplaceBinary(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "netsgo")
	dstPath := filepath.Join(dstDir, "netsgo")

	os.WriteFile(srcPath, []byte("new binary"), 0o755)
	os.WriteFile(dstPath, []byte("old binary"), 0o755)

	err := replaceBinary(srcPath, dstPath)
	if err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	data, _ := os.ReadFile(dstPath)
	if string(data) != "new binary" {
		t.Fatalf("binary not replaced")
	}
}

func TestCheckUpdateNeeded(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
		wantErr bool
	}{
		{"v1.0.0", "v1.1.0", true, false},
		{"v1.1.0", "v1.1.0", false, false},
		{"v1.2.0", "v1.1.0", false, false},
		{"dev", "v1.1.0", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.current+"_vs_"+tt.latest, func(t *testing.T) {
			got, err := checkUpdateNeeded(tt.current, tt.latest)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}
