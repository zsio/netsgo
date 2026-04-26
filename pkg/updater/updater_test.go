package updater

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
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

func TestFetchLatestVersionFromFinalTagURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			http.Redirect(w, r, "/releases/tag/v1.2.3", http.StatusFound)
		case "/releases/tag/v1.2.3":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &http.Client{Timeout: 5 * time.Second}

	version, err := fetchLatestVersionWithClient(server.URL+"/releases/latest", client)
	if err != nil {
		t.Fatalf("fetchLatestVersionWithClient final tag URL: %v", err)
	}
	if version != "v1.2.3" {
		t.Fatalf("expected v1.2.3, got %q", version)
	}
}

func TestBuildDownloadURL(t *testing.T) {
	got := buildDownloadURL(ChannelGitHub, "v1.2.3", "linux", "amd64")
	want := "https://github.com/zsio/netsgo/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz"
	if got != want {
		t.Fatalf("buildDownloadURL = %q, want %q", got, want)
	}

	got2 := buildDownloadURL(ChannelGhproxy, "v1.2.3", "linux", "amd64")
	want2 := "https://ghproxy.com/https://github.com/zsio/netsgo/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz"
	if got2 != want2 {
		t.Fatalf("buildDownloadURL ghproxy = %q, want %q", got2, want2)
	}
}

func TestBuildDownloadURL_ARMv7(t *testing.T) {
	got := buildDownloadURL(ChannelGitHub, "v1.2.3", "linux", "armv7")
	want := "https://github.com/zsio/netsgo/releases/download/v1.2.3/netsgo_1.2.3_linux_armv7.tar.gz"
	if got != want {
		t.Fatalf("buildDownloadURL armv7 = %q, want %q", got, want)
	}
}

func TestBuildChecksumsURL(t *testing.T) {
	got := buildChecksumsURL(ChannelGitHub, "v1.2.3")
	want := "https://github.com/zsio/netsgo/releases/download/v1.2.3/checksums.txt"
	if got != want {
		t.Fatalf("buildChecksumsURL = %q, want %q", got, want)
	}
}

func TestCurrentGOARMFallsBackToEnvironment(t *testing.T) {
	origReadBuildInfo := readBuildInfoFunc
	origGetenv := getenvFunc
	t.Cleanup(func() {
		readBuildInfoFunc = origReadBuildInfo
		getenvFunc = origGetenv
	})

	readBuildInfoFunc = func() (*debug.BuildInfo, bool) {
		return nil, false
	}
	getenvFunc = func(key string) string {
		if key == "GOARM" {
			return "7"
		}
		return ""
	}

	if got := currentGOARM(); got != "7" {
		t.Fatalf("currentGOARM() = %q, want %q", got, "7")
	}
}

func TestDownloadAndExtract(t *testing.T) {
	content := []byte("fake binary")
	archive := mustTarGz(t, "netsgo", content)
	sum := sha256.Sum256(archive)
	manifest := checksumLine("netsgo_1.2.3_linux_amd64.tar.gz", sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte(manifest))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "netsgo")

	client := &http.Client{Timeout: 5 * time.Second}
	err := downloadAndExtract(server.URL+"/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz", binaryPath, client)
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

func TestDownloadAndExtractFollowsRedirect(t *testing.T) {
	content := []byte("redirected binary")
	archive := mustTarGz(t, "netsgo", content)
	sum := sha256.Sum256(archive)
	manifest := checksumLine("netsgo_1.2.3_linux_amd64.tar.gz", sum[:])
	var checksumRequests []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz":
			http.Redirect(w, r, "/objects/abc123/netsgo_1.2.3_linux_amd64.tar.gz", http.StatusFound)
		case "/objects/abc123/netsgo_1.2.3_linux_amd64.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(archive)
		case "/releases/download/v1.2.3/checksums.txt":
			checksumRequests = append(checksumRequests, r.URL.Path)
			_, _ = w.Write([]byte(manifest))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	binaryPath := filepath.Join(tmpDir, "netsgo")

	err := downloadAndExtract(server.URL+"/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz", binaryPath, &http.Client{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("downloadAndExtract redirect: %v", err)
	}
	if len(checksumRequests) != 1 || checksumRequests[0] != "/releases/download/v1.2.3/checksums.txt" {
		t.Fatalf("expected checksums lookup in release context, got %v", checksumRequests)
	}

	data, err := os.ReadFile(binaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "redirected binary" {
		t.Fatalf("unexpected content after redirect: %q", string(data))
	}
}

func TestDownloadAndExtractFailsOnChecksumMismatch(t *testing.T) {
	archive := mustTarGz(t, "release/netsgo", []byte("fake binary"))
	wrongSum := sha256.Sum256([]byte("different"))
	manifest := checksumLine("netsgo_1.2.3_linux_amd64.tar.gz", wrongSum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz":
			_, _ = w.Write(archive)
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte(manifest))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := downloadAndExtract(server.URL+"/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz", filepath.Join(t.TempDir(), "netsgo"), &http.Client{Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestDownloadAndExtractFailsOnInvalidChecksumManifestLine(t *testing.T) {
	archive := mustTarGz(t, "netsgo", []byte("fake binary"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz":
			_, _ = w.Write(archive)
		case "/releases/download/v1.2.3/checksums.txt":
			_, _ = w.Write([]byte("abc netsgo_1.2.3_linux_amd64.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	err := downloadAndExtract(server.URL+"/releases/download/v1.2.3/netsgo_1.2.3_linux_amd64.tar.gz", filepath.Join(t.TempDir(), "netsgo"), &http.Client{Timeout: 5 * time.Second})
	if err == nil || !strings.Contains(err.Error(), "invalid checksums.txt line") {
		t.Fatalf("expected manifest parse error, got %v", err)
	}
}

func TestExtractBinaryAcceptsNestedRelativePath(t *testing.T) {
	archive := mustTarGz(t, "netsgo_1.2.3_linux_amd64/bin/netsgo", []byte("nested binary"))
	destPath := filepath.Join(t.TempDir(), "netsgo")

	err := extractBinary(bytes.NewReader(archive), destPath)
	if err != nil {
		t.Fatalf("extractBinary: %v", err)
	}

	data, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "nested binary" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

func TestExtractBinaryRejectsTraversalPath(t *testing.T) {
	archive := mustTarGz(t, "../netsgo", []byte("bad"))
	err := extractBinary(bytes.NewReader(archive), filepath.Join(t.TempDir(), "netsgo"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found for traversal path, got %v", err)
	}
}

func TestExtractBinaryRejectsSymlinkNamedNetsgo(t *testing.T) {
	archive := mustTarEntry(t, &tar.Header{Name: "netsgo", Typeflag: tar.TypeSymlink, Linkname: "other", Mode: 0o777}, nil)
	err := extractBinary(bytes.NewReader(archive), filepath.Join(t.TempDir(), "netsgo"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found for symlink entry, got %v", err)
	}
}

func TestExtractBinaryRejectsDirectoryNamedNetsgo(t *testing.T) {
	archive := mustTarEntry(t, &tar.Header{Name: "netsgo", Typeflag: tar.TypeDir, Mode: 0o755}, nil)
	err := extractBinary(bytes.NewReader(archive), filepath.Join(t.TempDir(), "netsgo"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found for directory entry, got %v", err)
	}
}

func TestReplaceBinary(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "netsgo")
	dstPath := filepath.Join(dstDir, "netsgo")

	_ = os.WriteFile(srcPath, []byte("new binary"), 0o755)
	_ = os.WriteFile(dstPath, []byte("old binary"), 0o755)

	err := replaceBinary(srcPath, dstPath)
	if err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	data, _ := os.ReadFile(dstPath)
	if string(data) != "new binary" {
		t.Fatalf("binary not replaced")
	}
}

func TestReplaceBinaryCleansTempFileOnRenameError(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "netsgo")
	dstPath := filepath.Join(dstDir, "existing-dir")

	if err := os.WriteFile(srcPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dstPath, 0o755); err != nil {
		t.Fatal(err)
	}

	err := replaceBinary(srcPath, dstPath)
	if err == nil {
		t.Fatal("expected rename error")
	}

	if _, err := os.Stat(dstPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected temp file to be cleaned up, stat err = %v", err)
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
		{"1.1.0+build.1", "v1.1.0+build.2", false, false},
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

func TestCheckForUpdateReturnsLatestWithoutApplying(t *testing.T) {
	origFetchLatestVersion := fetchLatestVersionFunc
	t.Cleanup(func() {
		fetchLatestVersionFunc = origFetchLatestVersion
	})

	fetchLatestVersionFunc = func(channel DownloadChannel) (string, error) {
		return "v1.1.0", nil
	}

	result, needed, err := CheckForUpdate(ChannelGitHub, "v1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !needed {
		t.Fatal("expected update to be needed")
	}
	if result.OldVersion != "v1.0.0" || result.NewVersion != "v1.1.0" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckForUpdateUsesSelectedChannelForLatestLookup(t *testing.T) {
	origFetchLatestVersion := fetchLatestVersionFunc
	t.Cleanup(func() {
		fetchLatestVersionFunc = origFetchLatestVersion
	})

	var gotChannel DownloadChannel
	fetchLatestVersionFunc = func(channel DownloadChannel) (string, error) {
		gotChannel = channel
		return "v1.0.0", nil
	}

	_, _, err := CheckForUpdate(ChannelGhproxy, "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotChannel != ChannelGhproxy {
		t.Fatalf("expected fetch latest to use %q, got %q", ChannelGhproxy, gotChannel)
	}
}

func TestApplyConfirmedUpdateUsesConfirmedVersionWithoutRefetch(t *testing.T) {
	origFetchLatestVersion := fetchLatestVersionFunc
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origDownloadAndExtract := downloadAndExtractFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		fetchLatestVersionFunc = origFetchLatestVersion
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		downloadAndExtractFunc = origDownloadAndExtract
		installedBinaryPath = origBinaryPath
	})

	fetchLatestVersionFunc = func(channel DownloadChannel) (string, error) {
		t.Fatal("ApplyConfirmedUpdate should not refetch latest version")
		return "", nil
	}
	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error { return nil }
	var gotURL string
	downloadAndExtractFunc = func(url, destPath string, client *http.Client) error {
		gotURL = url
		return os.WriteFile(destPath, []byte("new binary"), 0o755)
	}

	result, err := ApplyConfirmedUpdate(ChannelGitHub, "v1.0.0", "v1.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.NewVersion != "v1.1.0" {
		t.Fatalf("unexpected result: %+v", result)
	}
	wantURL := platformAssetURL(ChannelGitHub, "v1.1.0")
	if gotURL != wantURL {
		t.Fatalf("expected confirmed asset url %q, got %q", wantURL, gotURL)
	}
}

func TestApplyConfirmedUpdateRestartsStoppedServicesWhenStopFailsPartially(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string {
		return []string{"netsgo-server.service", "netsgo-client.service"}
	}
	disableAndStopFunc = func(unit string) error {
		if unit == "netsgo-client.service" {
			return fmt.Errorf("boom")
		}
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}

	_, err := ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(restarted) != 1 || restarted[0] != "netsgo-server.service" {
		t.Fatalf("expected restarted stopped units, got %v", restarted)
	}
}

func TestApplyConfirmedUpdateRestoresOldBinaryWhenStartFails(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origDownloadAndExtract := downloadAndExtractFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		downloadAndExtractFunc = origDownloadAndExtract
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error { return fmt.Errorf("start failed") }
	downloadAndExtractFunc = func(url, destPath string, client *http.Client) error {
		return os.WriteFile(destPath, []byte("new binary"), 0o755)
	}

	_, err := ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}

	data, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old binary" {
		t.Fatalf("expected old binary restored, got %q", string(data))
	}
}

func TestApplyConfirmedUpdateStopsAlreadyStartedServicesBeforeRollback(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origDownloadAndExtract := downloadAndExtractFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		downloadAndExtractFunc = origDownloadAndExtract
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	var stoppedAgain []string
	var startCalls []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		stoppedAgain = append(stoppedAgain, unit)
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		startCalls = append(startCalls, unit)
		if unit == "netsgo-client.service" {
			return fmt.Errorf("start failed")
		}
		return nil
	}
	downloadAndExtractFunc = func(url, destPath string, client *http.Client) error {
		return os.WriteFile(destPath, []byte("new binary"), 0o755)
	}

	_, err := ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(startCalls) < 2 || startCalls[0] != "netsgo-server.service" || startCalls[1] != "netsgo-client.service" {
		t.Fatalf("unexpected start order: %v", startCalls)
	}
	if len(stoppedAgain) != 3 {
		t.Fatalf("expected original stop plus rollback stop, got %v", stoppedAgain)
	}
	if stoppedAgain[2] != "netsgo-server.service" {
		t.Fatalf("expected already-started service to be stopped during rollback, got %v", stoppedAgain)
	}
}

func TestApplyConfirmedUpdateRestartsStoppedServicesWhenPanicOccurs(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origDownloadAndExtract := downloadAndExtractFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		downloadAndExtractFunc = origDownloadAndExtract
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	downloadAndExtractFunc = func(url, destPath string, client *http.Client) error {
		panic("download panic")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 2 || restarted[0] != "netsgo-server.service" || restarted[1] != "netsgo-client.service" {
			t.Fatalf("expected stopped services to be restarted in rollback order, got %v", restarted)
		}
	}()

	_, _ = ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
}

func TestApplyConfirmedUpdateRestartsOnlyPartiallyStoppedServicesWhenStopPanics(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		if unit == "netsgo-client.service" {
			panic("stop panic")
		}
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 1 || restarted[0] != "netsgo-server.service" {
			t.Fatalf("expected only fully stopped service to be restarted, got %v", restarted)
		}
	}()

	_, _ = ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
}

func TestApplyConfirmedUpdateRollsBackWhenStartPanics(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origDownloadAndExtract := downloadAndExtractFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		downloadAndExtractFunc = origDownloadAndExtract
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	var stopCalls []string
	var startCalls []string
	var startCallCount int
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		stopCalls = append(stopCalls, unit)
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		startCallCount++
		startCalls = append(startCalls, unit)
		if startCallCount == 2 {
			panic("start panic")
		}
		return nil
	}
	downloadAndExtractFunc = func(url, destPath string, client *http.Client) error {
		return os.WriteFile(destPath, []byte("new binary"), 0o755)
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(stopCalls) != 3 || stopCalls[2] != "netsgo-server.service" {
			t.Fatalf("expected rollback to stop already-started service, got %v", stopCalls)
		}
		if len(startCalls) != 4 || startCalls[0] != "netsgo-server.service" || startCalls[1] != "netsgo-client.service" || startCalls[2] != "netsgo-server.service" || startCalls[3] != "netsgo-client.service" {
			t.Fatalf("expected restart sequence after panic rollback, got %v", startCalls)
		}
		data, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "old binary" {
			t.Fatalf("expected old binary restored after panic, got %q", string(data))
		}
	}()

	_, _ = ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
}

func TestApplyConfirmedUpdateRestartsStoppedServicesWhenPanicOccursInProtectionGap(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origMkdirTemp := osMkdirTempFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		osMkdirTempFunc = origMkdirTemp
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	osMkdirTempFunc = func(dir, pattern string) (string, error) {
		panic("gap panic")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 2 || restarted[0] != "netsgo-server.service" || restarted[1] != "netsgo-client.service" {
			t.Fatalf("expected stopped services to be restarted from protection gap, got %v", restarted)
		}
	}()

	_, _ = ApplyConfirmedUpdate(ChannelGitHub, "1.0.0", "v1.1.0")
}

func TestUpgradeRestartsStoppedServicesWhenReplaceFails(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origReplaceBinary := replaceBinaryFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		replaceBinaryFunc = origReplaceBinary
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string {
		return []string{"netsgo-server.service"}
	}
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	replaceBinaryFunc = func(srcPath, dstPath string) error {
		return fmt.Errorf("replace failed")
	}

	_, err := Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(restarted) != 1 || restarted[0] != "netsgo-server.service" {
		t.Fatalf("expected service restart rollback, got %v", restarted)
	}
}

func TestUpgradeReturnsProvidedVersionFields(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error { return nil }

	result, err := Upgrade(newPath, "1.0.0", "1.1.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.OldVersion != "1.0.0" || result.NewVersion != "1.1.0" {
		t.Fatalf("unexpected versions: old=%q new=%q", result.OldVersion, result.NewVersion)
	}
	if len(result.Stopped) != 1 || result.Stopped[0] != "netsgo-server.service" {
		t.Fatalf("unexpected stopped services: %v", result.Stopped)
	}
	if len(result.Started) != 1 || result.Started[0] != "netsgo-server.service" {
		t.Fatalf("unexpected started services: %v", result.Started)
	}
}

func TestUpgradeRestoresOldBinaryWhenStartFails(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error { return fmt.Errorf("start failed") }

	_, err := Upgrade(newPath, "1.0.0", "1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}

	data, err := os.ReadFile(installedPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old binary" {
		t.Fatalf("expected old binary restored, got %q", string(data))
	}
}

func TestUpgradeStopsAlreadyStartedServicesBeforeRollback(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	var stoppedAgain []string
	var startCalls []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		stoppedAgain = append(stoppedAgain, unit)
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		startCalls = append(startCalls, unit)
		if unit == "netsgo-client.service" {
			return fmt.Errorf("start failed")
		}
		return nil
	}

	_, err := Upgrade(newPath, "1.0.0", "1.1.0")
	if err == nil {
		t.Fatal("expected error")
	}
	if len(startCalls) < 2 || startCalls[0] != "netsgo-server.service" || startCalls[1] != "netsgo-client.service" {
		t.Fatalf("unexpected start order: %v", startCalls)
	}
	if len(stoppedAgain) != 3 {
		t.Fatalf("expected original stop plus rollback stop, got %v", stoppedAgain)
	}
	if stoppedAgain[2] != "netsgo-server.service" {
		t.Fatalf("expected already-started service to be stopped during rollback, got %v", stoppedAgain)
	}
}

func TestUpgradeRestartsStoppedServicesWhenPanicOccurs(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origReplaceBinary := replaceBinaryFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		replaceBinaryFunc = origReplaceBinary
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	replaceBinaryFunc = func(srcPath, dstPath string) error {
		panic("replace panic")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 2 || restarted[0] != "netsgo-server.service" || restarted[1] != "netsgo-client.service" {
			t.Fatalf("expected stopped services to be restarted in rollback order, got %v", restarted)
		}
	}()

	_, _ = Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
}

func TestUpgradeRestartsOnlyPartiallyStoppedServicesWhenStopPanics(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		if unit == "netsgo-client.service" {
			panic("stop panic")
		}
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 1 || restarted[0] != "netsgo-server.service" {
			t.Fatalf("expected only fully stopped service to be restarted, got %v", restarted)
		}
	}()

	_, _ = Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
}

func TestUpgradeRollsBackWhenStartPanics(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origBinaryPath := installedBinaryPath
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		installedBinaryPath = origBinaryPath
	})

	tmpDir := t.TempDir()
	installedPath := filepath.Join(tmpDir, "installed-netsgo")
	newPath := filepath.Join(tmpDir, "new-netsgo")
	if err := os.WriteFile(installedPath, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	installedBinaryPath = installedPath

	var stopCalls []string
	var startCalls []string
	var startCallCount int
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error {
		stopCalls = append(stopCalls, unit)
		return nil
	}
	enableAndStartFunc = func(unit string) error {
		startCallCount++
		startCalls = append(startCalls, unit)
		if startCallCount == 2 {
			panic("start panic")
		}
		return nil
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(stopCalls) != 3 || stopCalls[2] != "netsgo-server.service" {
			t.Fatalf("expected rollback to stop already-started service, got %v", stopCalls)
		}
		if len(startCalls) != 4 || startCalls[0] != "netsgo-server.service" || startCalls[1] != "netsgo-client.service" || startCalls[2] != "netsgo-server.service" || startCalls[3] != "netsgo-client.service" {
			t.Fatalf("expected restart sequence after panic rollback, got %v", startCalls)
		}
		data, err := os.ReadFile(installedPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "old binary" {
			t.Fatalf("expected old binary restored after panic, got %q", string(data))
		}
	}()

	_, _ = Upgrade(newPath, "1.0.0", "1.1.0")
}

func TestUpgradeRestartsStoppedServicesWhenPanicOccursInProtectionGap(t *testing.T) {
	origDisableAndStop := disableAndStopFunc
	origEnableAndStart := enableAndStartFunc
	origDetectInstalledUnits := detectInstalledUnitsFunc
	origMkdirTemp := osMkdirTempFunc
	t.Cleanup(func() {
		disableAndStopFunc = origDisableAndStop
		enableAndStartFunc = origEnableAndStart
		detectInstalledUnitsFunc = origDetectInstalledUnits
		osMkdirTempFunc = origMkdirTemp
	})

	var restarted []string
	detectInstalledUnitsFunc = func() []string { return []string{"netsgo-server.service", "netsgo-client.service"} }
	disableAndStopFunc = func(unit string) error { return nil }
	enableAndStartFunc = func(unit string) error {
		restarted = append(restarted, unit)
		return nil
	}
	osMkdirTempFunc = func(dir, pattern string) (string, error) {
		panic("gap panic")
	}

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic")
		}
		if len(restarted) != 2 || restarted[0] != "netsgo-server.service" || restarted[1] != "netsgo-client.service" {
			t.Fatalf("expected stopped services to be restarted from protection gap, got %v", restarted)
		}
	}()

	_, _ = Upgrade("/tmp/netsgo", "1.0.0", "1.1.0")
}

func mustTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	return mustTarEntry(t, &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(content))}, content)
}

func mustTarEntry(t *testing.T, header *tar.Header, content []byte) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if len(content) > 0 {
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func checksumLine(filename string, checksum []byte) string {
	return hex.EncodeToString(checksum) + "  " + filename + "\n"
}
