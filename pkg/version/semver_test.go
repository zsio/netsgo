package version

import "testing"

func TestReleaseTagValidation(t *testing.T) {
	tests := []struct {
		input      string
		stable     bool
		beta       bool
		releaseTag bool
	}{
		{"v1.2.3", true, false, true},
		{"v0.1.0-beta.1", false, true, true},
		{"1.2.3", false, false, false},
		{"v1.2.3-beta", false, false, false},
		{"v1.2.3-beta.0", false, false, false},
		{"v1.2.3-rc.1", false, false, false},
		{"v01.2.3", false, false, false},
		{"dev", false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := IsStableTag(tt.input); got != tt.stable {
				t.Fatalf("IsStableTag(%q) = %v, want %v", tt.input, got, tt.stable)
			}
			if got := IsBetaTag(tt.input); got != tt.beta {
				t.Fatalf("IsBetaTag(%q) = %v, want %v", tt.input, got, tt.beta)
			}
			if got := IsReleaseTag(tt.input); got != tt.releaseTag {
				t.Fatalf("IsReleaseTag(%q) = %v, want %v", tt.input, got, tt.releaseTag)
			}
		})
	}
}

func TestComparableBase(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{"v0.1.0", "v0.1.0", true},
		{"v0.1.0-beta.5", "v0.1.0-beta.5", true},
		{"v0.1.0-3-gabc123", "v0.1.0", true},
		{"v0.1.0-3-gabc123-dirty", "v0.1.0", true},
		{"v0.1.0-beta.5-3-gabc123", "v0.1.0-beta.5", true},
		{"v0.1.0-beta.5-3-gabc123-dirty", "v0.1.0-beta.5", true},
		{"v0.1.0-rc.1-3-gabc123", "", false},
		{"dev", "", false},
		{"snapshot", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := ComparableBase(tt.input)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("ComparableBase(%q) = %q, %v; want %q, %v", tt.input, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCompareReleaseTags(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.2.0", "v1.1.0", 1},
		{"v1.1.0", "v1.2.0", -1},
		{"v1.2.3-beta.2", "v1.2.3-beta.1", 1},
		{"v1.2.3-beta.10", "v1.2.3-beta.2", 1},
		{"v1.2.3", "v1.2.3-beta.1", 1},
		{"v1.2.3-beta.1", "v1.2.3", -1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got, err := Compare(tt.a, tt.b)
			if err != nil {
				t.Fatalf("Compare returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("Compare = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNormalizeVersionStringRequiresVTag(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"v1.2.3", "v1.2.3", false},
		{"netsgo version v1.2.3 (abcdef1, 2026-04-25)", "v1.2.3", false},
		{"netsgo version v1.2.3-beta.1", "v1.2.3-beta.1", false},
		{"netsgo version 1.2.3", "", true},
		{"netsgo version v1.2.3-rc.1", "", true},
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
