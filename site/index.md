---
layout: home

hero:
  name: Harbor Pilot
  text: One gateway for every coding agent
  tagline: >-
    Point Claude Code, Codex, OpenCode or any SDK at one URL. Harbor Pilot routes each request to your subscriptions,
    API keys or local models by policy, keeps quotas and budgets honest, masks secrets on the way out, and shows you
    why every request went where it did.
  image:
    src: /logo.svg
    alt: Harbor Pilot
  actions:
    - theme: brand
      text: Get started
      link: /guide/what-is-harbor-pilot
    - theme: alt
      text: Try the demo
      link: /guide/demo
    - theme: alt
      text: Connect a tool
      link: /tools/

features:
  - title: One URL for every tool
    details: >-
      Claude Code, Codex, OpenCode, Pi and any OpenAI, Anthropic or Gemini SDK connect with a token and a base URL.
      Harbor Pilot speaks all four wire protocols and translates between them where a model needs it.
  - title: Subscriptions, API keys and local models
    details: >-
      Route to metered API keys, to Claude and ChatGPT subscriptions through their own official CLIs, or to Ollama,
      vLLM and LM Studio. Accounts fail over to each other before the first byte reaches your tool.
  - title: Plans you can read
    details: >-
      Routing plans map model names to ordered fallback groups, with hard restrictions, rules and decision tables.
      Drafts are checked before publishing, including a warning when a version would block most traffic.
  - title: "Why this route?"
    details: >-
      Every request has a page that says, in one sentence, where it went and why: which rule matched, which accounts
      were skipped and why, every attempt, and what it cost.
  - title: Limits that tell the truth
    details: >-
      Quota tracking for subscriptions and providers, budgets enforced inside the same transaction that reserves a
      request, and spend kept in separate categories: estimates, provider bills and subscription fees are never added up.
  - title: Secrets masked on the way out
    details: >-
      Credentials and personal data in prompts are replaced with placeholders before a provider sees them, and put
      back in the reply. Short-lived captured content is encrypted per day and erased when it expires.
  - title: Alerts and events
    details: >-
      Telegram, Slack, Discord and webhook alerts for used-up quotas, failing credentials and budgets, plus a durable
      event stream to Pulse (NATS JetStream) and signed webhooks.
  - title: One binary, one database
    details: >-
      A single Go binary with the web UI embedded and SQLite storage. Online backups, verified restore, a Terraform
      provider, Prometheus metrics and container images with the CLI bridges included.
---

<div class="hp-home">

## See it

<Shot name="home" alt="Home page with two things that need attention, request and cost figures, the limits closest to running out and the latest requests" caption="Home: what needs attention first, then the numbers" />

<div class="hp-gallery">

<Shot name="request" alt="A request page explaining that the request was served by a backup account after the first one was rate limited" caption="Why this route: one sentence, then the route, attempts and candidates" />

<Shot name="plan" alt="A routing plan described in plain words, with its flow from model names to fallback groups" caption="Routing plans in plain words" />

<Shot name="limits" alt="Limits page with accounts, their tightest quota windows, untracked accounts and budgets" caption="Quotas and budgets in one place" />

<Shot name="spend" alt="Spend page with estimated cost, billed cost and tokens charted over time" caption="Spend, with every cost category kept separate" />

<Shot name="client" alt="A client page with its recent activity, access tokens and setup steps" caption="Clients: activity, tokens and setup together" />

<Shot name="privacy" alt="Masking policies with what each one masks and which plans use it" caption="Masking policies" />

</div>

</div>
