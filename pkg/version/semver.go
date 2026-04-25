package version

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type Semver struct {
	Major         int
	Minor         int
	Patch         int
	Prerelease    string
	BuildMetadata string
}

func ParseSemver(s string) (Semver, error) {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, "+", 2)
	coreAndPrerelease := parts[0]
	buildMetadata := ""
	if len(parts) == 2 {
		buildMetadata = parts[1]
		if err := validateSemverIdentifiers(buildMetadata, "build metadata", s); err != nil {
			return Semver{}, err
		}
	}

	parts = strings.SplitN(coreAndPrerelease, "-", 2)
	core := parts[0]
	prerelease := ""
	if len(parts) == 2 {
		prerelease = parts[1]
		if err := validateSemverIdentifiers(prerelease, "prerelease", s); err != nil {
			return Semver{}, err
		}
	}
	parts = strings.Split(core, ".")
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
	return Semver{Major: major, Minor: minor, Patch: patch, Prerelease: prerelease, BuildMetadata: buildMetadata}, nil
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
	if s.Prerelease == other.Prerelease {
		return 0
	}
	if s.Prerelease == "" {
		return 1
	}
	if other.Prerelease == "" {
		return -1
	}
	return comparePrerelease(s.Prerelease, other.Prerelease)
}

func NormalizeVersionString(s string) (string, error) {
	for _, field := range strings.Fields(s) {
		normalized := strings.TrimPrefix(field, "v")
		if _, err := ParseSemver(normalized); err == nil {
			return normalized, nil
		}
	}
	return "", fmt.Errorf("no semver found in %q", s)
}

func comparePrerelease(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if aParts[i] == bParts[i] {
			continue
		}
		aNum, aErr := strconv.Atoi(aParts[i])
		bNum, bErr := strconv.Atoi(bParts[i])
		switch {
		case aErr == nil && bErr == nil:
			if aNum > bNum {
				return 1
			}
			if aNum < bNum {
				return -1
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		case aParts[i] > bParts[i]:
			return 1
		default:
			return -1
		}
	}
	if len(aParts) > len(bParts) {
		return 1
	}
	if len(aParts) < len(bParts) {
		return -1
	}
	return 0
}

func validateSemverIdentifiers(value, field, input string) error {
	if value == "" {
		return fmt.Errorf("invalid %s: %q", field, input)
	}

	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return fmt.Errorf("invalid %s: %q", field, input)
		}
		for _, r := range identifier {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				return fmt.Errorf("invalid %s: %q", field, input)
			}
		}
	}

	return nil
}
