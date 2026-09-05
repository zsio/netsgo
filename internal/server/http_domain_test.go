package server

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateHTTPDomain(t *testing.T) {
	valid := []string{
		"x.com", "APP.X.COM.", "*.x.com", "*.*.x.com", "*.*.*.x.com",
		"*.a.b.com", "*.*.*.a.b.com", "*.xn--bcher-kva.example", "*.a-b.example",
		"*." + strings.Repeat("a", 63) + ".example",
	}
	for _, domain := range valid {
		t.Run("valid/"+domain, func(t *testing.T) {
			if err := validateHTTPDomain(domain); err != nil {
				t.Fatalf("valid domain %q: %v", domain, err)
			}
		})
	}
	invalid := []string{
		"", "*", "*.", "*.com", "*.localhost", "*.*.*.*.x.com",
		"shop*.x.com", "shop.*.x.com", "*.shop.*.x.com", "**.x.com", "*.*x.com",
		"*.x.*", "*.x..com", "*.x.com..", "*..x.com", ".x.com",
		" *.x.com", "*.x.com ", "*.x. com", "*.x.com\n", "*.x.\tcom",
		"https://*.x.com", "*.x.com/path", "*.x.com?x=1", "*.x.com#fragment",
		"*.x.com:80", "*.x.com:443", "*.127.0.0.1", "127.0.0.1", "[::1]",
		"*.用户.com", "*.-x.com", "*.x-.com", "*.x_y.com", "*.x@x.com",
		"*." + strings.Repeat("a", 64) + ".com",
	}
	for _, domain := range invalid {
		t.Run("invalid/"+domain, func(t *testing.T) {
			if err := validateHTTPDomain(domain); err == nil {
				t.Fatalf("invalid domain %q accepted", domain)
			}
		})
	}
	// Count the wildcard labels in the overall DNS length limit as well.
	suffix := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 59)
	if len("*."+suffix) != 253 {
		t.Fatal("incorrect boundary fixture")
	}
	if err := validateHTTPDomain("*." + suffix + "."); err != nil {
		t.Fatalf("253-character pattern with terminal dot: %v", err)
	}
	if err := validateHTTPDomain("*." + suffix + "d"); err == nil {
		t.Fatal("254-character pattern accepted")
	}
}

func TestHTTPWildcardsNeverValidateAsManagementAddresses(t *testing.T) {
	for _, domain := range []string{"*.x.com", "*.*.x.com", "*.*.*.x.com"} {
		if err := validateDomain(domain); err == nil {
			t.Errorf("exact-only domain validator accepted %q", domain)
		}
		if _, err := validateServerAddr("https://" + domain); err == nil {
			t.Errorf("management address accepted %q", domain)
		}
	}
}

func TestHTTPDomainMatches(t *testing.T) {
	cases := []struct {
		pattern string
		host    string
		want    bool
	}{
		{"*.x.com", "shop.x.com", true},
		{"*.x.com", "x.com", false},
		{"*.x.com", "a.b.x.com", false},
		{"*.a.b.com", "shop.a.b.com", true},
		{"*.a.b.com", "a.b.com", false},
		{"*.a.b.com", "a.shop.a.b.com", false},
		{"*.*.x.com", "a.b.x.com", true},
		{"*.*.x.com", "a.x.com", false},
		{"*.*.x.com", "a.b.c.x.com", false},
		{"*.*.*.x.com", "a.b.c.x.com", true},
		{"*.*.*.x.com", "a.b.c.d.x.com", false},
		{"*.*.*.a.b.com", "x.y.z.a.b.com", true},
		{"*.X.COM.", "SHOP.X.COM.:8443", true},
		{"shop.x.com", "SHOP.X.COM.:443", true},
		{"shop.x.com", "shop.x.com:80", true},
		{"shop.x.com", "shop.x.com:65535", true},
		{"*.x.com", "xn--bcher-kva.x.com", true},
		{"*.x.com", "a-b.x.com", true},
		{"*.x.com", "shop.notx.com", false},
		{"*.x.com", "shop.x.com.evil.test", false},
		{"*.x.com", "shopx.com", false},
		{"*.x.com", ".x.com", false},
		{"*.*.x.com", "a..x.com", false},
		{"*.x.com", "*.x.com", false},
		{"*.x.com", "shop.x.com..", false},
		{"*.x.com", "shop.x.com:0", false},
		{"*.x.com", "shop.x.com:65536", false},
		{"*.x.com", "shop.x.com:bad", false},
		{"*.x.com", "https://shop.x.com", false},
		{"*.x.com", "shop.x.com/path", false},
		{"*.x.com", "shop.x.com\r\n", false},
		{"*.x.com", "shop.x.com@evil.com", false},
		{"*.x.com", strings.Repeat("a", 64) + ".x.com", false},
		{"*.0.0.1", "127.0.0.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+"/"+tc.host, func(t *testing.T) {
			if got := httpDomainMatches(tc.pattern, tc.host); got != tc.want {
				t.Errorf("match(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
			}
		})
	}
}

func TestHTTPDomainCandidatePriority(t *testing.T) {
	want := []string{"shop.dev.eu.x.com", "*.dev.eu.x.com", "*.*.eu.x.com", "*.*.*.x.com"}
	if got := httpDomainCandidates("SHOP.DEV.EU.X.COM.:8443"); !reflect.DeepEqual(got, want) {
		t.Fatalf("candidate order = %v, want %v", got, want)
	}
	if got := httpDomainCandidates("x.com"); !reflect.DeepEqual(got, []string{"x.com"}) {
		t.Fatalf("apex candidates = %v", got)
	}
}

func TestHTTPDomainOwnershipConflicts(t *testing.T) {
	cases := []struct {
		left, right       string
		sameOwnerConflict bool
		otherConflict     bool
	}{
		{"app.x.com", "APP.X.COM.", true, true},
		{"*.x.com", "*.X.COM.", true, true},
		{"*.x.com", "app.x.com", false, true},
		{"*.*.x.com", "*.dev.x.com", false, true},
		{"*.*.*.x.com", "app.dev.eu.x.com", false, true},
		{"*.x.com", "*.*.x.com", false, false},
		{"*.x.com", "x.com", false, false},
		{"*.dev.x.com", "*.prod.x.com", false, false},
		{"*.x.com", "app.notx.com", false, false},
	}
	for _, tc := range cases {
		for _, pair := range [][2]string{{tc.left, tc.right}, {tc.right, tc.left}} {
			if got := httpDomainsConflict(pair[0], "alice", pair[1], "alice"); got != tc.sameOwnerConflict {
				t.Errorf("same owner conflict(%q, %q) = %v", pair[0], pair[1], got)
			}
			if got := httpDomainsConflict(pair[0], "alice", pair[1], "bob"); got != tc.otherConflict {
				t.Errorf("cross owner conflict(%q, %q) = %v", pair[0], pair[1], got)
			}
			if tc.otherConflict && !httpDomainsConflict(pair[0], "", pair[1], "") {
				t.Errorf("unknown owners must not authorize overlapping rules")
			}
		}
	}
}

func TestHTTPDomainOverlapAgreesWithConcreteExpansion(t *testing.T) {
	// Independent oracle: expand all wildcards into literal names from the
	// complete fixture alphabet, then compare sets rather than pattern labels.
	patterns := []string{"x.com", "a.x.com", "b.x.com", "*.x.com", "a.a.x.com", "a.b.x.com", "*.a.x.com", "*.b.x.com", "*.*.x.com", "*.a.a.x.com", "*.b.a.x.com", "*.*.a.x.com", "*.*.*.x.com"}
	expand := func(pattern string) map[string]bool {
		names := []string{pattern}
		for strings.Contains(names[0], "*") {
			next := make([]string, 0, len(names)*2)
			for _, name := range names {
				next = append(next, strings.Replace(name, "*", "a", 1), strings.Replace(name, "*", "b", 1))
			}
			names = next
		}
		set := map[string]bool{}
		for _, name := range names {
			set[name] = true
		}
		return set
	}
	for _, left := range patterns {
		for _, right := range patterns {
			a, b := expand(left), expand(right)
			intersects := false
			for name := range a {
				intersects = intersects || b[name]
				if !httpDomainMatches(left, name) {
					t.Fatalf("expanded name %q does not match %q", name, left)
				}
			}
			if got := httpDomainsOverlap(left, right); got != intersects {
				t.Errorf("overlap(%q, %q) = %v, concrete intersection = %v", left, right, got, intersects)
			}
		}
	}
}
