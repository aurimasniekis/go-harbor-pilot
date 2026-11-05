# Reference: Porch as OpenID Provider

- **Source:** `aurimasniekis/go-porch` @ `d92cdb0` (v0.3.1). License Apache-2.0. Code is under `internal/`
  and not importable. Harbor Pilot integrates over HTTP only, as a standard OIDC relying party.
- **What Porch is:** a self-hosted identity and SSO service. It is an OpenID Provider (built on
  `zitadel/oidc/v3` `op.Server`, but Porch issues its own tokens) and a forward-auth outpost. Upstream logins
  are Google and a password-plus-recovery-answers "backup login".

Harbor Pilot's OIDC client is generic. "Porch compatible" means it was tested against the contract below.
No other provider is special-cased in code.

## 1. Endpoints (`internal/oidcop/provider.go:41-48`)

These are verified-in-source. All are relative to the issuer URL.

| Purpose | Path |
|---|---|
| Discovery | `/.well-known/openid-configuration` |
| Authorize | `/oauth2/authorize` |
| Token | `/oauth2/token` |
| Userinfo | `/oauth2/userinfo` |
| Revoke | `/oauth2/revoke` |
| Introspect | `/oauth2/introspect` |
| End session | `/oauth2/end_session` |
| JWKS | `/jwks` |
| Internal continuation | `/oauth2/continue` (not a client endpoint) |

Harbor Pilot discovers every endpoint from discovery. It hard-codes none.

## 2. Discovery values (`provider.go:153-163`)

Verified-in-source unless marked.

| Field | Value |
|---|---|
| `scopes_supported` | `openid profile email offline_access groups` |
| `response_types_supported` | `code` (agent report; not re-read) |
| `response_modes_supported` | `query` (agent report) |
| `grant_types_supported` | `authorization_code`, `refresh_token`, `client_credentials` |
| `id_token_signing_alg_values_supported` | `ES256` (default), `RS256` |
| `token_endpoint_auth_methods_supported` | `client_secret_basic`, `client_secret_post`, `none` |
| `code_challenge_methods_supported` | `S256` |
| `claims_supported` | `iss sub aud exp iat auth_time nonce amr azp at_hash name email email_verified porch` |
| `authorization_response_iss_parameter_supported` | `true` (RFC 9207; agent report) |
| `request` / `request_uri` | not supported (agent report) |

## 3. Authorize and token rules

- PKCE is required for public clients and for confidential clients with `require_pkce`. A `code_challenge_method`
  other than `S256` is rejected (`provider.go:268-270`). The verifier is checked in constant time (`provider.go:374`).
  Verified-in-source.
- `openid` is required. Requested scopes are filtered to the client's `allowed_scopes`. `resource` must be one
  of the client's `audiences` (agent report).
- Lifetimes: auth request 10 min, code 1 min (agent report).
- Doc-claim (`site/guide/connect-an-app.md`):
  - Redirect URIs must match exactly; native apps get the RFC 8252 loopback-port exception.
  - Codes are single-use, and reusing one revokes the grant.
  - Refresh tokens rotate, with reuse detection per family.
  - The user must be a tenant member.

## 4. Claims (`internal/oidcop/tokens.go`)

| Claim | When |
|---|---|
| `iss`, `sub`, `aud=[client_id]`, `azp`, `exp`, `iat`, `auth_time` | always; `sub` is a stable public ID `u_…`, never an email |
| `nonce`, `amr`, `at_hash` | when applicable |
| `name` | `profile` scope |
| `email`, `email_verified` | `email` scope **and** the application's `email` disclosure |
| `porch` | `{"v":1,"tenant":{"id","slug"},"roles":[…]?,"groups":[…]?,"truncated":true?}` |

`porch` claim details (verified-in-source, `tokens.go:91-107`, `provider.go:56`):
- The claim is capped at `maxPorchClaimBytes = 2048`. When it is too big, `groups` is dropped first, then
  `roles`, and `truncated: true` is set.
- **Caveat: roles are always empty.** `roles` is emitted as an empty list (`tokens.go:96`). The code comment
  says roles come from the future authorization engine. **Harbor Pilot must not map roles from `porch.roles`.**
  Map from `porch.groups`, and treat `truncated: true` as "membership unknown": deny any group-derived role
  and audit the event.
- `groups` appears only with the `groups` scope **and** the application's groups disclosure.

Other token facts (agent report):
- Access tokens are JWTs with `typ: at+jwt` (RFC 9068).
- Refresh tokens are opaque, with the prefix `porch_rt_`.
- `EndSession` accepts an optional `id_token_hint`. `post_logout_redirect_uri` must be registered.
  There is no front-channel or back-channel logout.

## 5. Client registration fields

These come from the OpenAPI `OIDCClient` schema and `connect-an-app.md` (doc-claim, cross-checked against
schema field names):
- Create with `POST /api/v1/tenants/{tenant}/applications`, body
  `{slug, name, kind:"oidc", client:{redirect_uris, post_logout_redirect_uris, grant_types}, disclosure:{email, groups, roles, entitlements}}`.
  `client_secret` is returned **once**.
- `OIDCClient` fields: `client_id, client_type` (confidential/public), `application_type` (web/native/user_agent),
  `redirect_uris, redirect_uris_exact, post_logout_redirect_uris, grant_types, allowed_scopes, allowed_origins,
  audiences, require_pkce, signing_alg, access_token_ttl_seconds, id_token_ttl_seconds, refresh_ttl_seconds,
  refresh_idle_seconds, secret_hint, issuer, discovery_url`.
- Per-app `access_mode` is `all_members` or `assigned`.

## 6. What Harbor Pilot requires from a Porch app registration

| Setting | Value |
|---|---|
| `kind` | `oidc` |
| `client_type` | `confidential` (Harbor Pilot is a server-side web app) |
| `application_type` | `web` |
| `redirect_uris` | `<public_url>/auth/oidc/callback` (exact) |
| `post_logout_redirect_uris` | `<public_url>/login?logged_out=1` |
| `grant_types` | `authorization_code` (`refresh_token` is not needed: Harbor Pilot does not keep IdP tokens) |
| scopes requested | `openid profile email groups` |
| disclosure | `groups: true`, `email: true` (optional; email is display-only, never the identity key) |
| `require_pkce` | `true` (Harbor Pilot always sends S256) |
| `signing_alg` | default `ES256` is accepted; `RS256` also works |
| Harbor Pilot config | issuer URL, client ID, client secret (secret ref or encrypted), `groups_claim: porch.groups`, explicit group→role allowlist rows, provisioning `preprovisioned` or `jit` with a group restriction |

Identity is keyed on `(iss, sub)`. Logout redirects to the discovered `end_session_endpoint` with `client_id` and
`post_logout_redirect_uri`. Harbor Pilot keeps no ID token, so it sends no `id_token_hint`.

## 7. Test strategy

- **Contract tests** (Phase 2) use an in-process test OpenID Provider:
  - It reproduces this contract: ES256 and RS256 keys, `porch` claim shape with truncation, empty `roles`,
    `auth_time`, PKCE S256 enforcement, and RFC 9207 `iss` in the authorization response.
  - Negative cases: wrong `iss`/`aud`, expired token, nonce mismatch, bad PKCE, alg `none`/HS256 rejected.
- **Live verification** against a real Porch instance is opt-in (`scripts/live/oidc-porch.sh`) and recorded in
  `../COMPATIBILITY.md`. Until then the capability is `contract-tested`.
