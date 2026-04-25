package updater

import (
	"fmt"
	"net/http"
	"path"
	"runtime"
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
	defer func() { _ = resp.Body.Close() }()

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
	assetName := fmt.Sprintf(releaseAssetTpl, version, goos, goarch)
	return fmt.Sprintf("%s/releases/download/%s/%s", base, version, assetName)
}

func platformAssetURL(channel DownloadChannel, version string) string {
	return buildDownloadURL(channel, version, runtime.GOOS, runtime.GOARCH)
}
