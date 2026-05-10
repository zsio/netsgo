package updater

import (
	"strings"
	"testing"
)

func TestParseReleaseIndex(t *testing.T) {
	raw := `{
		"schema": 1,
		"project": "netsgo",
		"generated_at": "2026-05-10T12:00:00Z",
		"channels": {
			"stable": {"latest": "v0.1.0", "release_urls": [{"provider": "github", "url": "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0.json"}]},
			"beta": {"latest": "v0.1.0-beta.1", "release_urls": [{"provider": "github", "url": "https://raw.githubusercontent.com/zsio/netsgo/release-index/updates/index-v1/releases/v0.1.0-beta.1.json"}]}
		}
	}`
	idx, err := ParseReleaseIndex(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("ParseReleaseIndex returned error: %v", err)
	}
	if idx.Channels["stable"].Latest != "v0.1.0" {
		t.Fatalf("stable latest mismatch: %+v", idx.Channels["stable"])
	}
}

func TestParseReleaseIndexRejectsInvalidPrerelease(t *testing.T) {
	raw := `{"schema":1,"project":"netsgo","channels":{"beta":{"latest":"v0.1.0-rc.1"}}}`
	_, err := ParseReleaseIndex(strings.NewReader(raw))
	if err == nil {
		t.Fatal("expected invalid prerelease error")
	}
}
