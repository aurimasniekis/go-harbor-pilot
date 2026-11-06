# Spend

**Monitor → Spend** shows what your coding agents cost, from two directions: what the gateway estimates from the
requests it handled, and what providers say they billed. Harbor Pilot keeps every kind of cost in its own column and
**never adds different kinds together**. An estimate plus a bill plus a subscription fee is not a meaningful number, so
you will not find one here.

<Shot name="spend" alt="Spend overview with billed by providers, through Harbor Pilot, not through Harbor Pilot, subscription fees and tokens tiles, a daily cost chart stacked by account, and a breakdown table" caption="Spend overview for the last 30 days" />

Pick the range at the top right: **24h**, **7d**, **30d** or **90d**. **+ Add budget** opens the
[budget dialog](/operators/limits#budgets).

## The kinds of cost

| Kind | Where it comes from | Is it money you pay? |
|---|---|---|
| **Estimated cost** | Computed by the gateway from token counts and the prices configured on the provider | An estimate of it |
| **Billed cost** | Reported by the provider's billing API, through [quota tracking](/operators/limits#quota-tracking) | Yes, as the provider reports it |
| **Subscription value** | Subscription usage priced at public list prices, for comparison | No: not a charge or an invoice |
| **Subscription fees** | The fixed fees you entered for each [subscription](/operators/subscriptions#subscriptions-and-fees), prorated to the range | Yes, but not a usage charge |
| **Allocation** | Subscription fees split across the clients and projects that used them | A share of the fees above |
| **No cost** | No price is configured for the model | Unknown |

Subscription value is how you compare "what this Claude Max plan did for us" with "what the same tokens would have
cost on the API". It is never added to the estimated or billed figures.

## Overview

The tiles change with what Harbor Pilot knows:

| Tile | Shown | What it is |
|---|---|---|
| **Billed by providers** | When at least one account's quota tracking reports billing | What those providers charged, _including_ usage that did not go through Harbor Pilot |
| **Through Harbor Pilot** | Always (named **Estimated cost** when there is no billing data) | Estimated from gateway requests |
| **Not through Harbor Pilot** | With billing data | Billed by providers minus the gateway estimate: usually other tools or keys using the same accounts |
| **Subscription fees** | When you have entered fees | Prorated to the range |
| **Tokens** | Always | Input and output tokens |

The chart shows **Daily cost** (hourly for 24 hours, weekly for 90 days), switchable between **Cost** and **Tokens**, and
stacked by the grouping you pick for the breakdown below it: **Account**, **Client**, **Model** or **Project**. The
breakdown lists each group's requests, tokens in and out, estimated cost and share.

### Provider vs gateway

The **Provider vs gateway** section compares, per account, the input tokens a provider reported with the ones the
gateway saw. When a provider reports far more than went through Harbor Pilot, it says so plainly, for example:

> Providers reported 19.3M input tokens the gateway never saw. Usage may be bypassing Harbor Pilot.

That usually means someone is using the same API key or subscription directly. It needs an account whose
[quota tracker](/operators/limits#quota-tracking) reports usage; otherwise it says no provider reports usage for a
linked account.

## Breakdown

**Breakdown** is for questions like "which client failed most this week" or "what does each tool cost".

<Shot name="spend-breakdown" alt="Spend breakdown with request, error rate, fallback and latency tiles, a requests per day chart split into OK, errors, blocked and fallbacks, and a table grouped by client" caption="Breakdown: requests, errors, fallbacks, latency and cost by any dimension" />

- Tiles: **Requests** (with failed and blocked counts), **Error rate**, **Fallbacks** (how many switched account and how
  many changed model) and **Latency p95** (with the median and time to first token).
- The **Requests per day** chart shows **Requests**, **Tokens**, **Latency** or **Cost**, split into OK, errors,
  blocked and fallbacks.
- **By** groups the table by **Client**, **Tool**, **Model**, **Account**, **Provider**, **Plan**, **Project**,
  **Purpose** or **Session**. **More columns** adds errors, fallbacks and latency percentiles.

Click a row to open [Requests](/operators/requests) filtered to that group.

## Prices

Estimated cost uses the per-model prices on each provider (**Models and prices** in the provider dialog, see
[Providers](/operators/providers#models-and-prices)). A model without a price shows **No cost**, not $0. Every amount
carries its currency, and money is stored exactly (in 10⁻¹⁰ units), not as floating point.

## Related

- [Limits](/operators/limits): budgets that warn or block on spend.
- [Subscriptions and CLI bridges](/operators/subscriptions): entering fees.
- [Events](/operators/events): send usage events to Pulse or a webhook.
