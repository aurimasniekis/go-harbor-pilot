package relay

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
)

// syntheticCreds builds a valid .creds file signed by a throwaway account
// key; no server trusts that account, and none is ever reached here.
func syntheticCreds(t *testing.T) []byte {
	t.Helper()
	acct, err := nkeys.CreateAccount()
	if err != nil {
		t.Fatal(err)
	}
	user, err := nkeys.CreateUser()
	if err != nil {
		t.Fatal(err)
	}
	upub, _ := user.PublicKey()
	uc := jwt.NewUserClaims(upub)
	uc.Tags.Add("pulse-client:synthetic") // the client id tag a Pulse server would set
	token, err := uc.Encode(acct)
	if err != nil {
		t.Fatal(err)
	}
	seed, _ := user.Seed()
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		t.Fatal(err)
	}
	return creds
}

// Pulse connections obey the outbound network policy, and connection
// failures do not reveal how the dial failed (refused vs timed out).
func TestConnectPulseUsesNetworkPolicy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	fakeCreds := syntheticCreds(t)
	_ = ln.Close() // a closed loopback port: "connection refused" if dialled

	_, err = ConnectPulse("nats://"+addr, fakeCreds, nil, "test", false)
	if err == nil {
		t.Fatal("loopback Pulse server accepted without allow_private_network")
	}
	res := pulseConnectError(err)
	if res.Retryable || !strings.Contains(res.Err, "blocked") {
		t.Fatalf("policy refusal reported as %+v (err %v)", res, err)
	}

	_, err = ConnectPulse("nats://"+addr, fakeCreds, nil, "test", true)
	if err == nil || errors.Is(err, netpolicy.ErrDestinationBlocked) {
		t.Fatalf("allowed loopback dial: %v", err)
	}
	res = pulseConnectError(err)
	if res.Err != "could not connect to Pulse" || !res.Retryable {
		t.Fatalf("dial failure leaks detail: %+v", res)
	}
}
