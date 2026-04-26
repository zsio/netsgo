# Manage Update / Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `update` to the `manage` interactive menu (auto-download latest release) and a standalone `upgrade` root command (replace installed binary with currently running one). Both support Linux systemd managed mode only.

**Architecture:** A shared `pkg/updater` package handles version checking, download, and binary replacement. `internal/manage` wires `update` into the service menu. `cmd/netsgo/cmd_upgrade.go` provides the standalone entry point. Version comparison lives in `pkg/version`.

**Tech Stack:** Go, standard library (net/http, archive/tar, compress/gzip), existing svcmgr/systemd integration.

---

## File Structure

| File | Responsibility |
|------|---------------|
| `pkg/version/semver.go` | Semantic version parsing and comparison |
| `pkg/version/semver_test.go` | Tests for version comparison |
| `pkg/updater/version.go` | Fetch latest release version from GitHub via HTTP 302 |
| `pkg/updater/download.go` | Download and extract release tar.gz asset |
| `pkg/updater/replace.go` | Replace binary and orchestrate systemd stop/start |
| `pkg/updater/update.go` | `AutoUpdate` high-level function |
| `pkg/updater/upgrade.go` | `Upgrade` high-level function |
| `pkg/updater/updater_test.go` | Tests for updater components |
| `internal/manage/update.go` | `manage` interactive `update` flow |
| `internal/manage/update_test.go` | Tests for manage update integration |
| `cmd/netsgo/cmd_upgrade.go` | Standalone `upgrade` CLI command |
| `internal/manage/service_menu.go` | Add `Update` to menu options |
| `internal/manage/server.go` | Wire `Update` into server service menu deps |
| `internal/manage/client.go` | Wire `Update` into client service menu deps |
| `cmd/netsgo/cmd_update.go` | Update placeholder message |

---

## Task 1: Semantic Version Comparison

**Files:**
- Create: `pkg/version/semver.go`
- Test: `pkg/version/semver_test.go`

- [ ] **Step 1: Write the failing test**

```go
package version

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input   string
		want    Semver
		wantErr bool
	}{
		{"v1.2.3", Semver{1, 2, 3}, false},
		{"1.2.3", Semver{1, 2, 3}, false},
		{"v0.0.1", Semver{0, 0, 1}, false},
		{"dev", Semver{}, true},
		{"", Semver{}, true},
		{"v1.2", Semver{}, true},
		{"v1.2.3.4", Semver{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseSemver(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSemver(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseSemver(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSemverCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.2.0", "v1.1.0", 1},
		{"v1.1.0", "v1.2.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v0.1.0", "v0.0.1", 1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			a, _ := ParseSemver(tt.a)
			b, _ := ParseSemver(tt.b)
			got := a.Compare(b)
			if got != tt.want {
				t.Fatalf("Compare = %d, want %d", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags dev ./pkg/version/... -v`
Expected: FAIL - Semver not defined

- [ ] **Step 3: Write minimal implementation**

```go
package version

import (
	"fmt"
	"strconv"
	"strings"
)

type Semver struct {
	Major int
	Minor int
	Patch int
}

func ParseSemver(s string) (Semver, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Semver{}, fmt.Errorf("invalid semver: %q", s)
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid major: %w", err)
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid minor: %w", err)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		return Semver{}, fmt.Errorf("invalid patch: %w", err)
	}
	return Semver{Major: major, Minor: minor, Patch: patch}, nil
}

func (s Semver) Compare(other Semver) int {
	if s.Major != other.Major {
		if s.Major > other.Major {
			return 1
		}
		return -1
	}
	if s.Minor != other.Minor {
		if s.Minor > other.Minor {
			return 1
		}
		return -1
	}
	if s.Patch != other.Patch {
		if s.Patch > other.Patch {
			return 1
		}
		return -1
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags dev ./pkg/version/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/version/semver.go pkg/version/semver_test.go
git commit -m "feat: add semantic version parsing and comparison"
```

---

## Task 2: Fetch Latest Release Version

**Files:**
- Create: `pkg/updater/version.go`
- Test: `pkg/updater/updater_test.go`

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: FAIL - undefined types/functions

- [ ] **Step 3: Write minimal implementation**

```go
package updater

import (
	"fmt"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"
)

const (
	githubBaseURL   = "https://github.com/zsio/netsgo"
	ghproxyBaseURL  = "https://ghproxy.com/https://github.com/zsio/netsgo"
	releaseAssetTpl = "netsgo_%s_%s_%s.tar.gz"
)

type DownloadChannel string

const (
	ChannelGitHub  DownloadChannel = "github"
	ChannelGhproxy DownloadChannel = "ghproxy"
)

var defaultHTTPClient = &http.Client{
	Timeout: 15 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func fetchLatestVersion() (string, error) {
	return fetchLatestVersionWithClient(githubBaseURL+"/releases/latest", defaultHTTPClient)
}

func fetchLatestVersionWithClient(url string, client *http.Client) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch latest: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("no redirect location")
	}

	version := path.Base(location)
	if version == "" || version == "." || version == "/" {
		return "", fmt.Errorf("invalid version in redirect: %q", location)
	}

	return version, nil
}

func buildDownloadURL(channel DownloadChannel, version, goos, goarch string) string {
	base := githubBaseURL
	if channel == ChannelGhproxy {
		base = ghproxyBaseURL
	}
	assetName := fmt.Sprintf(releaseAssetTpl, strings.TrimPrefix(version, "v"), goos, goarch)
	return fmt.Sprintf("%s/releases/download/%s/%s", base, version, assetName)
}

func platformAssetURL(channel DownloadChannel, version string) string {
	return buildDownloadURL(channel, version, runtime.GOOS, runtime.GOARCH)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/updater/version.go pkg/updater/updater_test.go
git commit -m "feat: add release version fetcher and download URL builder"
```

---

## Task 3: Download and Extract Release Asset

**Files:**
- Create: `pkg/updater/download.go`
- Modify: `pkg/updater/updater_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/updater/updater_test.go`:

```go
import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: FAIL - downloadAndExtract undefined

- [ ] **Step 3: Write minimal implementation**

```go
package updater

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func downloadAndExtract(url, destPath string, client *http.Client) error {
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: status %d", resp.StatusCode)
	}
	return extractBinary(resp.Body, destPath)
}

func extractBinary(r io.Reader, destPath string) error {
	gr, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("extract: open gzip: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extract: read tar: %w", err)
		}
		if header.Name == "netsgo" || filepath.Base(header.Name) == "netsgo" {
			_ = os.MkdirAll(filepath.Dir(destPath), 0o755)
			f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
			if err != nil {
				return fmt.Errorf("extract: create file: %w", err)
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return fmt.Errorf("extract: write: %w", err)
			}
			if err := f.Close(); err != nil {
				return fmt.Errorf("extract: close: %w", err)
			}
			return nil
		}
	}
	return fmt.Errorf("extract: binary 'netsgo' not found")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/updater/download.go pkg/updater/updater_test.go
git commit -m "feat: add release asset download and extraction"
```

---

## Task 4: Binary Replacement and Service Orchestration

**Files:**
- Create: `pkg/updater/replace.go`
- Modify: `pkg/updater/updater_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/updater/updater_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: FAIL - replaceBinary undefined

- [ ] **Step 3: Write minimal implementation**

```go
package updater

import (
	"fmt"
	"io"
	"os"
)

type Orchestrator struct {
	DisableAndStop func(unitName string) error
	EnableAndStart func(unitName string) error
}

func (o *Orchestrator) StopServices(units []string) ([]string, error) {
	stopped := make([]string, 0, len(units))
	for _, unit := range units {
		if err := o.DisableAndStop(unit); err != nil {
			return stopped, fmt.Errorf("stop %s: %w", unit, err)
		}
		stopped = append(stopped, unit)
	}
	return stopped, nil
}

func (o *Orchestrator) StartServices(units []string) ([]string, error) {
	started := make([]string, 0, len(units))
	for _, unit := range units {
		if err := o.EnableAndStart(unit); err != nil {
			return started, fmt.Errorf("start %s: %w", unit, err)
		}
		started = append(started, unit)
	}
	return started, nil
}

func replaceBinary(srcPath, dstPath string) error {
	tmpPath := dstPath + ".tmp"

	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer src.Close()

	dst, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/updater/replace.go pkg/updater/updater_test.go
git commit -m "feat: add binary replacement and service orchestration"
```

---

## Task 5: High-Level Update and Upgrade Functions

**Files:**
- Create: `pkg/updater/update.go`
- Create: `pkg/updater/upgrade.go`
- Modify: `pkg/updater/updater_test.go`

- [ ] **Step 1: Write the failing test**

Append to `pkg/updater/updater_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: FAIL - checkUpdateNeeded undefined

- [ ] **Step 3: Write minimal implementation**

`pkg/updater/update.go`:

```go
package updater

import (
	"fmt"
	"netsgo/internal/svcmgr"
	"netsgo/pkg/version"
)

func checkUpdateNeeded(currentVersion, latestVersion string) (bool, error) {
	current, err := version.ParseSemver(currentVersion)
	if err != nil {
		return false, fmt.Errorf("parse current: %w", err)
	}
	latest, err := version.ParseSemver(latestVersion)
	if err != nil {
		return false, fmt.Errorf("parse latest: %w", err)
	}
	return latest.Compare(current) > 0, nil
}

func installedUnits() []string {
	var units []string
	if svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleServer))
	}
	if svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleClient))
	}
	return units
}

type Result struct {
	OldVersion string
	NewVersion string
	Stopped    []string
	Started    []string
}

func AutoUpdate(channel DownloadChannel, currentVersion string) (*Result, error) {
	result := &Result{OldVersion: currentVersion}

	latest, err := fetchLatestVersion()
	if err != nil {
		return result, fmt.Errorf("check latest: %w", err)
	}
	result.NewVersion = latest

	needed, err := checkUpdateNeeded(currentVersion, latest)
	if err != nil {
		return result, fmt.Errorf("compare: %w", err)
	}
	if !needed {
		return result, nil
	}

	units := installedUnits()
	if len(units) == 0 {
		return result, fmt.Errorf("no installed services")
	}

	orch := &Orchestrator{
		DisableAndStop: svcmgr.DisableAndStop,
		EnableAndStart: svcmgr.EnableAndStart,
	}

	stopped, err := orch.StopServices(units)
	if err != nil {
		return result, err
	}
	result.Stopped = stopped

	tmpDir, err := os.MkdirTemp("", "netsgo-update-*")
	if err != nil {
		return result, fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	url := platformAssetURL(channel, latest)
	newBinary := filepath.Join(tmpDir, "netsgo")
	if err := downloadAndExtract(url, newBinary, defaultHTTPClient); err != nil {
		return result, fmt.Errorf("download: %w", err)
	}

	if err := replaceBinary(newBinary, svcmgr.BinaryPath); err != nil {
		return result, fmt.Errorf("replace: %w", err)
	}

	started, err := orch.StartServices(units)
	if err != nil {
		return result, err
	}
	result.Started = started
	return result, nil
}
```

`pkg/updater/upgrade.go`:

```go
package updater

import (
	"fmt"
	"netsgo/internal/svcmgr"
)

func Upgrade(srcPath string) (*Result, error) {
	result := &Result{}

	units := installedUnits()
	if len(units) == 0 {
		return result, fmt.Errorf("no installed services")
	}

	orch := &Orchestrator{
		DisableAndStop: svcmgr.DisableAndStop,
		EnableAndStart: svcmgr.EnableAndStart,
	}

	stopped, err := orch.StopServices(units)
	if err != nil {
		return result, err
	}
	result.Stopped = stopped

	if err := replaceBinary(srcPath, svcmgr.BinaryPath); err != nil {
		return result, fmt.Errorf("replace: %w", err)
	}

	started, err := orch.StartServices(units)
	if err != nil {
		return result, err
	}
	result.Started = started
	return result, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags dev ./pkg/updater/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/updater/update.go pkg/updater/upgrade.go pkg/updater/updater_test.go
git commit -m "feat: add AutoUpdate and Upgrade high-level functions"
```

---

## Task 6: Standalone `upgrade` CLI Command

**Files:**
- Create: `cmd/netsgo/cmd_upgrade.go`

- [ ] **Step 1: Write the code**

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
	"netsgo/pkg/version"

	"github.com/spf13/cobra"
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade installed NetsGo binary with the currently running one",
	Long: `Upgrade replaces the system-installed NetsGo binary with the currently
running binary, then restarts all managed services.

Requires root privileges (auto-elevates via sudo).
Only works when services are installed via 'netsgo install'.
If the current binary is already the installed one, does nothing.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if os.Getuid() != 0 {
			return syscall.Exec("/usr/bin/sudo", append([]string{"sudo"}, os.Args...), os.Environ())
		}

		units := installedUnits()
		if len(units) == 0 {
			fmt.Fprintln(os.Stderr, "No installed services found.")
			fmt.Fprintln(os.Stderr, "Run 'netsgo install' first, or use 'netsgo update' to download.")
			os.Exit(1)
		}

		currentPath, err := svcmgr.CurrentBinaryPath()
		if err != nil {
			return fmt.Errorf("get current binary path: %w", err)
		}

		if currentPath == svcmgr.BinaryPath {
			fmt.Println("Current binary is already the installed binary.")
			fmt.Println("Nothing to upgrade.")
			return nil
		}

		installedVersion := getInstalledVersion()
		currentVersion := version.Current

		if currentVersion == installedVersion && installedVersion != "" {
			fmt.Printf("Current version %s is the same as installed.\n", currentVersion)
			fmt.Println("Nothing to upgrade.")
			return nil
		}

		if isDevVersion(currentVersion) {
			fmt.Printf("Current version is '%s' (development build).\n", currentVersion)
			fmt.Println("Upgrading with a development build is not recommended.")
			return nil
		}

		if installedVersion != "" {
			cmp, _ := version.ParseSemver(currentVersion)
			inst, _ := version.ParseSemver(installedVersion)
			if cmp.Compare(inst) < 0 {
				fmt.Printf("Current %s is older than installed %s.\n", currentVersion, installedVersion)
				fmt.Println("This would downgrade. Aborting.")
				return nil
			}
		}

		fmt.Printf("Upgrading %s -> %s\n", installedVersion, currentVersion)
		fmt.Printf("Services to restart: %v\n", units)
		fmt.Println()

		result, err := updater.Upgrade(currentPath)
		if err != nil {
			return fmt.Errorf("upgrade failed: %w", err)
		}

		fmt.Println("Upgraded successfully.")
		fmt.Printf("Stopped: %v\n", result.Stopped)
		fmt.Printf("Started: %v\n", result.Started)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)
}

func installedUnits() []string {
	var units []string
	if svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleServer))
	}
	if svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled {
		units = append(units, svcmgr.UnitName(svcmgr.RoleClient))
	}
	return units
}

func getInstalledVersion() string {
	if _, err := os.Stat(svcmgr.BinaryPath); err != nil {
		return ""
	}
	out, err := exec.Command(svcmgr.BinaryPath, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isDevVersion(v string) bool {
	_, err := version.ParseSemver(v)
	return err != nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build -tags dev ./cmd/netsgo`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add cmd/netsgo/cmd_upgrade.go
git commit -m "feat: add upgrade CLI command"
```

---

## Task 7: Manage Menu Update Integration

**Files:**
- Create: `internal/manage/update.go`
- Test: `internal/manage/update_test.go`

- [ ] **Step 1: Write the failing test**

```go
package manage

import (
	"testing"
)

func TestRunUpdate_NoServices(t *testing.T) {
	ui := &mockUI{}
	err := runUpdate(ui, "v1.0.0", func() bool { return false }, nil)
	if err == nil {
		t.Fatal("expected error when no services installed")
	}
}

type mockUI struct {
	selectIndex int
	selectErr   error
	confirmVal  bool
	confirmErr  error
}

func (m *mockUI) Select(prompt string, options []string) (int, error) {
	return m.selectIndex, m.selectErr
}
func (m *mockUI) Confirm(prompt string) (bool, error) {
	return m.confirmVal, m.confirmErr
}
func (m *mockUI) PrintSummary(title string, rows [][2]string) {}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags dev ./internal/manage/... -v -run TestRunUpdate`
Expected: FAIL - runUpdate undefined

- [ ] **Step 3: Write minimal implementation**

```go
package manage

import (
	"fmt"

	"netsgo/internal/svcmgr"
	"netsgo/pkg/updater"
	"netsgo/pkg/version"
)

func runUpdate(ui uiProvider, currentVersion string, hasInstalled func() bool, autoUpdate func(updater.DownloadChannel, string) (*updater.Result, error)) error {
	if hasInstalled == nil {
		hasInstalled = func() bool {
			return svcmgr.Detect(svcmgr.RoleServer) == svcmgr.StateInstalled ||
				svcmgr.Detect(svcmgr.RoleClient) == svcmgr.StateInstalled
		}
	}

	if !hasInstalled() {
		return fmt.Errorf("no installed services found")
	}

	if _, err := version.ParseSemver(currentVersion); err != nil {
		ui.PrintSummary("Update", [][2]string{
			{"Version", currentVersion},
			{"Status", "Development build — automatic update not supported"},
		})
		return nil
	}

	channelIdx, err := ui.Select("Select download channel", []string{"GitHub (default)", "ghproxy (mirror)"})
	if err != nil {
		return err
	}

	channel := updater.ChannelGitHub
	if channelIdx == 1 {
		channel = updater.ChannelGhproxy
	}

	ui.PrintSummary("Update", [][2]string{
		{"Current", currentVersion},
		{"Channel", string(channel)},
		{"Status", "Checking..."},
	})

	if autoUpdate == nil {
		autoUpdate = updater.AutoUpdate
	}

	result, err := autoUpdate(channel, currentVersion)
	if err != nil {
		ui.PrintSummary("Update failed", [][2]string{{"Error", err.Error()}})
		return nil
	}

	if result.NewVersion == currentVersion {
		ui.PrintSummary("No update", [][2]string{
			{"Current", currentVersion},
			{"Status", "Already latest"},
		})
		return nil
	}

	rows := [][2]string{
		{"From", result.OldVersion},
		{"To", result.NewVersion},
	}
	if len(result.Stopped) > 0 {
		rows = append(rows, [2]string{"Stopped", fmt.Sprintf("%v", result.Stopped)})
	}
	if len(result.Started) > 0 {
		rows = append(rows, [2]string{"Started", fmt.Sprintf("%v", result.Started)})
	}
	ui.PrintSummary("Update complete", rows)
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags dev ./internal/manage/... -v -run TestRunUpdate`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/manage/update.go internal/manage/update_test.go
git commit -m "feat: add manage menu update integration"
```

---

## Task 8: Wire Update into Service Menus

**Files:**
- Modify: `internal/manage/service_menu.go`
- Modify: `internal/manage/server.go`
- Modify: `internal/manage/client.go`

- [ ] **Step 1: Modify service_menu.go**

Change `manageActionOptions` and `serviceMenuDeps`:

```go
var (
	manageActionOptions = []string{"Status", "Inspect", "Logs", "Start", "Stop", "Restart", "Update", "Uninstall", "Back"}
	errReturnToSelection = errors.New("manage: return to selection")
)

type serviceMenuDeps struct {
	UI        uiProvider
	Status    func() error
	Detail    func() error
	Logs      func() error
	Start     func() error
	Stop      func() error
	Update    func() error
	Uninstall func() (bool, error)
}
```

In `runServiceMenu`, add case for Update (index 6), shift Uninstall to 7, Back to 8:

```go
		case 6:
			if err := deps.Update(); err != nil {
				return err
			}
		case 7:
			exitMenu, err := deps.Uninstall()
			...
		case 8:
			return errReturnToSelection
```

- [ ] **Step 2: Modify server.go**

Add `"netsgo/pkg/version"` to imports.

In `ManageServerWith`, add Update to serviceMenuDeps:

```go
			Update: func() error {
				return runUpdate(deps.UI, version.Current, nil, nil)
			},
```

- [ ] **Step 3: Modify client.go**

Add `"netsgo/pkg/version"` to imports.

In `ManageClientWith`, add Update to serviceMenuDeps:

```go
			Update: func() error {
				return runUpdate(deps.UI, version.Current, nil, nil)
			},
```

- [ ] **Step 4: Verify compilation**

Run: `go build -tags dev ./cmd/netsgo`
Expected: SUCCESS

- [ ] **Step 5: Run tests**

Run: `go test -tags dev ./internal/manage/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/manage/service_menu.go internal/manage/server.go internal/manage/client.go
git commit -m "feat: wire Update into manage service menus"
```

---

## Task 9: Update Placeholder Command

**Files:**
- Modify: `cmd/netsgo/cmd_update.go`

- [ ] **Step 1: Update the placeholder**

```go
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update NetsGo binary (use 'manage' or 'upgrade' instead)",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("For managed services: run 'netsgo manage' and select 'Update'")
		fmt.Println("To upgrade with current binary: run 'netsgo upgrade'")
		fmt.Println("Manual download: https://github.com/zsio/netsgo/releases")
	},
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
```

- [ ] **Step 2: Verify compilation**

Run: `go build -tags dev ./cmd/netsgo`
Expected: SUCCESS

- [ ] **Step 3: Commit**

```bash
git add cmd/netsgo/cmd_update.go
git commit -m "chore: update placeholder for netsgo update"
```

---

## Final Verification

- [ ] Run all tests: `go test -tags dev ./...`
- [ ] Build: `make build`
