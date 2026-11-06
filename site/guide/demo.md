# Try the demo

Demo mode runs Harbor Pilot with a synthetic dataset and synthetic providers, so you can click through every page
without an API key. Nothing leaves your machine: the "providers" are built into the binary.

## Run it

From a checkout of the [repository](https://github.com/aurimasniekis/go-harbor-pilot) (needs Go 1.27, Node.js 24 and
pnpm):

```sh
make build
./bin/harbor-pilot serve --demo --listen 127.0.0.1:8080 --data-dir ./.data-demo
```

Open <http://127.0.0.1:8080> and sign in as `admin@example.test` with the password `harbor-pilot-demo`. A
`viewer@example.test` account with the same password shows the read-only view.

`make dev` does the same with a separate data directory and keeps it between runs; `DEV_RESET=1 make dev` starts
fresh.

::: tip With Docker
Container images are published with releases. With an image, publish the port on your loopback address only:

```sh
docker run --rm -p 127.0.0.1:8080:8080 docker.io/aurimasniekis/harbor-pilot \
  serve --demo --demo-allow-public
```

`--demo-allow-public` is needed because the server inside the container listens on all interfaces. Never use it on a
shared network: the demo administrator's password is public.
:::

## What you get

Within about 15 seconds of starting, the demo sends its own synthetic traffic through the real gateway, including
rate-limited accounts and fallbacks, so every page has data:

- **Providers:** `anthropic-demo`, `openai-demo` and `local-demo`, with four accounts (two Anthropic team keys, an
  OpenAI team key and a "workstation GPU" local model).
- **Model names and fallback groups:** `coding/default`, `coding/fast`, `coding/deep`, `review/default` and
  `local/private`.
- **Routing plans:** `team-default` (paid fallback allowed) and `private-only` (local models only, with masking and
  content capture).
- **Clients:** `claude-code-laptop`, `codex-ci`, `spaceman-runs` and `private-notebook`.
- **Quota tracking** with one used-up weekly window and one failing credential, so Home has something to show.
- **Budgets:** a monthly workspace budget and a daily client budget.

<Shot name="home" alt="Demo home page showing a failing quota credential, a used-up weekly limit, 18 requests and the latest requests" caption="The demo's Home page shortly after start" />

<Shot name="requests" alt="Requests page grouped by day, with request totals, error rate, fallbacks and latency" caption="Synthetic requests, including two that needed a fallback" />

## Things to try

1. Open **Requests**, filter by **Fallbacks**, and open one. The page explains which account was rate limited and
   which one served the request.
2. Open **Routing → Team default → Test a request** and simulate a request for `claude-sonnet-4-5`.
3. Open **Limits** and see the used-up weekly window. Routing skips that account until it resets.
4. Press <kbd>Ctrl</kbd>+<kbd>K</kbd> (<kbd>⌘</kbd>+<kbd>K</kbd> on macOS) to jump anywhere.

<Shot name="palette" alt="Command palette open with the search term budget, listing budget pages and the Add a budget action" caption="The command palette" />

## How the demo is kept separate

- It uses its own database files (`demo.db`, `demo-traces.db`) and a throwaway master key (`demo-master.key`) in its
  data directory. It never reads production credentials.
- Every identity uses the reserved `example.test` domain, and every record is synthetic. The UI shows a **Demo** badge
  next to the logo.
- It refuses to listen on anything but a loopback address unless you pass `--demo-allow-public`.
- `--demo-clock 2026-11-05T12:00:00Z` freezes the clock, which makes screenshots and end-to-end tests reproducible.
