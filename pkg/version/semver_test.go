package version

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input   string
		want    Semver
		wantErr bool
	}{
		{"v1.2.3", Semver{Major: 1, Minor: 2, Patch: 3}, false},
		{"1.2.3", Semver{Major: 1, Minor: 2, Patch: 3}, false},
		{"v0.0.1", Semver{Major: 0, Minor: 0, Patch: 1}, false},
		{"1.2.3-beta.1", Semver{Major: 1, Minor: 2, Patch: 3, Prerelease: "beta.1"}, false},
		{"1.2.3+build.5", Semver{Major: 1, Minor: 2, Patch: 3, BuildMetadata: "build.5"}, false},
		{"v1.2.3-beta.1+build.5", Semver{Major: 1, Minor: 2, Patch: 3, Prerelease: "beta.1", BuildMetadata: "build.5"}, false},
		{"dev", Semver{}, true},
		{"", Semver{}, true},
		{"v1.2", Semver{}, true},
		{"v1.2.3.4", Semver{}, true},
		{"1.2.3+", Semver{}, true},
		{"1.2.3-beta+bad space", Semver{}, true},
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
		{"v1.2.3-beta.2", "v1.2.3-beta.1", 1},
		{"v1.2.3-beta.10", "v1.2.3-beta.2", 1},
		{"v1.2.3-beta", "v1.2.3-beta.1", -1},
		{"v1.2.3", "v1.2.3-beta.1", 1},
		{"v1.2.3-beta.1", "v1.2.3", -1},
		{"v1.2.3+build.1", "v1.2.3+build.2", 0},
		{"v1.2.3-beta.1+aaa", "v1.2.3-beta.1+bbb", 0},
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

func TestNormalizeVersionString(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"v1.2.3", "1.2.3", false},
		{"1.2.3", "1.2.3", false},
		{"netsgo version 1.2.3", "1.2.3", false},
		{"netsgo version 1.2.3 (abcdef1, 2026-04-25)", "1.2.3", false},
		{"netsgo version v1.2.3-beta.1 (abcdef1, 2026-04-25)", "1.2.3-beta.1", false},
		{"netsgo version 1.2.3+build.5 (abcdef1, 2026-04-25)", "1.2.3+build.5", false},
		{"netsgo version v1.2.3-beta.1+build.5", "1.2.3-beta.1+build.5", false},
		{"dev", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeVersionString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeVersionString(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("NormalizeVersionString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
