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
	// AllowPrivate permits RFC1918, unique-local, loopback and CGNAT
	// destinations. Link-local and cloud metadata addresses stay blocked
	// unless AllowMetadata is also set.
	AllowPrivate bool
	// AllowMetadata permits well-known cloud metadata endpoints. Never set by
	// default and not exposed through the API.
	AllowMetadata bool
	// AllowedCIDRs are explicitly authorized private prefixes, used when one
	// local endpoint (for example an Ollama host) must be reachable without
	// opening all private ranges.
	AllowedCIDRs []netip.Prefix
}

// metadataPrefixes are cloud metadata and host-agent endpoints. Link-local
// ranges are included as a whole: several clouds serve metadata or agents
// there (169.254.0.23, 169.254.169.123, ...), and no supported upstream lives
// on a link-local address.
var metadataPrefixes = []netip.Prefix{
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("100.100.100.200/32"), // Alibaba
	netip.MustParsePrefix("168.63.129.16/32"),   // Azure WireServer
	netip.MustParsePrefix("192.0.0.192/32"),     // Oracle Cloud
	netip.MustParsePrefix("fd00:ec2::254/128"),  // AWS IPv6
}

// IPv6 ranges that embed an IPv4 address the packet may reach.
var (
	nat64       = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local  = netip.MustParsePrefix("64:ff9b:1::/48")
	sixToFour   = netip.MustParsePrefix("2002::/16")
	v4Compat    = netip.MustParsePrefix("::/96")
	teredo      = netip.MustParsePrefix("2001::/32")
	errEmbedded = "embedded IPv4 address: "
)

// embeddedV4 returns the IPv4 address carried inside translation and
// tunnelling IPv6 forms, so the policy applies to it as well.
func embeddedV4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() || ip.Is4In6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case nat64.Contains(ip), v4Compat.Contains(ip) && !ip.IsLoopback() && !ip.IsUnspecified():
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case nat64Local.Contains(ip):
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case sixToFour.Contains(ip):
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	case teredo.Contains(ip):
		// The client address is stored inverted in the last 32 bits.
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	}
	return netip.Addr{}, false
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
	if v4, ok := embeddedV4(ip); ok {
		if err := p.CheckAddr(v4); err != nil {
			return fmt.Errorf("%w (%s%s)", err, errEmbedded, v4)
		}
	}
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
	private := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
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

// Dialer returns a net.Dialer that checks every resolved address against the
// policy just before connecting (covering DNS rebinding and each address a
// multi-address dial tries). Non-HTTP clients such as NATS use it directly.
func Dialer(p Policy, timeout time.Duration) *net.Dialer {
	return &net.Dialer{
		Timeout:   timeout,
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
}

// Transport returns an http.Transport whose dialer enforces the policy on
// every resolved address. Environment proxies are disabled so the check
// cannot be bypassed.
func Transport(p Policy, o TransportOptions) *http.Transport {
	d := Dialer(p, o.DialTimeout)
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
