# People, API tokens and sign-in

Three kinds of identity reach the control plane:

- **People** sign in to the web UI with a password or single sign-on.
- **Automation accounts** (Terraform, CI, dashboards) use **API tokens**.
- **Clients** (coding tools and SDKs) use **access tokens** on the gateway. They are covered in
  [Clients and access tokens](/operators/clients), not here.

All of this lives under **Settings**: **People**, **API tokens** and **Sign-in & SSO**.

## Roles

Every person has one role.

| Role | Can |
|---|---|
| **Viewer** | Read configuration, usage and requests, and see that content was captured. |
| **Operator** | Everything a viewer can, plus change configuration, publish plans, set secrets, manage client tokens, events, alerts and protocol-path overrides, read the audit log and delete captured content. |
| **Admin** | Everything, including people, sign-in settings, automation accounts and backups. |

**Captured content access** is separate from the role, on purpose: even an admin can't read prompts and replies
until someone grants it. See [Captured content](/operators/captured-content#who-can-read-it).

| Permission | Viewer | Operator | Admin |
|---|:-:|:-:|:-:|
| `config.read`, `usage.read`, `activity.read`, `traces.metadata.read` | ✓ | ✓ | ✓ |
| `config.write`, `policy.publish`, `secrets.write`, `tokens.manage` | | ✓ | ✓ |
| `events.manage`, `notifications.manage`, `compat.manage` | | ✓ | ✓ |
| `audit.read`, `traces.delete` | | ✓ | ✓ |
| `users.manage`, `auth.settings`, `system.manage` | | | ✓ |
| `traces.content.read`, `traces.content.unredacted`, `traces.export` | granted per person | | |

## People

<Shot name="people" alt="Settings, People tab, listing Demo Admin and Demo Viewer with their roles, extra access and last sign-in" caption="Settings → People" />

The list shows each **Person**, their **Role**, what they **Can also** do (content grants) and their **Last sign-in**.
A person who can't sign in is marked **Cannot sign in**; a role set by SSO group mapping is marked **From SSO**.

### Invite someone

**Settings → People → Invite user**: **Email**, **Display name**, **Role**, and **Signs in with**:

<Shot name="people-invite" alt="Invite user dialog with email, display name and role, over the People list" caption="Invite user. With an SSO provider set up, the dialog also asks how the person signs in." />

- **Password (invitation link)**: Harbor Pilot shows a link. Send it to the person yourself; it lets them choose a
  password and expires after 72 hours. Harbor Pilot does not send email.
- **An SSO provider**: the person signs in with that provider. Their identity is linked on the first sign-in, if the
  verified email matches and is in the provider's allowed domains.

### Change someone's access

From a person's **⋯** menu:

| Action | What it does |
|---|---|
| **Edit role and access** | Change the display name, **Role**, **Can sign in**, and **Captured content access**. Linked SSO identities can be unlinked here. |
| **New invitation link** / **Create a password reset link** | A fresh link. A reset link expires after one hour. |
| **Sign out everywhere** | Ends every session of that person. |
| **Remove** | Deletes the person. You can't remove yourself. |

Each person manages their own name, password, SSO links and sessions under **My account** (the menu at the bottom of
the sidebar). Changing a password signs out their other sessions. Passwords need at least 12 characters.

<Shot name="account" alt="My account page with the profile, a change password form and the list of active sessions" caption="My account: profile, password and active sessions" />

## API tokens and automation accounts

API tokens belong to **automation accounts**, never to people. Each account has exactly the permissions it needs, and
can hold several tokens.

<Shot name="api-tokens" alt="Settings, API tokens tab, with the demo-terraform automation account marked Terraform, its Change permissions and its creation time" caption="Settings → API tokens: one automation account with its tokens" />

**Settings → API tokens → New automation account.**

1. A name and a **Description** (what uses it, for example *Terraform in CI*).
2. **Access**: start from a preset, or choose **Custom** and pick permissions:

   | Preset | Permissions |
   |---|---|
   | **Terraform** | Reads and changes configuration, publishes plans, sets secrets and SSO: `config.read`, `config.write`, `policy.publish`, `secrets.write`, `tokens.manage`, `events.manage`, `notifications.manage`, `compat.manage`, `auth.settings`, `usage.read`. |
   | **Read-only** | For CI checks and dashboards: `config.read`, `usage.read`, `activity.read`. |
   | **Custom** | Pick from **Read**, **Change**, **Administer** and **Captured content**. |

3. **Active**: when off, every token of the account stops working at once.

Then open the account's **API tokens** and issue one: a **Token name** (for example `terraform-ci`) and **Expires
after** 30 days, 90 days, 1 year or **No expiry**. The token (`hpa_…`) is shown **once**. Use it as
`Authorization: Bearer …`, or put it in `HP_API_TOKEN` / a file named by `HP_API_TOKEN_FILE` for the CLI and the
[Terraform provider](/operators/terraform). The list shows when each token **Expires** and was **Last used**. Revoke a
token from its row.

<Shot name="api-token-issued" alt="API token issued dialog over the demo-terraform tokens list, showing a new hpa_ token once with a copy button and how to send it" caption="An API token is shown once" />

An account created by Terraform is marked **Terraform**; changes made to it in the UI show up as drift.

API tokens can never pass the recent-sign-in check below, so no token can read unredacted content or download
captures.

## Sign-in and sessions

<Shot name="sso" alt="Settings, Sign-in and SSO tab, with password sign-in on, idle timeout of 30 minutes, longest session of 12 hours, and no identity providers yet" caption="Settings → Sign-in & SSO" />

| Setting | Default | Range |
|---|---|---|
| **Password sign-in** | On | When off, only SSO works. Break-glass recovery always works. |
| **Sign out after idle** | 30 min | 5 minutes to 24 hours |
| **Longest session** | 12 hours | Up to 30 days |

Failed password sign-ins are throttled per account and per address: after three failures each further one doubles the wait, up to 15 minutes. There is no permanent lockout. Session cookies are `Secure` only when `server.public_url` is
an `https://` address; the **System & backups** tab warns while it isn't.

## Single sign-on (OIDC)

Harbor Pilot signs people in with any OpenID Connect provider, using the authorization-code flow with PKCE, state and
nonce. Accounts are matched on the provider's **user id** (`iss` + `sub`), never on email alone.

::: info Status
OIDC sign-in, account linking and group role mapping are **contract-tested** against an in-process OpenID provider,
including Porch's claim shape. They have not been verified against a live Porch or other provider yet.
:::

**Settings → Sign-in & SSO → Add provider.** Choose **OpenID Connect provider** or **Porch**, then follow the three
steps in the dialog:

<Shot name="sso-add" alt="Add identity provider dialog on OpenID Connect provider: the redirect URI to register with a warning that the public URL is plain http, display name, short name, issuer URL, client ID, client secret and groups claim, group to role mappings, what everyone else gets and how new people are admitted" caption="Add identity provider: register the redirect URI, enter the details, decide who gets in" />

1. **Register this redirect URI with the provider.** The dialog shows it:
   `https://pilot.example.com/api/v1/auth/oidc/callback`. It warns when your public URL is plain http, which most
   providers refuse.
2. **Enter the details it gives you.**

   | Field | Meaning |
   |---|---|
   | **Display name** | Shown on the sign-in button. |
   | **Short name** | Used in URLs and the API (for Porch, `porch`). It can't be changed later. |
   | **Issuer URL** | For example `https://id.example.com`. Discovery is read from it. |
   | **Client ID** | From the provider. |
   | **Client secret** | Stored encrypted. Leave empty for a public client. |
   | **Groups claim** | Where groups are in the ID token: `groups`, or `porch.groups` for Porch. |

3. **Decide who gets in.**

   - **Group mappings**: a group (for example `platform-admins`) and the role it gets. With several matching groups,
     the highest role wins. Roles from groups are applied on every sign-in.
   - **Everyone else**: the role for people whose groups match no mapping, or **No access**.
   - **New people**: **Must be invited first** (invite them under People and choose this provider) or **Created on
     first sign-in**. For the latter you must list **Allowed email domains** or add a group mapping, so not everyone
     at the provider gets in.
   - **Show on the sign-in page**.

Under **Advanced**: **Scopes** (default `openid profile email groups`) and **Issuer is on a private or local network**
(allows plain http and private addresses for the issuer; cloud metadata addresses stay blocked).

### Porch

Choosing **Porch** fills in the name `porch` and the groups claim `porch.groups`. In Porch, also enable the
application's groups disclosure and request the `groups` scope; groups then arrive in `porch.groups`.

### Test before you rely on it

**Test** on a provider's row runs a real sign-in without creating a session, and shows the **Test sign-in result**:
the issuer, subject, email (and whether it is verified), groups, and the role Harbor Pilot would give. Use it to get
the group mappings right before you turn off password sign-in.

### Existing accounts and SSO

With **Must be invited first**, an SSO sign-in links to an existing account only when an admin has marked that
account for the provider (by inviting it with that provider, or `PATCH /api/v1/users/{id}` with
`{"sso_provider":"<slug>"}`), the email is verified and it is in the allowed domains. A matching email alone never
takes over an account.

## Step-up sign-in

Some actions need a sign-in within the **last 10 minutes**, even with a valid session:

- reading **unredacted** captured content;
- **downloading** captured content;
- setting a first password on an account that only had SSO.

Otherwise the action is refused with `403 reauth_required` and the message *sign in again (or confirm your password) to
continue*. Sign out and in again, then repeat the action. A script using a session can confirm the password with
`POST /api/v1/session/reauth` instead.

## Break-glass recovery

If no admin can sign in (SSO is down, a password is lost), on the server run:

```sh
harbor-pilot admin recover --email admin@example.com --config /etc/harbor-pilot/config.yaml
```

It prints a single-use recovery link. Using it is audited and raises a **security alert** that stays at the top of
the [audit log](/operators/audit) until an admin acknowledges it. Recovery works even when password sign-in is off.

To create the very first admin on a new instance, use `harbor-pilot admin bootstrap` instead (see
[First run](/guide/first-run)).

## With Terraform

`harborpilot_auth_settings`, `harborpilot_oidc_provider` (with a write-only `client_secret_wo`),
`harborpilot_service_principal` and `harborpilot_api_token` manage all of the above except people. See
[Terraform](/operators/terraform).
