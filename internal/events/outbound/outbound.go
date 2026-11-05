// Package outbound sends signed HTTP deliveries for event webhooks and
// notification channels under the outbound network policy: no redirects,
// bounded response bodies, timeouts, and no private or metadata
// destinations unless explicitly allowed (metadata endpoints never).
package outbound

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
)

// Result classifies a delivery attempt.
type Result struct {
	OK         bool
	Retryable  bool
	RetryAfter time.Duration
	Status     int
	// Err is a sanitized description (never a secret or a response body).
	Err string
	// Body is a bounded copy of the response (for provider error parsing).
	Body []byte
}

// Client posts JSON with policy enforcement.
type Client struct {
	private *http.Client
	public  *http.Client
	// TestTransport replaces the transport (tests).
	TestTransport http.RoundTripper
}

// New creates a client.
func New() *Client {
	mk := func(allowPrivate bool) *http.Client {
		o := netpolicy.DefaultTransportOptions()
		o.ResponseHeaderTimeout = 30 * time.Second
		return &http.Client{Transport: netpolicy.Transport(netpolicy.Policy{AllowPrivate: allowPrivate}, o), CheckRedirect: netpolicy.NoRedirects}
	}
	return &Client{private: mk(true), public: mk(false)}
}

const maxResponse = 64 << 10

// Post sends body to url.
func (c *Client) Post(ctx context.Context, url string, body []byte, headers http.Header, allowPrivate bool, timeout time.Duration) Result {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{Err: "invalid destination URL"}
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "harbor-pilot-notifier")
	hc := c.public
	if allowPrivate {
		hc = c.private
	}
	if c.TestTransport != nil {
		hc = &http.Client{Transport: c.TestTransport, CheckRedirect: netpolicy.NoRedirects}
	}
	resp, err := hc.Do(req)
	if err != nil {
		msg := "connection failed"
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			msg = "timed out"
		case strings.Contains(err.Error(), "not allowed"):
			return Result{Err: "destination blocked by outbound network policy"}
		}
		return Result{Retryable: true, Err: msg}
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	r := Result{Status: resp.StatusCode, Body: b}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if s, err := strconv.ParseFloat(ra, 64); err == nil && s > 0 {
			r.RetryAfter = time.Duration(s * float64(time.Second))
		}
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		r.OK = true
	case resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500:
		r.Retryable = true
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		r.Err = fmt.Sprintf("HTTP %d (redirects are not followed)", resp.StatusCode)
	default:
		r.Err = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return r
}

// Sign returns the X-Harbor-Signature header value: t=<unix>,v1=<hex>
// for each secret (current first, previous during rotation). The MAC is
// HMAC-SHA256 over "<t>.<body>".
func Sign(body []byte, at time.Time, secrets ...string) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	parts := []string{"t=" + ts}
	for _, s := range secrets {
		if s == "" {
			continue
		}
		m := hmac.New(sha256.New, []byte(s))
		m.Write([]byte(ts + "."))
		m.Write(body)
		parts = append(parts, "v1="+hex.EncodeToString(m.Sum(nil)))
	}
	return strings.Join(parts, ",")
}

// Verify checks a signature header (receiver guidance and tests): the
// timestamp must be within tolerance and any v1 value must match.
func Verify(header string, body []byte, secret string, now time.Time, tolerance time.Duration) bool {
	var ts string
	var sigs []string
	for _, p := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sigs = append(sigs, v)
		}
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || now.Sub(time.Unix(sec, 0)).Abs() > tolerance {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "."))
	m.Write(body)
	want := hex.EncodeToString(m.Sum(nil))
	for _, s := range sigs {
		if hmac.Equal([]byte(s), []byte(want)) {
			return true
		}
	}
	return false
}

// JSON marshals v.
func JSON(v any) []byte { b, _ := json.Marshal(v); return b }
