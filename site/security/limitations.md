# Known limitations

These are deliberate limits of the design, not bugs waiting for a fix. Read them before you depend on Harbor Pilot
for something they touch. The same list, kept current with the code, is in
[docs/LIMITATIONS.md](https://github.com/aurimasniekis/go-harbor-pilot/blob/main/docs/LIMITATIONS.md).

## Deployment

- **One instance.** SQLite in WAL mode with one writer. There is no multi-writer high availability and no database
  shared between hosts: you scale up, not out. Recovery is restore from backup.
- **Plain HTTP is for local use.** When `server.public_url` is `http://`, session cookies lose the `__Host-` prefix and
  the `Secure` flag, and the server logs a warning.
- **No second factor for local accounts.** Local accounts use a password plus login throttling. If you need MFA, sign
  in through an identity provider that enforces it.
- **Sensitive reads need a fresh sign-in.** Unredacted captured content and its exports need a sign-in within the last
  10 minutes, so API tokens cannot read them, and single sign-on users re-authenticate by signing in again.
- **Gateway failure throttling is per address.** After 60 failed credential checks in a minute from one address,
  every request from that address is refused until the budget refills, valid tokens included. Behind a shared NAT, a
  client sending bad tokens can briefly delay its neighbours.
- **Activity is kept until you delete it.** Requests, attempts, usage, events and the audit log have no automatic
  retention yet, so `harbor.db` grows with traffic; keep an eye on the data directory's disk usage. Captured content,
  sessions and one-time links do expire.

## Masking

- **Masking is best-effort.** Detectors miss formats they do not know. They do not decode base64, normalise letter
  case or join a secret split across lines.
- **Some content is not inspected.** JSON-escaped secrets are found in tool arguments and tool results, not in
  ordinary message text. Tool definitions, media titles and URLs, protocol extension fields, images, documents and
  opaque provider state are not inspected. Tools running on your machine are outside the gateway entirely.
- **Signed reasoning cannot be rewritten.** A request whose signed reasoning contains a value the policy masks or
  blocks is blocked instead.
- **Short configured values.** Configured secret values shorter than 6 characters are rejected when stored. Values
  read through `secret_values_ref` are re-read at run time, and short lines there are skipped.
- **Placeholders are memory-only.** After a restart, or after 24 hours idle, a placeholder the model repeats is
  returned to the tool unchanged. The vault holds at most 200,000 mappings or 128 MiB, and 5,000 mappings or 8 MiB per
  credential; past a credential's budget, values are still masked but cannot be restored.

See [Masking: guarantees and limits](/security/masking).

## Captured content

- **Erasure reaches SQLite, not copies of it.** Deletes and expiry use SQLite and full-text-index secure delete, merge
  the index and truncate the WAL, so deleted text leaves the database files. File-system snapshots, storage-level
  copies and backups taken with `--include-traces` are not reached.
- **A manual delete does not destroy the day's key.** Content keys are shared by every trace of the same day and
  policy, and are destroyed only when that day expires. Until then, a backup taken before the delete can still be
  decrypted.

## Accounting and limits

- **Budgets are local estimates.** They are enforced through reservations inside Harbor Pilot, not as provider caps.
  Usage that happens outside the gateway shows up only through provider-reported quota and usage, reconciled later.
- **Budgets can block slightly early, never late.** They count settled costs plus in-flight estimates, and the
  estimate prices input at four characters per token and output at the requested maximum.
- **The first budget check after a restart or a change is slower.** Harbor Pilot keeps each budget's spend in memory
  and reads only new usage on each request. Right after a restart, **Start over**, or an edit to a budget's dates or
  scope, it adds up the budget's whole window once: up to about 0.3 s for a one-time budget that has seen 200,000
  requests in a year. It does this in the background straight away, but on a busy server the first request after a
  restart or a change can still get there first and wait for it. It also refreshes every budget every 30 seconds, so
  a budget on a quiet project doesn't fall far behind while other projects keep spending.
  Re-creating a one-time budget without **Count spend from** starts a new count.
- **Creating a run token checks budgets only when asked.** Without `check_budget`, an orchestrator gets a run token
  even when a blocking budget that covers its client is used up, and the token's requests are then refused with
  `429`. With `check_budget: true`, client, project, workspace and global budgets are checked, but provider account
  budgets aren't, because the account is picked per request. The check also counts budgets when the run would only use
  local or free accounts, which budgets otherwise ignore, so it can refuse such a run. It skips the check only when
  the client's routing plan allows nothing but `local` and `free` billing. See
  [Agent orchestrators](/tools/orchestrators#budgets).
- **Quota can be stale or unknown.** Routing treats unknown as unknown, never as zero or unlimited; the plan decides
  what happens next.
- **Rate-limit headers are short-lived.** Limits read from response headers are fresh for two minutes, are not kept as
  history and are dropped (and counted) when the writer is busy. They never mark an account exhausted; a `429`
  cooldown does that.
- **Statistics are computed on demand.** A statistics query reads up to 200,000 requests; larger ranges are marked as
  truncated.
- **Filtering by reference has gaps.** Usage totals filtered by a trusted reference cover every usage entry. Request
  and session lists filtered by a reference follow request telemetry, which can drop rows when the writer is busy.
  Usage recorded after a restart carries references only when its request was recorded. A token that can create run
  tokens chooses their references, so when several clients use the same reference keys, filter by client too.
  Subscription fees under a filter are still whole-range figures, so **Spend** hides provider billing and fees while a
  reference filter is on.

## Routing and diagnostics

- **The blocking-traffic check is a sample.** Validating a plan version tries synthetic requests (each mapped model,
  each known tool, streaming and not, every account healthy) and warns when all or most are refused. Rules that depend
  on other request details, time, quota or health can still block traffic it did not try.
- **Test connection is a light check.** It lists the provider's models with the account's credential. That proves the
  address and the credential, not access to a given model, remaining quota or billing. A provider without a model list
  counts as reachable. CLI bridge accounts only report whether the vendor binary is installed.
- **Tool detection is a guess.** The User-Agent and other headers do not prove which program made a request.
- **Protocol Watch reports what it saw.** It is not a provider changelog. A field that stops appearing is reported
  with low confidence, not as removed. Its baseline lives in memory and is rebuilt at start.
- **History deduplication needs a session.** Without a trusted session reference or session header, repeated history
  is recorded as uncertain and never merged across conversations.
- **System messages inside the conversation follow a fixed model list.** Anthropic accepts a `system` entry inside
  `messages[]` only on some models. For models the public documentation lists as unsupported, Harbor Pilot sends the
  entry as user text. Any other model gets it as sent, so a newer model without support would answer 400 until
  Harbor Pilot's list is updated. There is no setting to override the list. The Codex bridge does not carry these
  entries.
- **Passthrough sees little.** In passthrough, usage is read best-effort from the raw stream; tool calls, response
  signals and captured content are not available. Passthrough does not avoid provider flags or restrictions.

## CLI bridges

- **They run on the Harbor Pilot host**, need the vendor binary and a login per account there, and add process
  start-up time to every turn.
- **They expose only what the vendor CLI exposes**: no native passthrough, and usage only as detailed as the CLI
  reports it. Tool calls are carried through MCP, so their ids look different to the vendor model.
- **They run as the server's user.** If the vendor CLI's tool restrictions failed, it could read what the server can.
  The restrictions are layered (tools off by flag, a fail-closed check on every item, read-only sandboxes); for
  defence in depth, run the bridge image or service under a separate user, or in a container that cannot read the data
  directory and the master key.
- **Parked processes are local state.** When a Claude Code process ends between turns, its session is kept on disk for
  up to the conversation's prompt-cache lifetime after its last turn (at most an hour) and the next turn continues it. After a restart, or
  once that time has passed, the next request resumes from a transcript rebuilt from your tool's history, with the
  loss recorded, and the prompt cache is usually written again.
- **Claude Code bridge fidelity.** The model sees a system prompt prefixed with the CLI's identity line and an
  environment note, and your tool's tools appear with an `mcp__client__` prefix (mapped back for the client).
  Sampling parameters, stop sequences and forced tool choice are not passed; each is recorded as an approximate loss.
  The output limit and effort are passed. A system message with text inside the conversation reaches the model as
  user text. Provider-run tools such as web search are not available. Your tool's prompt-cache breakpoints
  are replaced by the CLI's own, which keep your cache lifetime (one hour or five minutes) but not your placement
  (recorded as an approximate loss).
- **Codex bridge history.** The CLI runs one turn per process. Conversations the bridge served continue with
  `codex exec resume`; a conversation seen for the first time is replayed as text, recorded as a `history_replay`
  loss. A turn in which the CLI still starts a command, file change or web search is stopped with an error.
- **Codex keeps its conversation files** so resuming works, and removes them once older than 24 hours, not when the
  turn ends.
- **One Codex process per account.** The CLI refreshes its sign-in in place, so by default one process per account
  runs at a time (`bridges.codex.max_per_account`).

## Events and imports

- **Delivery is at least once.** Events and webhooks can repeat; deduplicate by event id.
- **Quota tracking imports usage, not prices.** Quota adapters can report model prices and API-key inventories;
  Harbor Pilot imports usage and quota buckets only and keeps model pricing on providers.
- **The Quota Harbor importer is partial.** It imports monitored accounts, the Telegram channel and observation
  history. Pricing, budgets and threshold alert rules are listed as skipped. `env_file` references, and `file`
  references with a `key`, must be set again after import, and in-app sign-ins need a new sign-in. Stop the server
  while it runs. See [Migrating from Quota Harbor](/operators/migrate-quota-harbor).

## Not verified against live services yet

Every capability is covered by tests against synthetic fixtures, fakes and embedded servers. None has yet been checked
against live provider accounts, subscription logins, the Codex, OpenCode and Pi binaries, a Porch instance, a Pulse
server or real Telegram, Slack and Discord endpoints. The [compatibility matrix](/reference/compatibility) shows the
status of each cell.
