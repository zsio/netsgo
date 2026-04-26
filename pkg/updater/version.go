package updater

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	githubBaseURL   = "https://github.com/zsio/netsgo"
	ghproxyBaseURL  = "https://ghproxy.com/https://github.com/zsio/netsgo"
	releaseAssetTpl = "netsgo_%s_%s_%s.tar.gz"
	checksumsAsset  = "checksums.txt"
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

var downloadHTTPClient = &http.Client{Timeout: 60 * time.Second}

var fetchLatestVersionFunc = fetchLatestVersion
var readBuildInfoFunc = debug.ReadBuildInfo
var getenvFunc = os.Getenv

func fetchLatestVersion(channel DownloadChannel) (string, error) {
	base := githubBaseURL
	if channel == ChannelGhproxy {
		base = ghproxyBaseURL
	}
	return fetchLatestVersionWithClient(base+"/releases/latest", defaultHTTPClient)
}

func fetchLatestVersionWithClient(url string, client *http.Client) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch latest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusFound:
		location := resp.Header.Get("Location")
		if location == "" {
			return "", fmt.Errorf("no redirect location")
		}
		return extractVersionFromReleaseTagURL(location)
	case http.StatusOK:
		return extractVersionFromReleaseTagPath(resp.Request.URL.Path)
	default:
		return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}
}

func extractVersionFromReleaseTagURL(location string) (string, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("invalid redirect location: %q", location)
	}
	return extractVersionFromReleaseTagPath(parsed.Path)
}

func extractVersionFromReleaseTagPath(path string) (string, error) {
	const releaseTagMarker = "/releases/tag/"

	idx := strings.LastIndex(path, releaseTagMarker)
	if idx == -1 {
		return "", fmt.Errorf("invalid release tag path: %q", path)
	}

	version := path[idx+len(releaseTagMarker):]
	if version == "" || strings.Contains(version, "/") {
		return "", fmt.Errorf("invalid version in release tag path: %q", path)
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

func buildChecksumsURL(channel DownloadChannel, version string) string {
	base := githubBaseURL
	if channel == ChannelGhproxy {
		base = ghproxyBaseURL
	}
	return fmt.Sprintf("%s/releases/download/%s/%s", base, version, checksumsAsset)
}

func platformAssetURL(channel DownloadChannel, version string) string {
	arch := runtime.GOARCH
	if runtime.GOARCH == "arm" {
		if goarm := currentGOARM(); goarm != "" {
			arch = "armv" + goarm
		}
	}
	return buildDownloadURL(channel, version, runtime.GOOS, arch)
}

func currentGOARM() string {
	info, ok := readBuildInfoFunc()
	if !ok {
		return getenvFunc("GOARM")
	}
	for _, setting := range info.Settings {
		if setting.Key == "GOARM" {
			return setting.Value
		}
	}
	return getenvFunc("GOARM")
}
