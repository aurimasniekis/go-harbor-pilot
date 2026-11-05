package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/quota/domain"
)

// DefaultMaxBody bounds provider response bodies.
const DefaultMaxBody = 4 << 20

// HTTPOptions configures an outbound client.
type HTTPOptions struct {
	Timeout   time.Duration
	MaxBody   int64
	UserAgent string
	// Transport overrides the transport for every request (tests, or an
	// adapter-specific guarded transport). When nil, requests go through the
	// outbound network policy: public destinations only, or also private
	// ones for contexts marked with WithPrivateNetwork.
	Transport http.RoundTripper
	// MaxRedirects; 0 disables redirects entirely.
	MaxRedirects int
	// AllowPrivate lets every request reach private and loopback endpoints
	// (adapter tests against local fakes). Production clients leave it off
	// and opt in per monitor with WithPrivateNetwork.
	AllowPrivate bool
}

// HTTPClient is a hardened JSON HTTP client for adapters.
type HTTPClient struct {
	c         *http.Client
	private   *http.Client // nil when a fixed transport was supplied
	maxBody   int64
	userAgent string
}

// ErrDestinationBlocked is the outbound policy's refusal.
var ErrDestinationBlocked = netpolicy.ErrDestinationBlocked

type privateNetKey struct{}

// WithPrivateNetwork marks a context whose requests may reach private and
// loopback destinations (a monitor's allow_private_network). Metadata and
// link-local addresses stay blocked.
func WithPrivateNetwork(ctx context.Context, allow bool) context.Context {
	return context.WithValue(ctx, privateNetKey{}, allow)
}

func allowsPrivate(ctx context.Context) bool {
	v, _ := ctx.Value(privateNetKey{}).(bool)
	return v
}

// NewHTTPClient builds a client. Redirects are only followed to the same
// origin, so credentials are never forwarded to another host.
func NewHTTPClient(o HTTPOptions) *HTTPClient {
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.MaxBody == 0 {
		o.MaxBody = DefaultMaxBody
	}
	if o.UserAgent == "" {
		o.UserAgent = "harbor-pilot"
	}
	maxRedirects := o.MaxRedirects
	mk := func(tr http.RoundTripper) *http.Client {
		return &http.Client{
			Timeout:   o.Timeout,
			Transport: tr,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) > maxRedirects {
					return errRedirectRefused
				}
				if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
					return errRedirectRefused
				}
				return nil
			},
		}
	}
	hc := &HTTPClient{maxBody: o.MaxBody, userAgent: o.UserAgent}
	if o.Transport != nil {
		hc.c = mk(o.Transport)
		return hc
	}
	// Environment proxies are disabled by the policy transport, so the
	// destination check cannot be bypassed.
	to := netpolicy.TransportOptions{DialTimeout: 10 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second, MaxIdleConnsPerHost: 8}
	hc.c = mk(netpolicy.Transport(netpolicy.Policy{AllowPrivate: o.AllowPrivate}, to))
	hc.private = mk(netpolicy.Transport(netpolicy.Policy{AllowPrivate: true}, to))
	return hc
}

var errRedirectRefused = errors.New("redirect refused")

// Request is an outbound request description.
type Request struct {
	Method string
	URL    string
	Header http.Header
	Body   any // JSON-encoded when non-nil
	// Form is sent as application/x-www-form-urlencoded when non-nil.
	Form url.Values
	// NotFoundIsUnsupported maps HTTP 404 to ErrUnsupported (feature not
	// enabled for this account) instead of an invalid response.
	NotFoundIsUnsupported bool
}

// Response carries status and headers of a successful call.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Do performs the request and returns the bounded body for 2xx responses.
// Non-2xx statuses become typed *Error values whose messages never include
// the response body or the request URL query (which may contain keys).
func (c *HTTPClient) Do(ctx context.Context, r Request) (*Response, error) {
	var body io.Reader
	if r.Form != nil {
		body = strings.NewReader(r.Form.Encode())
	} else if r.Body != nil {
		b, err := json.Marshal(r.Body)
		if err != nil {
			return nil, NewError(domain.ErrConfiguration, "cannot encode request body")
		}
		body = bytes.NewReader(b)
	}
	method := r.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL, body)
	if err != nil {
		return nil, NewError(domain.ErrConfiguration, "invalid request URL")
	}
	for k, vs := range r.Header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if r.Form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.c
	if c.private != nil && allowsPrivate(ctx) {
		client = c.private
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, classifyTransportError(ctx, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBody+1))
	if err != nil {
		return nil, WrapError(domain.ErrNetwork, "failed reading response body", err)
	}
	if int64(len(data)) > c.maxBody {
		return nil, NewError(domain.ErrInvalidResponse, "response body exceeds size limit")
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
	}
	return nil, statusError(resp, r.NotFoundIsUnsupported)
}

// DoJSON performs the request and decodes a JSON body into out.
func (c *HTTPClient) DoJSON(ctx context.Context, r Request, out any) (*Response, error) {
	resp, err := c.Do(ctx, r)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := json.Unmarshal(resp.Body, out); err != nil {
			return nil, NewError(domain.ErrInvalidResponse, "response is not valid JSON for the expected schema")
		}
	}
	return resp, nil
}

func statusError(resp *http.Response, notFoundUnsupported bool) *Error {
	e := &Error{HTTPStatus: resp.StatusCode}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		e.Kind, e.Message = domain.ErrAuthentication, "provider rejected the credentials"
	case resp.StatusCode == http.StatusForbidden:
		e.Kind, e.Message = domain.ErrPermission, "credentials lack permission for this endpoint"
	case resp.StatusCode == http.StatusNotFound && notFoundUnsupported:
		e.Kind, e.Message = domain.ErrUnsupported, "endpoint not available for this account"
	case resp.StatusCode == http.StatusTooManyRequests:
		e.Kind, e.Message = domain.ErrRateLimited, "provider rate limit reached"
		e.RetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	case resp.StatusCode == http.StatusPaymentRequired:
		e.Kind, e.Message = domain.ErrPermission, "provider reports payment required or credits exhausted"
	case resp.StatusCode >= 500:
		e.Kind, e.Message = domain.ErrUnavailable, "provider temporarily unavailable"
		e.RetryAfter = ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	default:
		e.Kind, e.Message = domain.ErrInvalidResponse, "unexpected provider response status"
	}
	return e
}

func classifyTransportError(ctx context.Context, err error) *Error {
	if errors.Is(err, errRedirectRefused) {
		return WrapError(domain.ErrConfiguration, "provider redirected to another origin; refused", err)
	}
	if errors.Is(err, ErrDestinationBlocked) {
		return WrapError(domain.ErrConfiguration, "destination address is blocked by the outbound network policy (private and local endpoints need allow_private_network on the monitor)", err)
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return WrapError(domain.ErrInternal, "request cancelled", ctx.Err())
		}
		return WrapError(domain.ErrNetwork, "request timed out", ctx.Err())
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return WrapError(domain.ErrNetwork, "request timed out", err)
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		// url.Error embeds the full URL which may contain query secrets; never
		// expose it.
		return WrapError(domain.ErrNetwork, "network error contacting provider", errors.Unwrap(ue))
	}
	return WrapError(domain.ErrNetwork, "network error contacting provider", err)
}

// ParseRetryAfter parses a Retry-After header (delta-seconds or HTTP-date).
// It returns 0 when absent or invalid, and caps the result at 24h.
func ParseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs < 0 {
			return 0
		}
		d = time.Duration(secs * float64(time.Second))
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	} else {
		return 0
	}
	if d < 0 {
		return 0
	}
	if d > 24*time.Hour {
		d = 24 * time.Hour
	}
	return d
}

// BearerHeader builds an Authorization header map.
func BearerHeader(token string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	return h
}

// JoinURL joins a base URL and path, failing on invalid base.
func JoinURL(base, path string) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return "", fmt.Errorf("invalid base URL")
	}
	return u.String(), nil
}
