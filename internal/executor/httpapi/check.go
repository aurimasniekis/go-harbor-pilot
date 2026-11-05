package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/executor"
	"github.com/aurimasniekis/go-harbor-pilot/internal/ir"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/credentials"
	"github.com/aurimasniekis/go-harbor-pilot/internal/platform/netpolicy"
	"github.com/aurimasniekis/go-harbor-pilot/internal/protocol"
)

// Check outcomes.
const (
	CheckOK          = "ok"          // the provider accepted the credential
	CheckRejected    = "rejected"    // the provider refused the credential (401/403)
	CheckUnreachable = "unreachable" // no answer, blocked address or TLS failure
	CheckError       = "error"       // any other unexpected answer
)

// CheckResult is the outcome of a credential check.
type CheckResult struct {
	Status     string
	Message    string
	HTTPStatus int
	Latency    time.Duration
}

// checkURL is a free, read-only endpoint that needs the credential: the
// provider's model list.
func checkURL(t executor.Target) (string, ir.Protocol) {
	base := strings.TrimRight(t.Provider.Spec.BaseURL, "/")
	switch t.Provider.Spec.Type {
	case controlplane.ProviderAnthropic:
		return base + "/v1/models", ir.AnthropicMessages
	case controlplane.ProviderGemini:
		return base + "/v1beta/models", ir.Gemini
	case controlplane.ProviderOllama:
		return base + "/api/tags", ir.OpenAIChat
	default: // openai, openai_compatible: the base already ends in /v1
		return base + "/models", ir.OpenAIChat
	}
}

// Check lists the provider's models with the account's credential, which
// proves the address is reachable and the credential is accepted without
// spending tokens. Messages never contain the credential.
func (e *Executor) Check(ctx context.Context, t executor.Target, cred credentials.Secret) CheckResult {
	u, up := checkURL(t)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return CheckResult{Status: CheckError, Message: "the provider address is not a valid URL"}
	}
	setHeaders(req, t, up, cred, false, e.UserAgent, "")
	req.Header.Del("Content-Type")
	start := time.Now()
	resp, err := e.client(t.Provider.Spec.AllowPrivateNetwork).Do(req) //nolint:gosec // address comes from operator configuration and passes the network policy
	lat := time.Since(start)
	if err != nil {
		msg := "could not reach the provider"
		if errors.Is(err, netpolicy.ErrDestinationBlocked) {
			msg = "the provider address is a private or blocked network address; allow a local network address on the provider if this is intended"
		} else if errors.Is(err, context.DeadlineExceeded) {
			msg = "the provider did not answer within 15 seconds"
		}
		return CheckResult{Status: CheckUnreachable, Message: msg, Latency: lat}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	r := CheckResult{HTTPStatus: resp.StatusCode, Latency: lat}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		r.Status, r.Message = CheckOK, "the provider accepted the credential"
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		r.Status, r.Message = CheckRejected, "the provider rejected the credential"
		if pe := protocolErrorText(up, resp.StatusCode, body, resp.Header); pe != "" {
			r.Message += ": " + pe
		}
	case resp.StatusCode == http.StatusNotFound:
		// The address answers but has no model list (some compatible
		// servers); the credential was not refused.
		r.Status, r.Message = CheckOK, "the provider answered; it has no model list to confirm the credential with"
	default:
		r.Status, r.Message = CheckError, "the provider answered with HTTP "+http.StatusText(resp.StatusCode)
		if pe := protocolErrorText(up, resp.StatusCode, body, resp.Header); pe != "" {
			r.Message += ": " + pe
		}
	}
	r.Message = executor.ScrubText(r.Message, cred)
	return r
}

func protocolErrorText(up ir.Protocol, status int, body []byte, h http.Header) string {
	if len(body) == 0 {
		return ""
	}
	e := protocol.ParseError(up, status, body, h)
	if e == nil {
		return ""
	}
	msg := strings.TrimSpace(e.Message)
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	return msg
}
