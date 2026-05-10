package version

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

var (
	stableTagRe      = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`)
	betaTagRe        = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.([1-9]\d*)$`)
	describeStableRe = regexp.MustCompile(`^(v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*))-\d+-g[0-9A-Fa-f]+(?:-dirty)?$`)
	describeBetaRe   = regexp.MustCompile(`^(v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.([1-9]\d*))-\d+-g[0-9A-Fa-f]+(?:-dirty)?$`)
)

func IsStableTag(v string) bool {
	return stableTagRe.MatchString(strings.TrimSpace(v))
}

func IsBetaTag(v string) bool {
	return betaTagRe.MatchString(strings.TrimSpace(v))
}

func IsReleaseTag(v string) bool {
	v = strings.TrimSpace(v)
	return IsStableTag(v) || IsBetaTag(v)
}

func ChannelForReleaseTag(v string) (string, bool) {
	switch {
	case IsStableTag(v):
		return ChannelStable, true
	case IsBetaTag(v):
		return ChannelBeta, true
	default:
		return "", false
	}
}

// ComparableBase returns the strict release tag used for comparisons.
// The returned value may be extracted from a git describe string, but the
// original version should still be used in public API responses.
func ComparableBase(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if IsReleaseTag(v) && semver.IsValid(v) {
		return v, true
	}
	if matches := describeBetaRe.FindStringSubmatch(v); len(matches) > 1 && semver.IsValid(matches[1]) {
		return matches[1], true
	}
	if matches := describeStableRe.FindStringSubmatch(v); len(matches) > 1 && semver.IsValid(matches[1]) {
		return matches[1], true
	}
	return "", false
}

func Compare(a, b string) (int, error) {
	if !IsReleaseTag(a) || !semver.IsValid(a) {
		return 0, fmt.Errorf("invalid release tag: %q", a)
	}
	if !IsReleaseTag(b) || !semver.IsValid(b) {
		return 0, fmt.Errorf("invalid release tag: %q", b)
	}
	return semver.Compare(a, b), nil
}

func NormalizeVersionString(s string) (string, error) {
	for _, field := range strings.Fields(s) {
		field = strings.Trim(field, "(),")
		if IsReleaseTag(field) && semver.IsValid(field) {
			return field, nil
		}
	}
	return "", fmt.Errorf("no release semver tag found in %q", s)
}
