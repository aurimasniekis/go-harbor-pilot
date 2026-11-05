package relay

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aurimasniekis/go-pulse/pkg/pulse"
	"github.com/nats-io/nats.go"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
)

type pulsePublisher struct{ c *pulse.Client }

func (p pulsePublisher) Publish(ctx context.Context, subject, typ string, payload []byte, id string, at time.Time) (bool, error) {
	ack, err := p.c.Publish(ctx, subject, typ, json.RawMessage(payload), pulse.WithID(id), pulse.WithTime(at), pulse.WithContentType("application/json"))
	if err != nil {
		return false, err
	}
	return ack.Duplicate, nil
}

func (p pulsePublisher) Close() { p.c.Close() }

// ConnectPulse opens a Pulse client with .creds authentication. The
// connection goes through the outbound network policy: private and loopback
// servers need allowPrivate, metadata addresses are always refused.
func ConnectPulse(url string, creds, ca []byte, source string, allowPrivate bool) (Publisher, error) {
	d := netpolicy.Dialer(netpolicy.Policy{AllowPrivate: allowPrivate}, 10*time.Second)
	opts := []pulse.Option{pulse.WithCreds(creds), pulse.WithSource(source), pulse.WithName(source),
		pulse.WithNATSOptions(nats.SetCustomDialer(d))}
	if len(ca) > 0 {
		opts = append(opts, pulse.WithCA(ca))
	}
	c, err := pulse.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	return pulsePublisher{c: c}, nil
}

// WrapPulse adapts an existing client (tests use an embedded server).
func WrapPulse(c *pulse.Client) Publisher { return pulsePublisher{c: c} }
