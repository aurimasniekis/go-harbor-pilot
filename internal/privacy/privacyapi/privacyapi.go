// Package privacyapi exposes the non-persisting rule-testing sandbox.
package privacyapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/aurimasniekis/go-harbor-pilot/internal/api"
	"github.com/aurimasniekis/go-harbor-pilot/internal/auth"
	cp "github.com/aurimasniekis/go-harbor-pilot/internal/controlplane"
	"github.com/aurimasniekis/go-harbor-pilot/internal/privacy"
)

// SandboxResult is what a policy would do with a sample text.
type SandboxResult struct {
	Findings []privacy.Finding `json:"findings" doc:"Detections with byte offsets into the sample (values are not echoed)."`
	Masked   string            `json:"masked" doc:"The sample as an upstream provider would receive it."`
	Summary  *privacy.Summary  `json:"summary"`
}

// Register mounts the sandbox.
func Register(a *api.API, svc func() *privacy.Service, snap func() *cp.Snapshot) {
	type in struct {
		Body struct {
			PolicyID string                `json:"policy_id,omitempty" doc:"Test a saved policy (including its configured secret values)."`
			Policy   *cp.PrivacyPolicySpec `json:"policy,omitempty" doc:"Or test an unsaved policy definition."`
			Text     string                `json:"text" maxLength:"262144"`
		}
	}
	type out struct{ Body SandboxResult }
	api.Register(a, api.Op{ID: "test-privacy-policy", Method: http.MethodPost, Path: "/privacy/sandbox", Summary: "Test masking rules on sample text",
		Description: "Nothing is stored or logged. Use synthetic samples where possible.", Tags: []string{"Privacy"},
		Permission: auth.PermConfigRead, MaxBodyBytes: 300 << 10}, func(ctx context.Context, i *in) (*out, error) {
		var pol *privacy.Policy
		switch {
		case i.Body.PolicyID != "":
			p, err := svc().Policy(ctx, snap(), i.Body.PolicyID)
			if (err != nil && !errors.Is(err, privacy.ErrSecretValuesUnavailable)) || p == nil {
				return nil, api.NotFound("privacy policy")
			}
			// Matching against stored secret values would let a reader test
			// guesses for them, so that needs the right to set them.
			if p.HasConfiguredSecrets() && !auth.FromContext(ctx).Can(auth.PermSecretsWrite) {
				return nil, api.Forbidden("testing a policy with configured secret values requires secrets.write; test an unsaved definition instead")
			}
			pol = p
		case i.Body.Policy != nil:
			spec := *i.Body.Policy
			v := cp.NewValidator("", nil)
			raw, _ := cp.PrivacyPolicyDef.Canonicalize(mustJSON(spec), v)
			if !v.OK() {
				return nil, api.Invalid("invalid policy", toFields(v.Errors)...)
			}
			spec, _ = cp.Decode[cp.PrivacyPolicySpec](raw)
			p, err := privacy.Compile("", spec, nil)
			if err != nil {
				return nil, api.Invalid(err.Error())
			}
			pol = p
		default:
			return nil, api.Invalid("policy_id or policy is required")
		}
		f, masked, sum := pol.Test(i.Body.Text)
		if f == nil {
			f = []privacy.Finding{}
		}
		return &out{Body: SandboxResult{Findings: f, Masked: masked, Summary: sum}}, nil
	})
}
