package updater

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	buildversion "netsgo/pkg/version"
)

const (
	githubBaseURL    = "https://github.com/zsio/netsgo"
	cnbBaseURL       = "https://cnb.cool/zsio/netsgo"
	releaseAssetTpl  = "netsgo_%s_%s_%s.tar.gz"
	checksumsAsset   = "checksums.txt"
	releaseTagMarker = "/releases/tag/"
)

type DownloadChannel string

type releaseTrack int

const (
	ChannelGitHub DownloadChannel = "github"
	ChannelCNB    DownloadChannel = "cnb"
)

const (
	releaseTrackAny releaseTrack = iota
	releaseTrackStable
	releaseTrackBeta
)

var errNoCompatibleRelease = errors.New("no compatible release found")

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

var releaseTagLinkRe = regexp.MustCompile(releaseTagMarker + `[^"'<>\s]+`)
var releaseTagLinkMaxReadBytes int64 = 1 << 20

func fetchLatestVersion(channel DownloadChannel, track releaseTrack) (string, error) {
	base := githubBaseURL
	if channel == ChannelCNB {
		base = cnbBaseURL + "/-"
	}
	return fetchLatestVersionWithClient(base+"/releases", defaultHTTPClient, track)
}

func fetchLatestVersionWithClient(url string, client *http.Client, track releaseTrack) (string, error) {
	currentURL := url

	for redirectCount := 0; redirectCount < 5; redirectCount++ {
		resp, err := client.Get(currentURL)
		if err != nil {
			return "", fmt.Errorf("fetch latest: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusFound, http.StatusMovedPermanently, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
			location := resp.Header.Get("Location")
			if location == "" {
				_ = resp.Body.Close()
				return "", fmt.Errorf("no redirect location")
			}
			resolvedLocation, err := resolveLocation(resp.Request.URL, location)
			_ = resp.Body.Close()
			if err != nil {
				return "", err
			}

			if version, err := extractVersionFromReleaseTagURL(resolvedLocation.String()); err == nil && releaseVersionMatchesTrack(version, track) {
				return version, nil
			}
			currentURL = resolvedLocation.String()
			continue
		case http.StatusOK:
			pathVersion, err := extractVersionFromReleaseTagPath(resp.Request.URL.Path)
			if err == nil && releaseVersionMatchesTrack(pathVersion, track) {
				_ = resp.Body.Close()
				return pathVersion, nil
			}

			version, bodyErr := extractLatestReleaseTagFromBody(io.LimitReader(resp.Body, releaseTagLinkMaxReadBytes), track)
			_ = resp.Body.Close()
			if bodyErr == nil {
				return version, nil
			}
			return "", bodyErr
		default:
			_ = resp.Body.Close()
			return "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
		}
	}

	return "", fmt.Errorf("too many redirects when checking latest version")
}

func extractVersionFromReleaseTagURL(location string) (string, error) {
	parsed, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("invalid redirect location: %q", location)
	}
	return extractVersionFromReleaseTagPath(parsed.Path)
}

func resolveLocation(base *url.URL, location string) (*url.URL, error) {
	resolved, err := base.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("invalid redirect location: %q", location)
	}
	return resolved, nil
}

func extractLatestReleaseTagFromBody(reader io.Reader, track releaseTrack) (string, error) {
	payload, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("fetch latest body: %w", err)
	}

	version, err := selectLatestReleaseTag(releaseTagLinkRe.FindAllString(string(payload), -1), track)
	if err != nil {
		return "", err
	}
	return version, nil
}

func selectLatestReleaseTag(matches []string, track releaseTrack) (string, error) {
	seen := map[string]bool{}
	var bestTag string
	var bestVersion buildversion.Semver
	found := false

	for _, match := range matches {
		tag, err := extractVersionFromReleaseTagPath(match)
		if err != nil {
			continue
		}
		normalized, err := buildversion.NormalizeVersionString(tag)
		if err != nil {
			continue
		}
		if seen[normalized] {
			continue
		}
		seen[normalized] = true

		parsed, err := buildversion.ParseSemver(normalized)
		if err != nil {
			continue
		}
		if !semverMatchesReleaseTrack(parsed, track) {
			continue
		}

		if !found || parsed.Compare(bestVersion) > 0 {
			bestTag = tag
			bestVersion = parsed
			found = true
		}
	}

	if !found {
		return "", errNoCompatibleRelease
	}
	return bestTag, nil
}

func releaseVersionMatchesTrack(tag string, track releaseTrack) bool {
	normalized, err := buildversion.NormalizeVersionString(tag)
	if err != nil {
		return false
	}
	parsed, err := buildversion.ParseSemver(normalized)
	if err != nil {
		return false
	}
	return semverMatchesReleaseTrack(parsed, track)
}

func semverMatchesReleaseTrack(parsed buildversion.Semver, track releaseTrack) bool {
	switch track {
	case releaseTrackStable:
		return parsed.Prerelease == ""
	case releaseTrackBeta:
		return strings.HasPrefix(parsed.Prerelease, "beta.")
	default:
		return true
	}
}

func extractVersionFromReleaseTagPath(path string) (string, error) {
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
	releasePath := "releases/download"
	if channel == ChannelCNB {
		base = cnbBaseURL
		releasePath = "-/releases/download"
	}
	assetName := fmt.Sprintf(releaseAssetTpl, strings.TrimPrefix(version, "v"), goos, goarch)
	return fmt.Sprintf("%s/%s/%s/%s", base, releasePath, version, assetName)
}

func buildChecksumsURL(channel DownloadChannel, version string) string {
	base := githubBaseURL
	releasePath := "releases/download"
	if channel == ChannelCNB {
		base = cnbBaseURL
		releasePath = "-/releases/download"
	}
	return fmt.Sprintf("%s/%s/%s/%s", base, releasePath, version, checksumsAsset)
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
