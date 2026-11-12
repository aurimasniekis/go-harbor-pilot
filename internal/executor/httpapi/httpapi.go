// Package httpapi executes attempts against HTTP model APIs: Anthropic,
// OpenAI, Gemini, OpenAI-compatible servers and local OpenAI-compatible
// endpoints (Ollama, vLLM, llama.cpp, LM Studio).
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/anthropic"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/gemini"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openaichat"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/openairesponses"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol/wire"
)

// Limits bound upstream responses.
type Limits struct {
	MaxResponseBytes int64         // non-streaming body cap
	MaxEventBytes    int           // single SSE event cap
	DefaultTimeout   time.Duration // overall request timeout
	DefaultIdle      time.Duration // stream idle timeout
}

// DefaultLimits are generous but bounded.
var DefaultLimits = Limits{MaxResponseBytes: 64 << 20, MaxEventBytes: 16 << 20, DefaultTimeout: 10 * time.Minute, DefaultIdle: 5 * time.Minute}

// Executor implements executor.Executor for HTTP providers.
type Executor struct {
	Limits    Limits
	UserAgent string
	mu        sync.Mutex
	clients   map[string]*http.Client
	// AllowedPrivate lets tests and explicitly configured local endpoints
	// reach private networks (in addition to per-provider authorization).
	AllowedPrivate []netip.Prefix
	// Sample receives a bounded raw copy of one in SampleEvery healthy
	// streams (Protocol Watch baseline learning). It must not block.
	Sample      func(p ir.Protocol, providerID string, raw []byte)
	SampleEvery int64
	sampled     atomic.Int64
}

// limitedBuffer keeps at most max bytes.
type limitedBuffer struct {
	b   []byte
	max int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - len(l.b); room > 0 {
		l.b = append(l.b, p[:min(len(p), room)]...)
	}
	return len(p), nil
}

// New creates an executor.
func New(userAgent string) *Executor {
	return &Executor{Limits: DefaultLimits, UserAgent: userAgent, clients: map[string]*http.Client{}}
}

func (e *Executor) client(allowPrivate bool) *http.Client {
	key := fmt.Sprint(allowPrivate)
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.clients[key]; ok {
		return c
	}
	opts := netpolicy.DefaultTransportOptions()
	opts.ResponseHeaderTimeout = 0 // model latency is unbounded; the request context bounds it
	opts.MaxIdleConnsPerHost = 32
	c := &http.Client{Transport: netpolicy.Transport(netpolicy.Policy{AllowPrivate: allowPrivate, AllowedCIDRs: e.AllowedPrivate}, opts),
		CheckRedirect: netpolicy.NoRedirects}
	e.clients[key] = c
	return c
}

// UpstreamProtocol picks the wire protocol to use with a provider.
func UpstreamProtocol(providerType string, inbound ir.Protocol) ir.Protocol {
	switch providerType {
	case controlplane.ProviderAnthropic:
		return ir.AnthropicMessages
	case controlplane.ProviderGemini:
		return ir.Gemini
	case controlplane.ProviderOpenAI:
		if inbound == ir.OpenAIChat {
			return ir.OpenAIChat
		}
		return ir.OpenAIResponses
	case controlplane.ProviderOpenAICompatible, controlplane.ProviderOllama:
		return ir.OpenAIChat
	}
	return ""
}

func endpoint(t executor.Target, p ir.Protocol, stream bool) (string, error) {
	base := strings.TrimRight(t.Provider.Spec.BaseURL, "/")
	switch p {
	case ir.AnthropicMessages:
		return base + "/v1/messages", nil
	case ir.OpenAIResponses:
		return base + "/responses", nil
	case ir.OpenAIChat:
		if t.Provider.Spec.Type == controlplane.ProviderOllama {
			return base + "/v1/chat/completions", nil
		}
		return base + "/chat/completions", nil
	case ir.Gemini:
		method := "generateContent"
		suffix := ""
		if stream {
			method, suffix = "streamGenerateContent", "?alt=sse"
		}
		return base + "/v1beta/models/" + url.PathEscape(t.Model) + ":" + method + suffix, nil
	}
	return "", fmt.Errorf("no endpoint for protocol %s", p)
}

// Execute runs one attempt.
func (e *Executor) Execute(ctx context.Context, t executor.Target, cred credentials.Secret, req executor.Request, stream bool) (*executor.Result, *executor.AttemptError) {
	up := UpstreamProtocol(t.Provider.Spec.Type, req.Inbound)
	if up == "" {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUnsupported, Message: "provider type " + t.Provider.Spec.Type + " is not an HTTP provider"}}
	}
	mode := executor.ModeTranslated
	if up == req.Inbound {
		mode = executor.ModeNative
	}
	var body []byte
	var losses []ir.Loss
	var err error
	var ferr error
	var fields ir.Losses
	if req.Passthrough {
		if up != req.Inbound {
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUnsupported, Message: "native passthrough requires an upstream speaking " + string(req.Inbound)}}
		}
		mode = executor.ModePassthrough
		body, ferr, err = passthroughBody(req.RawBody, t, up, stream, req.IR, &fields)
	} else {
		body, losses, err = build(req.IR, t, up, stream)
		if err == nil {
			body, ferr = applyRequestFields(body, t.Provider.Spec.Type, t.ModelSpec, ClientSettingsOf(req.IR, req.RawBody), &fields)
		}
	}
	if err != nil {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInternal, Message: "could not build upstream request"}}
	}
	if ferr != nil {
		return nil, &executor.AttemptError{Err: requestFieldError(ferr)}
	}
	losses = append(losses, fields.List()...)
	if ir.HasReject(losses) && !req.AllowLosses {
		var feats []string
		for _, l := range losses {
			if l.Severity == ir.LossReject {
				feats = append(feats, l.Feature)
			}
		}
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUnsupported, Message: "the request needs features this upstream cannot provide: " + strings.Join(feats, ", ")}}
	}
	u, err := endpoint(t, up, stream)
	if err != nil {
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInternal, Message: err.Error()}}
	}
	timeout := e.Limits.DefaultTimeout
	if s := t.Provider.Spec.RequestTimeoutSecs; s > 0 {
		timeout = time.Duration(s) * time.Second
	}
	actx, cancel := context.WithTimeout(ctx, timeout)
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrInternal, Message: "invalid upstream URL"}}
	}
	setHeaders(hreq, t, up, cred, stream, e.UserAgent, req.RequestID)
	if up == req.Inbound && req.ClientHeader != nil {
		ClientHeaders(hreq.Header, req.ClientHeader, up)
	}
	resp, err := e.client(t.Provider.Spec.AllowPrivateNetwork).Do(hreq) //nolint:bodyclose // closed on every path, including the stream goroutines
	if err != nil {
		cancel()
		return nil, transportError(ctx, err)
	}
	firstByte := time.Now()
	if resp.StatusCode >= 400 {
		defer cancel()
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		ae := protocol.ParseError(up, resp.StatusCode, b, resp.Header)
		return nil, &executor.AttemptError{Err: ae, Status: resp.StatusCode, Header: resp.Header.Clone(),
			AccountScoped: ae.Kind == ir.ErrAuthentication || ae.Kind == ir.ErrPermission || ae.Kind == ir.ErrRateLimited || ae.Kind == ir.ErrQuotaExhausted}
	}
	res := &executor.Result{Upstream: up, Mode: mode, Losses: losses, Status: resp.StatusCode, Header: resp.Header.Clone(), FirstByteAt: firstByte, Cancel: cancel}
	idle := e.Limits.DefaultIdle
	if s := t.Provider.Spec.IdleTimeoutSecs; s > 0 {
		idle = time.Duration(s) * time.Second
	}
	if !stream {
		defer cancel()
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, e.Limits.MaxResponseBytes+1))
		if err != nil {
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "upstream response was interrupted"}, Executed: true, Status: resp.StatusCode}
		}
		if int64(len(b)) > e.Limits.MaxResponseBytes {
			return nil, &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "upstream response exceeds the size limit"}, Executed: true, Status: resp.StatusCode}
		}
		if mode == executor.ModePassthrough {
			raw := make(chan []byte, 1)
			raw <- b
			close(raw)
			res.RawStream = raw
			return res, nil
		}
		r, perr := protocol.ParseResponse(up, b)
		if perr != nil {
			var ie *ir.Error
			if !errors.As(perr, &ie) {
				ie = &ir.Error{Kind: ir.ErrProtocol, Message: perr.Error()}
			}
			return nil, &executor.AttemptError{Err: ie, Executed: true, Status: resp.StatusCode}
		}
		res.Response = r
		return res, nil
	}
	body2 := newIdleReader(resp.Body, idle, cancel)
	if mode == executor.ModePassthrough {
		raw := make(chan []byte, 16)
		var rerr error
		go func() {
			defer close(raw)
			defer resp.Body.Close()
			buf := make([]byte, 32<<10)
			for {
				n, err := body2.Read(buf)
				if n > 0 {
					raw <- append([]byte(nil), buf[:n]...)
				}
				if err != nil {
					if !errors.Is(err, io.EOF) {
						rerr = err
					}
					return
				}
			}
		}()
		res.RawStream = raw
		res.Err = func() error { return rerr }
		return res, nil
	}
	events := make(chan ir.Event, 64)
	var serr error
	var unknownMu sync.Mutex
	var unknown []string
	var sample *limitedBuffer
	var src io.Reader = body2
	if every := e.SampleEvery; e.Sample != nil && every > 0 && e.sampled.Add(1)%every == 1 {
		sample = &limitedBuffer{max: 64 << 10}
		src = io.TeeReader(body2, sample)
	}
	go func() {
		defer close(events)
		defer resp.Body.Close()
		defer func() {
			if sample != nil && serr == nil && len(sample.b) > 0 {
				e.Sample(up, t.Provider.ID, sample.b)
			}
		}()
		rd := wire.NewSSEReader(src, e.Limits.MaxEventBytes)
		dec := protocol.NewDecoder(up)
		for {
			ev, err := rd.Next()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					serr = streamErr(actx, err)
				} else if !dec.Finished() {
					for _, out := range dec.Close() {
						events <- out
					}
					if !dec.Finished() {
						serr = &ir.Error{Kind: ir.ErrUpstream, Message: "upstream stream ended before completion"}
					}
				}
				return
			}
			out, unk, err := dec.Feed(ev)
			if unk != "" {
				unknownMu.Lock()
				if len(unknown) < 100 {
					unknown = append(unknown, unk)
				}
				unknownMu.Unlock()
			}
			if err != nil {
				serr = err
				return
			}
			for _, o := range out {
				select {
				case events <- o:
				case <-actx.Done():
					serr = streamErr(actx, actx.Err())
					return
				}
			}
		}
	}()
	res.Stream = events
	res.Err = func() error { return serr }
	res.Unknown = func() []string {
		unknownMu.Lock()
		defer unknownMu.Unlock()
		return append([]string(nil), unknown...)
	}
	return res, nil
}

func build(r *ir.Request, t executor.Target, up ir.Protocol, stream bool) ([]byte, []ir.Loss, error) {
	switch up {
	case ir.AnthropicMessages:
		def := 0
		if t.ModelSpec != nil {
			def = t.ModelSpec.MaxOutputTokens
		}
		return anthropic.BuildRequest(r, anthropic.BuildOptions{Model: t.Model, Stream: stream, DefaultMaxTokens: def})
	case ir.OpenAIChat:
		o := openaichat.BuildOptions{Model: t.Model, Stream: stream}
		if t.Provider.Spec.Type != controlplane.ProviderOpenAI {
			o.LegacyMaxTokens = true
		}
		return openaichat.BuildRequest(r, o)
	case ir.OpenAIResponses:
		return openairesponses.BuildRequest(r, openairesponses.BuildOptions{Model: t.Model, Stream: stream, Stateless: true})
	case ir.Gemini:
		return gemini.BuildRequest(r)
	}
	return nil, nil, fmt.Errorf("unsupported protocol %s", up)
}

// passthroughBody forwards the client body, changing only the model field
// (and the stream flag the gateway relies on) for protocols that carry them.
// A model's configured request fields are applied here too, because the
// client's bytes are what would otherwise bypass them.
// passthroughBody returns the client's body with the model rewritten and the
// model's request fields applied. ferr is a failure to apply those fields;
// err is anything else (a body that does not parse), so an unconfigured model
// never reports a field problem.
func passthroughBody(raw []byte, t executor.Target, p ir.Protocol, stream bool, r *ir.Request, fields *ir.Losses) (body []byte, ferr, err error) {
	fs, ok := configuredFields(t.Provider.Spec.Type, t.ModelSpec)
	var cs ClientSettings
	if ok {
		cs = ClientSettingsOf(r, raw)
	}
	if p == ir.Gemini {
		if !ok {
			return raw, nil, nil
		}
		// Gemini carries the model in the path, so its body is otherwise
		// untouched; the configured fields still apply.
		o, err := wire.ParseObject(raw)
		if err != nil {
			return nil, nil, err
		}
		if err := mergeFields(o, fs, cs, fields); err != nil {
			return nil, err, nil
		}
		body, err := wire.Marshal(o)
		return body, nil, err
	}
	o, err := wire.ParseObject(raw)
	if err != nil {
		return nil, nil, err
	}
	o["model"] = wire.MustMarshal(t.Model)
	if p == ir.OpenAIChat && stream {
		o["stream_options"] = json.RawMessage(`{"include_usage":true}`)
	}
	if ok {
		if err := mergeFields(o, fs, cs, fields); err != nil {
			return nil, err, nil
		}
	}
	body, err = wire.Marshal(o)
	return body, nil, err
}

func setHeaders(r *http.Request, t executor.Target, up ir.Protocol, cred credentials.Secret, stream bool, ua, reqID string) {
	h := r.Header
	h.Set("Content-Type", "application/json")
	if stream {
		h.Set("Accept", "text/event-stream")
	} else {
		h.Set("Accept", "application/json")
	}
	if ua != "" {
		h.Set("User-Agent", ua)
	}
	for k, v := range t.Provider.Spec.Headers {
		h.Set(k, v)
	}
	switch up {
	case ir.AnthropicMessages:
		if h.Get("anthropic-version") == "" {
			h.Set("anthropic-version", "2023-06-01")
		}
		if !cred.IsZero() {
			h.Set("x-api-key", cred.Reveal())
		}
	case ir.Gemini:
		if !cred.IsZero() {
			h.Set("x-goog-api-key", cred.Reveal())
		}
	default:
		if !cred.IsZero() {
			h.Set("Authorization", "Bearer "+cred.Reveal())
		}
	}
	if reqID != "" {
		h.Set("X-Request-ID", reqID)
	}
}

// ClientHeaders copies allowlisted client headers that alter upstream
// behaviour for native requests (beta feature flags). Credentials and
// identity headers are never forwarded.
func ClientHeaders(dst, src http.Header, up ir.Protocol) {
	if up == ir.AnthropicMessages {
		for _, k := range []string{"anthropic-beta", "anthropic-version"} {
			if v := src.Values(k); len(v) > 0 {
				dst[http.CanonicalHeaderKey(k)] = append([]string(nil), v...)
			}
		}
	}
	if up == ir.OpenAIResponses || up == ir.OpenAIChat {
		if v := src.Get("OpenAI-Beta"); v != "" {
			dst.Set("OpenAI-Beta", v)
		}
	}
}

func transportError(parent context.Context, err error) *executor.AttemptError {
	if parent.Err() != nil {
		return &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrCanceled, Message: "client canceled the request"}, Executed: true}
	}
	if errors.Is(err, netpolicy.ErrDestinationBlocked) {
		return &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrPermission, Message: "upstream destination blocked by the outbound network policy"}}
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "could not connect to the upstream"}}
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "upstream host could not be resolved"}}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrTimeout, Message: "upstream did not respond in time"}, Executed: true}
	}
	// The request may have reached the upstream (e.g. connection reset after
	// sending); treat as ambiguous.
	return &executor.AttemptError{Err: &ir.Error{Kind: ir.ErrUpstream, Message: "upstream connection failed"}, Executed: true}
}

func streamErr(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, errIdle):
		return &ir.Error{Kind: ir.ErrTimeout, Message: "upstream stream stalled"}
	case ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return &ir.Error{Kind: ir.ErrTimeout, Message: "upstream request timed out"}
	case ctx.Err() != nil:
		return &ir.Error{Kind: ir.ErrCanceled, Message: "request canceled"}
	case errors.Is(err, wire.ErrLineTooLong):
		return &ir.Error{Kind: ir.ErrProtocol, Message: "upstream stream event exceeds the size limit"}
	}
	return &ir.Error{Kind: ir.ErrUpstream, Message: "upstream stream was interrupted"}
}

var errIdle = errors.New("idle timeout")

// idleReader cancels the request when no bytes arrive for d.
type idleReader struct {
	r      io.Reader
	timer  *time.Timer
	d      time.Duration
	fired  chan struct{}
	cancel context.CancelFunc
}

func newIdleReader(r io.Reader, d time.Duration, cancel context.CancelFunc) *idleReader {
	ir := &idleReader{r: r, d: d, fired: make(chan struct{}), cancel: cancel}
	ir.timer = time.AfterFunc(d, func() {
		close(ir.fired)
		cancel()
	})
	return ir
}

func (r *idleReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.timer.Reset(r.d)
	}
	if err != nil {
		select {
		case <-r.fired:
			return n, errIdle
		default:
		}
		if !errors.Is(err, io.EOF) {
			return n, err
		}
		r.timer.Stop()
	}
	return n, err
}
