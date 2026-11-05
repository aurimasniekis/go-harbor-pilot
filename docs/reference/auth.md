# Authentication and Authorization

## Principals

| Kind | How it authenticates | Notes |
|---|---|---|
| User | Browser session cookie created by password, OIDC or break-glass sign-in | Human operators. |
| Service principal | `Authorization: Bearer hpa_<prefix>_<secret>` | Terraform, CI and integrations. Never uses cookies, so CSRF does not apply. |
| Gateway client | `hpk_…` gateway credential bound to a client binding | Data plane only (see `docs/contracts/gateway.md`). Cannot call the control-plane API. |

## Roles and permissions

Every API operation declares exactly one permission, published as `x-permission` in `api/openapi.yaml`.

| Permission | viewer | operator | admin |
|---|---|---|---|
| `config.read`, `usage.read`, `activity.read`, `traces.metadata.read` | ✓ | ✓ | ✓ |
| `config.write`, `policy.publish`, `secrets.write`, `tokens.manage`, `events.manage`, `notifications.manage`, `compat.manage`, `audit.read`, `traces.delete` | | ✓ | ✓ |
| `users.manage`, `auth.settings`, `system.manage` | | | ✓ |
| `traces.content.read`, `traces.content.unredacted`, `traces.export` | explicit grant | explicit grant | explicit grant |

Trace-content permissions are never implied by a role. An administrator grants them per user (`PUT /users/{id}/grants`) or per service principal, and every grant is audited.

## Passwords

- Hashing is Argon2id with m=64 MiB, t=3, p=2, a 16-byte salt and a 32-byte key, stored in PHC format.
- Parameters are versioned. A hash made with weaker parameters is upgraded transparently on the next successful login.
- Passwords are NFKC-normalized and must be 12–1024 bytes. Over-long input is rejected, never truncated. NUL characters are rejected.
- At most 4 hashes are computed concurrently, which bounds memory use.
- Requests for unknown accounts verify a dummy hash, so response timing does not reveal whether an account exists.

## Login throttling

Failures are counted per account and per client IP. IPv6 addresses are grouped by /64.

- The first 3 failures are free.
- After that, each failure doubles the lockout, up to 15 minutes.
- Counters decay after 24 hours without failures.
- There is no permanent lockout.
- The login endpoint also has a per-IP request rate limit.

## Sessions

- **Storage.** Sessions live server-side in `web_sessions`. Only an HMAC of the cookie token is stored, keyed by a server key that is itself encrypted under the master key.
- **Cookie.** `__Host-hp_session` with `Secure; HttpOnly; SameSite=Lax; Path=/`. When the public URL is plain `http`, which is for development only, the cookie is named `hp_session` and is not marked `Secure`; the server logs a warning at startup.
- **Lifetime.** Idle timeout defaults to 30 minutes and absolute lifetime to 12 hours; both are configurable in `/auth/settings`. The idle deadline is refreshed at most once per minute.
- **Revocation.** All of these revoke sessions: sign-out, password change (other sessions), disabling the user, unlinking an SSO identity (that user's SSO sessions), and an administrator using "sign out everywhere".
- **SSO-sourced roles.** A role assigned through SSO group mapping applies only to SSO sessions. If such a user signs in with a password, they get at most viewer.

## CSRF

A mutation authenticated by cookie must pass two checks. A failure on either returns 403.

1. The `Origin` header must equal the public origin. When `Origin` is absent, `Sec-Fetch-Site: same-origin` is required instead.
2. The request must carry `X-CSRF-Token`, which must equal `base64url(HMAC-SHA256(session_csrf_secret, "harbor-pilot/csrf/v1|"+session_id))`. The UI gets this token from `GET /session`.

Bearer-token requests skip both checks.

## First administrator and recovery

- `harbor-pilot admin bootstrap` runs offline and prints a one-time `/setup?token=…` link, valid for 30 minutes. It works only while no active administrator exists.
- `harbor-pilot admin recover --email E` prints a one-time `/recover?token=…` break-glass link, valid for 30 minutes.
  - It always works, even when password sign-in is disabled.
  - It re-enables the user if they were disabled.
  - It is audited and creates a persistent security alert. Every administrator sees the alert until one acknowledges it.
- Invitations are valid for 72 hours and password resets for 1 hour. Both are single use and only their hashes are stored.

## OIDC single sign-on

The flow is authorization code with PKCE S256, `state` and `nonce`.

**Flow state**
- State is stored server-side. The PKCE verifier is sealed under the master key, and `state`, `nonce` and the browser-binding value are stored only as hashes.
- State is single use, expires after 10 minutes, and is bound to the browser by the `__Host-hp_oidc` cookie.

**Validation**
- The issuer comes from discovery and the ID token signature must verify.
- `aud` must contain the client ID, and `azp` is checked when there are several audiences.
- `exp`, `iat` and `nonce` are checked.
- The RFC 9207 `iss` callback parameter is checked when present.
- Allowed algorithms are configurable; the default is ES256, RS256 or EdDSA.

**Identity**
- Users are keyed on `(issuer, subject)`, never on email alone.
- Access tokens and refresh tokens are discarded. ID tokens are not stored.

**Provisioning**

| Mode | Behaviour |
|---|---|
| `preprovisioned` | Links to an existing account only if the provider asserts `email_verified=true` and that account has no identity from this issuer yet. |
| `jit` | Creates the user. Requires `allowed_domains` or role mappings. Never takes over an unlinked local account with the same email. |

**Role mapping**
- Groups are read from a dotted claim path.
- If several groups match, the highest-privileged role wins.
- If no group matches, the provider's default role applies. When there is no default role, access is denied.
- For SSO-managed users, the role is re-evaluated on every login, and losing all mapped groups denies access.

**Testing a provider**
- `POST /auth/oidc-providers/{id}/test` runs the flow without signing anyone in.
- It shows the mapped claims and role. The result is kept in memory for 10 minutes and only the admin who started the test can read it.

### Porch

- Register a `kind: oidc` application with the redirect URI `<public_url>/api/v1/auth/oidc/callback`.
- Request the scopes `openid profile email groups` and enable the application's `groups` disclosure.
- Set `groups_claim: porch.groups`.
- Porch signs ID tokens with ES256 by default.
- Do not rely on `porch.roles`. Porch currently always emits an empty list (`docs/contracts/reference-porch-oidc.md`).

Status: contract-tested against an in-process OpenID Provider that uses Porch's claim shape. It has not been live-verified against a Porch instance. See LIMITATIONS.

## Audit log

Every security, configuration and content-access action is written to `audit_log`, in the same transaction as the change wherever possible.

- **Fields.** Each entry records actor, action, target, outcome, IP, request ID and redacted details. Details are limited to 8 KiB.
- **Query.** Use `GET /audit`, which needs `audit.read`. Results are cursor-paginated, newest first, and can be filtered by action prefix, target, actor and outcome.
