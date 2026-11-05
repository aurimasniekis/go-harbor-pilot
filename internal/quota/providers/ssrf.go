package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

// ErrDestinationBlocked is returned when an outbound connection targets an
// address disallowed by the SSRF policy.
var ErrDestinationBlocked = errors.New("destination blocked")

// SSRFPolicy controls which resolved destinations an outbound client may
// connect to. It is enforced at dial time on the resolved IP, which also
// covers redirects and DNS rebinding (each connection is checked).
type SSRFPolicy struct {
	// AllowPrivate permits RFC1918, unique-local, loopback and link-local
	// destinations. Cloud metadata addresses stay blocked regardless unless
	// AllowMetadata is set.
	AllowPrivate bool
	// AllowMetadata permits well-known cloud metadata endpoints. Never set
	// this by default.
	AllowMetadata bool
	// AllowedCIDRs are explicitly allowed private prefixes (used when
	// AllowPrivate is false but one internal service must be reachable).
	AllowedCIDRs []netip.Prefix
}

var metadataPrefixes = []netip.Prefix{
	netip.MustParsePrefix("169.254.169.254/32"),
	netip.MustParsePrefix("169.254.170.2/32"),
	netip.MustParsePrefix("100.100.100.200/32"),
	netip.MustParsePrefix("fd00:ec2::254/128"),
}

var alwaysBlocked = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("ff00::/8"),
	netip.MustParsePrefix("::/128"),
}

// CheckAddr reports whether an IP is allowed by the policy.
func (p SSRFPolicy) CheckAddr(ip netip.Addr) error {
	ip = ip.Unmap()
	for _, pre := range metadataPrefixes {
		if pre.Contains(ip) && !p.AllowMetadata {
			return fmt.Errorf("%w: cloud metadata address", ErrDestinationBlocked)
		}
	}
	for _, pre := range alwaysBlocked {
		if pre.Contains(ip) {
			return fmt.Errorf("%w: reserved address", ErrDestinationBlocked)
		}
	}
	private := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || isCGNAT(ip)
	if !private {
		return nil
	}
	if p.AllowPrivate {
		return nil
	}
	for _, pre := range p.AllowedCIDRs {
		if pre.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("%w: private or local address", ErrDestinationBlocked)
}

func isCGNAT(ip netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

// GuardedTransport returns an http.Transport whose dialer enforces the policy
// on every resolved address. Proxies are disabled so the check cannot be
// bypassed through an environment proxy.
func GuardedTransport(p SSRFPolicy) *http.Transport {
	d := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("%w: invalid address", ErrDestinationBlocked)
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("%w: unresolved address", ErrDestinationBlocked)
			}
			return p.CheckAddr(ip)
		},
	}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return d.DialContext(ctx, network, addr)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}
