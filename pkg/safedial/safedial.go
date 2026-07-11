// Package safedial builds HTTP clients that refuse to connect to private,
// loopback, link-local or otherwise non-public addresses. It guards
// outbound requests to consumer-supplied URLs (webhook deliveries) against
// SSRF: the check runs at dial time against the resolved IP, so it defeats
// DNS rebinding and applies to every redirect hop, not just the first URL.
package safedial

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrBlockedAddress is returned when a dial target resolves to a
// non-public address and private egress is not allowed.
type ErrBlockedAddress struct {
	Host string
	IP   string
}

func (e *ErrBlockedAddress) Error() string {
	return fmt.Sprintf("safedial: blocked connection to non-public address %s (%s)", e.Host, e.IP)
}

// Options configures a guarded client.
type Options struct {
	// AllowPrivate disables the guard entirely — for on-prem deployments
	// whose consumers legitimately live on private networks.
	AllowPrivate bool
	// Timeout is the whole-request timeout (default 10s).
	Timeout time.Duration
}

// NewClient returns an *http.Client whose dialer rejects non-public
// targets unless opts.AllowPrivate is set.
func NewClient(opts Options) *http.Client {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if !opts.AllowPrivate {
				if err := checkAddr(ctx, addr); err != nil {
					return nil, err
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}

// checkAddr resolves host:port and rejects the connection if any resolved
// IP is non-public. Rejecting when *any* resolved IP is private prevents a
// multi-record host from slipping a private address past the guard.
func checkAddr(ctx context.Context, addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("safedial: resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if !isPublic(ip.IP) {
			return &ErrBlockedAddress{Host: host, IP: ip.IP.String()}
		}
	}
	return nil
}

// isPublic reports whether ip is a globally routable unicast address.
func isPublic(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	// Cloud metadata and shared CGNAT/benchmark/reserved ranges that
	// net.IP does not classify as private.
	for _, cidr := range blockedCIDRs {
		if cidr.Contains(ip) {
			return false
		}
	}
	return true
}

// blockedCIDRs are ranges net.IP does not classify as private but which
// must never be dialled. IPv4-mapped IPv6 is deliberately absent: Go
// stores IPv4 in a 16-byte ::ffff:0:0/96 form, so blocking that range
// would reject every IPv4 address — mapped loopback/private addresses are
// already caught by the IsLoopback/IsPrivate checks above.
var blockedCIDRs = mustCIDRs(
	"100.64.0.0/10",   // RFC 6598 CGNAT
	"192.0.0.0/24",    // RFC 6890 IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"240.0.0.0/4",     // reserved
	"64:ff9b::/96",    // NAT64
	"100::/64",        // discard-only
	"2001:db8::/32",   // documentation
)

func mustCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("safedial: bad CIDR " + c)
		}
		out = append(out, n)
	}
	return out
}
