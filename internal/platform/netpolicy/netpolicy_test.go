package netpolicy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestCheckAddr(t *testing.T) {
	allowOne, err := ParseCIDRs([]string{"10.1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		policy  Policy
		addr    string
		blocked bool
	}{
		{name: "public v4", addr: "93.184.216.34"},
		{name: "public v6", addr: "2606:2800:220:1::1"},
		{name: "loopback blocked by default", addr: "127.0.0.1", blocked: true},
		{name: "rfc1918 blocked by default", addr: "10.0.0.5", blocked: true},
		{name: "cgnat blocked by default", addr: "100.64.1.1", blocked: true},
		{name: "metadata blocked", addr: "169.254.169.254", blocked: true},
		{name: "metadata blocked even when private allowed", policy: Policy{AllowPrivate: true}, addr: "169.254.169.254", blocked: true},
		{name: "ipv6 metadata blocked", policy: Policy{AllowPrivate: true}, addr: "fd00:ec2::254", blocked: true},
		{name: "mapped v4 loopback blocked", addr: "::ffff:127.0.0.1", blocked: true},
		{name: "reserved always blocked", policy: Policy{AllowPrivate: true}, addr: "0.0.0.1", blocked: true},
		{name: "private allowed", policy: Policy{AllowPrivate: true}, addr: "192.168.1.10"},
		{name: "explicit cidr allowed", policy: Policy{AllowedCIDRs: allowOne}, addr: "10.1.2.3"},
		{name: "explicit cidr exact only", policy: Policy{AllowedCIDRs: allowOne}, addr: "10.1.2.4", blocked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.policy.CheckAddr(netip.MustParseAddr(tt.addr))
			if got := errors.Is(err, ErrDestinationBlocked); got != tt.blocked {
				t.Fatalf("blocked=%v want %v (err=%v)", got, tt.blocked, err)
			}
		})
	}
}

func TestTransportBlocksLoopbackAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	defer srv.Close()

	blocked := &http.Client{Transport: Transport(Policy{}, DefaultTransportOptions())}
	if _, err := blocked.Get(srv.URL); !errors.Is(err, ErrDestinationBlocked) {
		t.Fatalf("expected blocked dial, got %v", err)
	}
	allowed := &http.Client{Transport: Transport(Policy{AllowPrivate: true}, DefaultTransportOptions())}
	resp, err := allowed.Get(srv.URL)
	if err != nil {
		t.Fatalf("allowed dial failed: %v", err)
	}
	resp.Body.Close()
}

func TestParseCIDRsRejectsGarbage(t *testing.T) {
	if _, err := ParseCIDRs([]string{"not-an-ip"}); err == nil {
		t.Fatal("expected error")
	}
}
