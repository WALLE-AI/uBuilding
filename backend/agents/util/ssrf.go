package util

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ---------------------------------------------------------------------------
// CheckSSRF — defensive URL validation for outbound fetch tools (WebFetch).
// Rejects URLs that would let an attacker pivot to internal services:
//   - non-http(s) schemes
//   - URLs with embedded credentials (user:pass@)
//   - loopback / link-local / private / ULA IP hosts
//   - the AWS/GCP/Azure metadata IP 169.254.169.254 specifically
//
// The check resolves the hostname once; if any resolved address is disallowed
// the whole URL is rejected (fail-closed). DNS resolution errors also fail.
// ---------------------------------------------------------------------------

// CheckSSRFOptions tunes the behavior (primarily for tests).
type CheckSSRFOptions struct {
	// AllowLoopback permits 127.0.0.0/8 and ::1. Used by httptest servers.
	// Metadata IP 169.254.169.254 is still always rejected.
	AllowLoopback bool
}

// CheckSSRF validates rawURL against common SSRF pitfalls.
func CheckSSRF(rawURL string) error {
	return CheckSSRFWithOptions(rawURL, CheckSSRFOptions{})
}

// CheckSSRFWithOptions is the extensible variant.
func CheckSSRFWithOptions(rawURL string, opts CheckSSRFOptions) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL scheme must be http or https, got %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL must not contain embedded credentials")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL must have a host")
	}

	// Try literal IP first; only resolve DNS if host is not an IP.
	var addrs []net.IP
	if ip := net.ParseIP(host); ip != nil {
		addrs = []net.IP{ip}
	} else {
		hosts, err := net.LookupHost(host)
		if err != nil {
			return fmt.Errorf("failed to resolve host %q: %w", host, err)
		}
		for _, h := range hosts {
			if ip := net.ParseIP(h); ip != nil {
				addrs = append(addrs, ip)
			}
		}
		if len(addrs) == 0 {
			return fmt.Errorf("no IP addresses resolved for host %q", host)
		}
	}

	for _, ip := range addrs {
		if err := checkIP(ip, opts); err != nil {
			return fmt.Errorf("host %q resolves to disallowed address %s: %w", host, ip, err)
		}
	}
	return nil
}

// checkIP returns an error if the IP belongs to a disallowed range.
func checkIP(ip net.IP, opts CheckSSRFOptions) error {
	// Cloud metadata endpoint — always blocked regardless of AllowLoopback.
	if ip.Equal(net.IPv4(169, 254, 169, 254)) {
		return fmt.Errorf("cloud metadata address blocked")
	}

	if ip.IsLoopback() {
		if opts.AllowLoopback {
			return nil
		}
		return fmt.Errorf("loopback address blocked")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("link-local address blocked")
	}
	if ip.IsMulticast() {
		return fmt.Errorf("multicast address blocked")
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("unspecified address blocked")
	}
	if isPrivate(ip) {
		return fmt.Errorf("private address blocked")
	}
	return nil
}

// isPrivate covers RFC1918 (10/8, 172.16/12, 192.168/16), CGNAT (100.64/10),
// and IPv6 ULA (fc00::/7). Mirrors what net.IP.IsPrivate does in Go 1.17+
// but we reimplement to keep the intent explicit.
func isPrivate(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 10:
			return true
		case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
			return true
		case ip4[0] == 192 && ip4[1] == 168:
			return true
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127: // CGNAT 100.64.0.0/10
			return true
		}
		return false
	}
	// IPv6 ULA: fc00::/7
	if len(ip) == net.IPv6len && (ip[0]&0xfe) == 0xfc {
		return true
	}
	return false
}

// HostnameFromURL extracts the lowercased hostname from rawURL without
// resolving DNS. Useful for blocklist matching.
func HostnameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}
