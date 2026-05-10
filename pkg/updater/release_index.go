package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	buildversion "netsgo/pkg/version"
)

const (
	LatestIndexURLCNB    = "https://cnb.cool/zsio/netsgo/-/raw/release-index/updates/index-v1/latest.json"
	LatestIndexURLGitHub = "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/latest.json"
	ReleaseURL           = "https://github.com/zsio/netsgo/releases"
)

var defaultHTTPClient = &http.Client{Timeout: 15 * time.Second}

type ReleaseIndex struct {
	Schema      int                       `json:"schema"`
	Project     string                    `json:"project"`
	GeneratedAt time.Time                 `json:"generated_at"`
	Channels    map[string]ReleaseChannel `json:"channels"`
}

type ReleaseChannel struct {
	Latest      string        `json:"latest"`
	ReleaseURLs []ProviderURL `json:"release_urls"`
}

type ProviderURL struct {
	Provider     string `json:"provider"`
	URL          string `json:"url"`
	RequiresAuth bool   `json:"requires_auth,omitempty"`
}

func ParseReleaseIndex(r io.Reader) (*ReleaseIndex, error) {
	var idx ReleaseIndex
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("decode release index: %w", err)
	}
	if idx.Schema != 1 {
		return nil, fmt.Errorf("unsupported release index schema: %d", idx.Schema)
	}
	if idx.Project != "netsgo" {
		return nil, fmt.Errorf("unexpected release index project: %q", idx.Project)
	}
	if idx.Channels == nil {
		return nil, fmt.Errorf("release index channels missing")
	}
	for channel, entry := range idx.Channels {
		switch channel {
		case buildversion.ChannelStable:
			if entry.Latest != "" && !buildversion.IsStableTag(entry.Latest) {
				return nil, fmt.Errorf("invalid stable latest: %q", entry.Latest)
			}
		case buildversion.ChannelBeta:
			if entry.Latest != "" && !buildversion.IsBetaTag(entry.Latest) {
				return nil, fmt.Errorf("invalid beta latest: %q", entry.Latest)
			}
		default:
			return nil, fmt.Errorf("unknown release channel: %q", channel)
		}
	}
	return &idx, nil
}

func FetchReleaseIndex(client *http.Client, urls ...string) (*ReleaseIndex, string, error) {
	if client == nil {
		client = defaultHTTPClient
	}
	var lastErr error
	for _, url := range urls {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = fmt.Errorf("fetch %s: %w", url, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("fetch %s: status %d", url, resp.StatusCode)
			continue
		}
		idx, err := ParseReleaseIndex(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("fetch %s: %w", url, err)
			continue
		}
		return idx, url, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no release index urls configured")
	}
	return nil, "", lastErr
}
