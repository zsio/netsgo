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
