// Package netpolicy enforces the outbound network policy for every HTTP
// client Harbor Pilot creates: provider executors, quota adapters, webhooks,
// notification channels, OIDC discovery and diagnostic probes.
//
// The policy is enforced at dial time on the resolved IP address, so it also
// covers redirects and DNS rebinding (each connection is checked).
package netpolicy

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
// address disallowed by the policy.
var ErrDestinationBlocked = errors.New("destination blocked")

// Policy controls which resolved destinations an outbound client may connect
// to. The zero value allows public internet destinations only.
type Policy struct {
	// AllowPrivate permits RFC1918, unique-local, loopback, CGNAT and
	// link-local destinations. Cloud metadata addresses stay blocked unless
	// AllowMetadata is also set.
	AllowPrivate bool
	// AllowMetadata permits well-known cloud metadata endpoints. Never set by
	// default and not exposed through the API.
	AllowMetadata bool
	// AllowedCIDRs are explicitly authorized private prefixes, used when one
	// local endpoint (for example an Ollama host) must be reachable without
	// opening all private ranges.
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

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// CheckAddr reports whether an IP is allowed by the policy.
func (p Policy) CheckAddr(ip netip.Addr) error {
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
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
	if !private || p.AllowPrivate {
		return nil
	}
	for _, pre := range p.AllowedCIDRs {
		if pre.Contains(ip) {
			return nil
		}
	}
	return fmt.Errorf("%w: private or local address", ErrDestinationBlocked)
}

// ParseCIDRs parses a list of CIDR strings or bare addresses.
func ParseCIDRs(items []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(items))
	for _, s := range items {
		if pre, err := netip.ParsePrefix(s); err == nil {
			out = append(out, pre.Masked())
			continue
		}
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR or address %q", s)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// TransportOptions tunes a guarded transport.
type TransportOptions struct {
	DialTimeout           time.Duration
	TLSHandshakeTimeout   time.Duration
	ResponseHeaderTimeout time.Duration // 0 = no limit (streaming upstreams set their own deadline)
	MaxIdleConnsPerHost   int
}

// DefaultTransportOptions are suitable for short control requests.
func DefaultTransportOptions() TransportOptions {
	return TransportOptions{
		DialTimeout:           10 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		MaxIdleConnsPerHost:   8,
	}
}

// Transport returns an http.Transport whose dialer enforces the policy on
// every resolved address. Environment proxies are disabled so the check
// cannot be bypassed.
func Transport(p Policy, o TransportOptions) *http.Transport {
	d := &net.Dialer{
		Timeout:   o.DialTimeout,
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
		TLSHandshakeTimeout:   o.TLSHandshakeTimeout,
		ResponseHeaderTimeout: o.ResponseHeaderTimeout,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   max(o.MaxIdleConnsPerHost, 1),
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// SameHostRedirects returns a CheckRedirect function that allows at most n
// redirects and only to the original host and scheme.
func SameHostRedirects(n int) func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= n {
			return fmt.Errorf("%w: too many redirects", ErrDestinationBlocked)
		}
		first := via[0].URL
		if req.URL.Host != first.Host || req.URL.Scheme != first.Scheme {
			return fmt.Errorf("%w: cross-host redirect", ErrDestinationBlocked)
		}
		return nil
	}
}

// NoRedirects refuses all redirects.
func NoRedirects(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
