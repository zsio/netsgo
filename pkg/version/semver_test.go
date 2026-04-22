package version

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input   string
		want    Semver
		wantErr bool
	}{
		{"v1.2.3", Semver{1, 2, 3}, false},
		{"1.2.3", Semver{1, 2, 3}, false},
		{"v0.0.1", Semver{0, 0, 1}, false},
		{"dev", Semver{}, true},
		{"", Semver{}, true},
		{"v1.2", Semver{}, true},
		{"v1.2.3.4", Semver{}, true},
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
