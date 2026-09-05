package server

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

const maxHTTPDomainWildcards = 3

// HTTP patterns consist of up to three leading '*.' labels and a fixed FQDN.
// Each star consumes exactly one nonempty label; it never includes the apex.
// Management addresses continue to use the exact-only validateDomain function.
func validateHTTPDomain(domain string) error {
	if domain != strings.TrimSpace(domain) {
		return fmt.Errorf("domain cannot contain whitespace")
	}
	if len(strings.TrimSuffix(domain, ".")) > 253 {
		return fmt.Errorf("domain length cannot exceed 253 characters")
	}
	suffix := domain
	count := 0
	for strings.HasPrefix(suffix, "*.") {
		count++
		suffix = strings.TrimPrefix(suffix, "*.")
	}
	if count > maxHTTPDomainWildcards {
		return fmt.Errorf("domain supports at most %d leading wildcard labels", maxHTTPDomainWildcards)
	}
	if strings.Contains(suffix, "*") {
		return fmt.Errorf("wildcards must be consecutive complete labels at the start of the domain")
	}
	return validateDomain(suffix)
}

func canonicalHTTPDomain(domain string) string {
	return strings.TrimSuffix(strings.ToLower(domain), ".")
}

// Unlike a configured domain, the request authority can contain a port. Do not
// accept URL syntax or forwarded headers as an alternative routing authority.
func canonicalHTTPRouteHost(host string) string {
	if host == "" || strings.ContainsAny(host, "* /\\?#@\t\r\n") {
		return ""
	}
	if name, port, err := net.SplitHostPort(host); err == nil {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ""
		}
		host = name
	} else if strings.Contains(host, ":") {
		// Preserve exact loopback routing used by existing installations/tests.
		if net.ParseIP(strings.Trim(host, "[]")) == nil {
			return ""
		}
	}
	if strings.HasSuffix(host, "..") {
		return ""
	}
	host = canonicalHTTPDomain(strings.Trim(host, "[]"))
	if net.ParseIP(host) != nil || host == "localhost" {
		return host
	}
	if validateDomain(host) != nil {
		return ""
	}
	return host
}

// Enumerating the only possible patterns gives a bounded, deterministic lookup
// order: exact, then increasingly general suffixes. No regex or map order.
func httpDomainCandidates(host string) []string {
	host = canonicalHTTPRouteHost(host)
	if host == "" {
		return nil
	}
	result := []string{host}
	if net.ParseIP(host) != nil {
		return result
	}
	labels := strings.Split(host, ".")
	for count := 1; count <= maxHTTPDomainWildcards && count <= len(labels)-2; count++ {
		result = append(result, strings.Repeat("*.", count)+strings.Join(labels[count:], "."))
	}
	return result
}

func httpDomainMatches(pattern, host string) bool {
	pattern = canonicalHTTPDomain(pattern)
	for _, candidate := range httpDomainCandidates(host) {
		if candidate == pattern {
			return true
		}
	}
	return false
}

// Patterns can overlap only at the same depth, with no incompatible literals.
// Inputs are validated configuration patterns, not arbitrary glob expressions.
func httpDomainsOverlap(left, right string) bool {
	a := strings.Split(canonicalHTTPDomain(left), ".")
	b := strings.Split(canonicalHTTPDomain(right), ".")
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != "*" && b[i] != "*" && a[i] != b[i] {
			return false
		}
	}
	return true
}

func httpDomainsConflict(left, leftOwner, right, rightOwner string) bool {
	if !httpDomainsOverlap(left, right) {
		return false
	}
	return canonicalHTTPDomain(left) == canonicalHTTPDomain(right) ||
		leftOwner == "" || rightOwner == "" || leftOwner != rightOwner
}
