# Reference: human authentication parameters (Hallpass)

- **Source:** `aurimasniekis/go-hallpass` @ `cc83dba` (v0.7.4). License Apache-2.0 (`LICENSE` + `NOTICE`).
- **Status:** all code lives under `internal/`, so Go cannot import it from another module. Harbor Pilot
  **re-implements** these parameters in `internal/auth` and copies no code. Harbor Pilot code does not name
  the reference. This page is the only record of where the parameters came from.

Anything not marked otherwise is verified-in-source.

## 1. Password hashing (`internal/opauth/password.go`)

| Parameter | Value | Harbor Pilot |
|---|---|---|
| Algorithm | Argon2id (`golang.org/x/crypto/argon2.IDKey`) | same |
| Encoding | PHC string `$argon2id$v=19$m=…,t=…,p=…$<salt b64>$<hash b64>` | same |
| Param set v1 | Memory `64*1024` KiB, Time 3, Threads 2, KeyLen 32, SaltLen 16 | same, versioned param sets |
| Rehash | on successful login when the param version is outdated | same |
| Normalisation | NFKC (`NormalizePassword`) | same |
| Max length | `MaxPasswordBytes = 1024` after normalisation; longer input is **rejected**, never truncated | same |
| Min length | default 12 bytes (`Options.PasswordMinBytes`) | same default, configurable |
| NUL bytes | rejected | same |
| Parse bounds | stored `m ≤ 4 GiB` (`4*1024*1024` KiB), `t ≤ 100`, non-zero | same (DoS guard on stored hashes) |
| Unknown account | verifies a dummy hash, so timing does not reveal whether the account exists (`dummyHash()`) | same |
| Concurrency | `HashConcurrency` semaphore limits how many hashes run at once | same |

## 2. Web sessions (`internal/opauth/sessions.go`, `service.go`, `internal/httpapi/server.go`)

| Parameter | Value |
|---|---|
| Storage | server-side SQLite table `web_sessions` (id_hash, operator_id, csrf_secret, auth_method, mfa_at, auth_at, created_at, last_seen_at, absolute_expires_at, ip, user_agent, revoked_at, revoke_reason, display_id, auth_provider) |
| Token at rest | only `sha256("hallpass/session/v1|" + token)` is stored |
| Idle timeout | 30 min (`service.go:72`) |
| Absolute timeout | 12 h (`service.go:75`) |
| Step-up / re-auth window | 5 min (agent report; not re-read for this page) |
| Idle refresh | written at most once per minute; `Validate` (no refresh) is used by SSE |
| Cookies | `__Host-hp_session`, `__Host-hp_oidc`; on plain-http dev, unprefixed names |
| Cookie flags | `Path=/`, `HttpOnly`, `Secure` (off only for dev http), `SameSite=Lax` (`server.go:219`) |
| Revocation | `revoked_at` checked on every request; disabling a user or unlinking SSO revokes sessions |
| Auth methods | `password`, `oidc`, `breakglass`, `token` (bearer automation tokens never fall back to cookies) |

Harbor Pilot mapping:
- Cookies are `__Host-hp_session` / `__Host-hp_oidc`. The `hp_` prefix stands for Harbor Pilot.
- The hash domain separator is `harbor-pilot/session/v1|`.
- Defaults are the same: 30 min idle, 12 h absolute.
- Gateway credentials and automation tokens are a **separate** credential class and are never
  accepted as a UI session.

## 3. CSRF (`internal/httpapi/server.go:254`, `internal/opauth/sessions.go:186`)

1. **Origin gate.** State-changing requests need an `Origin` equal to the public origin, **or**
   `Sec-Fetch-Site: same-origin`.
2. **Token.** `X-CSRF-Token` must equal `base64url(HMAC-SHA256(csrf_secret, "hallpass/csrf/v1|" + display_id))`,
   compared with `hmac.Equal`. The SPA receives it in the `/me` response (`csrf_token`).
3. Bearer-token requests skip both checks: they carry no ambient credential.

The Harbor Pilot separator is `harbor-pilot/csrf/v1|`.

## 4. Rate limiting and login throttling

| Mechanism | Value | Source |
|---|---|---|
| In-memory fixed window | `ratelimit.Window`, 4096-key cap plus an overflow bucket | `internal/ratelimit/window.go` |
| Keys | `ip:` (IPv6 grouped by /64), `auth:` (non-GET auth endpoints), `anon:`, `ses:` | `internal/httpapi/server.go` |
| Rejection | 429 with `Retry-After: 60` | `server.go:280,300` |
| Persistent throttle | per account **and** per IP; the first `throttleFree = 3` failures are free, then the delay is `1s << (failures-4)`, capped at `throttleMax = 15 min`; counters decay after `throttleDecay = 24 h`; no permanent lockout; `ErrThrottled` does not reveal whether the account exists | `internal/opauth/login.go:18-23,70-71` |

## 5. Bootstrap and break-glass

| Item | Value | Source |
|---|---|---|
| First admin | offline CLI `hallpass admin bootstrap`: takes the data-dir lock and needs the KEK; prints a one-time `/setup?token=…` link | `cmd/hallpass/admin_cmd.go`, `opauth.IssueSetupToken` |
| Setup token TTL | `SetupTokenTTL = 30 min` | `internal/opauth/tokens.go:19` |
| Invite TTL | `InviteTTL = 72 h` | `tokens.go:20` |
| Password reset TTL | `ResetTTL = 1 h` | `tokens.go:21` |
| Recovery login | offline `admin recover --email E [--new-admin]`: single-use link with `RecoveryLoginTTL = 30 min`; works even in SSO-only mode | `tokens.go:22`, `IssueRecoveryLogin` |
| Break-glass alert | raised at every startup until an admin acknowledges it | `BreakGlassAlert`, `POST /system/breakglass-ack` |

Harbor Pilot: `harbor-pilot admin bootstrap` and `harbor-pilot admin recover` behave the same way. The only
difference is that the CLI reads the email from a flag and **reads no secret from argv**.

## 6. Roles and permissions (`internal/rbac/rbac.go`)

- **Model:** fixed roles plus permission strings. Each API operation declares one `x-permission`, and
  `Matrix.Authorize` is the single decision point.
- **Role sources:** each assignment records where it came from (local or SSO). SSO-sourced roles apply only
  to SSO sessions.

Harbor Pilot roles are `admin`, `operator` and `viewer`. Separate permissions cover trace content:
`traces.content.read`, `traces.content.unredacted` and `traces.export`. Hallpass's own role set
(`administrator`, `approver`, `auditor`, `viewer`) is **not** copied. TOTP MFA exists in Hallpass; Harbor
Pilot delegates MFA to the OIDC provider and records local-password TOTP as a tracked enhancement (see
`../TRACEABILITY.md`).

## 7. OIDC client checks (`internal/oidc/flow.go`, `provider.go`)

| Check | Value |
|---|---|
| Libraries | `github.com/coreos/go-oidc/v3` (v3.21.0), `golang.org/x/oauth2` |
| Flow | Authorization Code + PKCE `S256` only |
| State, nonce, verifier | kept server-side, single use, hashed or sealed, bound to the browser via the `__Host-*_oidc` cookie; TTL ≤ 10 min (`provider.go:172`) |
| Redirect URI | fixed: `<public_url>/auth/oidc/callback`; start is `/auth/oidc/{provider}/start` |
| ID token signature | RS256, ES256 or EdDSA via JWKS (`flow.go:376`) |
| Claims | `iss`, `aud`, `azp` when there are several audiences, `exp/iat/nbf` with skew ≤ 60 s (`provider.go:181`), nonce compared as a hash in constant time |
| Identity key | `iss` + `sub`, never email |
| Token retention | access and refresh tokens discarded; the ID token is not stored |
| Groups | `groups_claim` is a dotted path (e.g. `porch.groups`); groups map to roles only through explicit allowlist rows |
| Provisioning | `preprovisioned` or `jit`; JIT requires a restriction (domain, `hd`, `tid` or group) |
| Step-up | `prompt=login&max_age=0`, then `auth_time` is checked |
| Logout | RP-initiated logout to `end_session_endpoint` with `client_id` + `post_logout_redirect_uri` |

Harbor Pilot keeps all of these. Linking an SSO identity to an existing local account requires an
authenticated session (purpose `link`). The email claim alone **never** links accounts.
