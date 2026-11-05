# Harbor Pilot

Harbor Pilot connects coding agents to models and subscriptions, routes each request through explicit policies, and explains what happened.

It is a self-hosted LLM gateway and policy control plane: a single Go binary on SQLite, with an embedded web UI. Claude Code, Codex, OpenCode, Pi and generic API clients connect to it with a gateway token and a base URL. Harbor Pilot then does the following:

- **Translates** between the OpenAI Chat and Responses, Anthropic Messages and Gemini protocols.
- **Routes** requests through versioned plans: aliases, pools, restrictions, decision tables and Expr rules, and fallback policy.
- **Executes** against API-key providers, local models, or the vendor's own CLIs (Claude Code, Codex) running under isolated, authorized subscription logins.
- **Accounts** for quota, consumption, budgets, charges and estimates as separate figures.
- **Masks** sensitive data, and keeps short-lived encrypted traces only when a policy allows it.
- **Emits** durable events to Pulse and webhooks, and notifications to Telegram, Slack, Discord and webhooks.
- **Explains** every request in the "Why this route?" view.

## Quick start
```
make demo        # synthetic providers and traffic on http://127.0.0.1:8080
```

For a real deployment:
```
harbor-pilot keys generate > /etc/harbor-pilot/master.key && chmod 600 /etc/harbor-pilot/master.key
harbor-pilot serve --config deploy/examples/harbor-pilot.yaml
harbor-pilot admin bootstrap --config deploy/examples/harbor-pilot.yaml   # one-time setup link
harbor-pilot harness setup claude --url https://pilot.example.com --token-file ~/.hp-token
```

## Documentation
- [Product](docs/PRODUCT.md), [architecture](docs/ARCHITECTURE.md), [decisions](docs/DECISIONS.md), [threat model](docs/THREAT_MODEL.md)
- [Compatibility matrix](docs/COMPATIBILITY.md), [limitations](docs/LIMITATIONS.md), [requirement traceability](docs/TRACEABILITY.md)
- Operations: [deployment](docs/operations/deployment.md), [master key](docs/operations/master-key.md), [backup and restore](docs/operations/backup-restore.md), [upgrade and rollback](docs/operations/upgrade-rollback.md), [troubleshooting](docs/operations/troubleshooting.md), [demo](docs/operations/demo.md)
- Guides: [harness setup](docs/guides/harnesses.md), [migrating from Quota Harbor](docs/guides/migrate-quota-harbor.md)
- API: `api/openapi.yaml` (generated; `harbor-pilot openapi`)

## Development
```
make check       # vet, lint, gofmt, race tests, web typecheck/lint/test/build
make dev         # demo instance from the working tree
make e2e         # Playwright against the demo binary
```

See [AGENTS.md](AGENTS.md) for conventions.

## License
Apache-2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
