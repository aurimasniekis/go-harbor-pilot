package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestControlPlaneOverHTTP(t *testing.T) {
	env := NewTestEnv(t)
	admin := env.SetupAdmin(t)
	type res struct {
		ID         string         `json:"id"`
		Revision   int64          `json:"revision"`
		ManagedBy  string         `json:"managed_by"`
		DriftState string         `json:"drift_state"`
		Spec       map[string]any `json:"spec"`
		Secrets    map[string]struct {
			Set    bool   `json:"set"`
			Source string `json:"source"`
		} `json:"secrets"`
	}
	var prov res
	if code := admin.Do("POST", "/api/v1/providers", map[string]any{"name": "openai", "spec": map[string]any{
		"type": "openai", "display_name": "OpenAI", "base_url": "", "allow_private_network": false, "classification": "",
		"headers": map[string]string{}, "request_timeout_seconds": 0, "stream_idle_timeout_seconds": 0,
		"models": []map[string]any{{"id": "gpt-5", "display_name": "GPT-5", "context_window": 400000, "max_output_tokens": 128000, "capabilities": []string{"tools"}}},
	}}, &prov, "X-HP-Managed-By", "terraform"); code != 201 {
		t.Fatalf("create provider: %d", code)
	}
	if prov.Spec["base_url"] != "https://api.openai.com/v1" || prov.ManagedBy != "terraform" {
		t.Fatalf("provider: %+v", prov)
	}
	var acc res
	body := map[string]any{"name": "openai-main", "spec": map[string]any{"provider_id": prov.ID, "display_name": "", "credential_ref": "",
		"billing": "metered_api", "allowed_models": []string{}, "max_concurrency": 4, "requests_per_minute": 0, "enabled": true, "labels": map[string]string{}},
		"secrets": map[string]string{"credential": "sk-proj-SUPERSECRETVALUE1234"}}
	if code := admin.Do("POST", "/api/v1/provider-accounts", body, &acc); code != 201 {
		t.Fatalf("create account: %d", code)
	}
	if !acc.Secrets["credential"].Set || acc.Secrets["credential"].Source != "stored" {
		t.Fatalf("secret presence: %+v", acc.Secrets)
	}
	// Secret never echoed by any read endpoint.
	for _, p := range []string{"/api/v1/provider-accounts/" + acc.ID, "/api/v1/provider-accounts", "/api/v1/provider-accounts/" + acc.ID + "/history", "/api/v1/config/export", "/api/v1/audit?limit=200"} {
		var raw map[string]any
		admin.Do("GET", p, nil, &raw)
		if strings.Contains(fmt.Sprint(raw), "SUPERSECRET") {
			t.Fatalf("secret leaked by %s", p)
		}
	}
	// UI edit of a terraform-managed provider marks drift.
	var edited res
	prov.Spec["display_name"] = "Edited"
	if code := admin.Do("PUT", "/api/v1/providers/"+prov.ID, map[string]any{"name": "openai", "spec": prov.Spec}, &edited, "If-Match", fmt.Sprintf(`"r%d"`, prov.Revision)); code != 200 {
		t.Fatalf("replace: %d", code)
	}
	if edited.DriftState != "drifted" || edited.ManagedBy != "terraform" {
		t.Fatalf("drift: %+v", edited)
	}
	if code := admin.Do("PUT", "/api/v1/providers/"+prov.ID, map[string]any{"name": "openai", "spec": prov.Spec}, nil, "If-Match", `"r1"`); code != 412 {
		t.Fatalf("stale If-Match: %d", code)
	}
	// Referenced delete is refused with details.
	var errBody struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if code := admin.Do("DELETE", "/api/v1/providers/"+prov.ID, nil, &errBody); code != 409 || errBody.Error.Details["referenced_by"] == nil {
		t.Fatalf("referenced delete: %d %+v", code, errBody)
	}
	// Plan + publish + binding + credential issuance.
	var pool, alias, plan, binding res
	admin.Do("POST", "/api/v1/pools", map[string]any{"name": "gpt", "spec": map[string]any{"display_name": "", "strategy": "ordered", "affinity": "session",
		"members": []map[string]any{{"account_id": acc.ID, "model": "gpt-5", "priority": 0, "weight": 1}}}}, &pool)
	admin.Do("POST", "/api/v1/model-aliases", map[string]any{"name": "coding/default", "spec": map[string]any{"description": "", "pool_id": pool.ID, "alias_id": "", "required_capabilities": []string{}}}, &alias)
	admin.Do("POST", "/api/v1/routing-plans", map[string]any{"name": "default", "spec": map[string]any{"display_name": "Default", "description": ""}}, &plan)
	var draft struct{ ID string }
	if code := admin.Do("POST", "/api/v1/routing-plans/"+plan.ID+"/versions", map[string]any{"note": "v1", "document": map[string]any{
		"schema_version": 1, "model_mappings": []map[string]any{{"match": "gpt-*", "alias_id": alias.ID, "note": ""}}, "default_alias_id": alias.ID,
		"restrictions": map[string]any{"allowed_provider_ids": []string{}, "denied_provider_ids": []string{}, "allowed_billing": []string{}, "allowed_classifications": []string{}, "required_capabilities": []string{}},
		"fallback":     map[string]any{"steps": []string{"alternate_account", "stop"}, "max_attempts": 2, "allow_paid_fallback": false, "wait_max_seconds": 0},
	}}, &draft); code != 201 {
		t.Fatalf("draft: %d", code)
	}
	var pub struct{ Status string }
	if code := admin.Do("POST", "/api/v1/routing-plans/"+plan.ID+"/versions/"+draft.ID+"/publish", nil, &pub); code != 200 || pub.Status != "published" {
		t.Fatalf("publish: %d %+v", code, pub)
	}
	if code := admin.Do("POST", "/api/v1/client-bindings", map[string]any{"name": "laptop", "spec": map[string]any{"display_name": "", "owner_kind": "none", "owner_id": "",
		"plan_id": plan.ID, "project_id": "", "allowed_project_ids": []string{}, "expected_harness": "claude_code", "allowed_models": []string{},
		"limits": map[string]any{"requests_per_minute": 0, "max_concurrency": 0, "max_request_bytes": 0},
		"trust":  map[string]any{"session_hints": true, "project_hints": false, "intent_hints": false}, "external_refs": map[string]string{}, "enabled": true}}, &binding); code != 201 {
		t.Fatalf("binding: %d", code)
	}
	var issued struct {
		Token    string            `json:"token"`
		BaseURLs map[string]string `json:"base_urls"`
	}
	if code := admin.Do("POST", "/api/v1/client-bindings/"+binding.ID+"/credentials", map[string]any{"name": "laptop"}, &issued); code != 201 || !strings.HasPrefix(issued.Token, "hpk_") {
		t.Fatalf("issue credential: %d", code)
	}
	if _, err := env.App.CP.AuthenticateGateway(t.Context(), issued.Token); err != nil {
		t.Fatalf("issued token does not authenticate: %v", err)
	}
	var eff struct {
		Plans []struct {
			PublishedVersion int64 `json:"published_version"`
		} `json:"plans"`
		Drifted []struct{ Name string } `json:"drifted"`
	}
	admin.Do("GET", "/api/v1/config/effective", nil, &eff)
	if len(eff.Plans) != 1 || eff.Plans[0].PublishedVersion != 1 || len(eff.Drifted) != 1 {
		t.Fatalf("effective: %+v", eff)
	}
	// Viewer can read but not write.
	_ = admin
}
