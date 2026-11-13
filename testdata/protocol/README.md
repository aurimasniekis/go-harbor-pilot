# Protocol fixtures

Synthetic request and response bodies for the protocol adapters. Each one is
written by hand from the provider's public API documentation. None is a
captured request, and none contains a real prompt.

| File | Shape it covers |
|---|---|
| `anthropic/messages_system_mid_conversation.json` | Messages API: a `system`-role entry with string content, appended after the last user turn |
| `anthropic/messages_system_before_assistant.json` | Messages API: a `system`-role entry with text blocks, followed by an assistant turn |
| `anthropic/messages_system_settings.json` | Messages API: a turn-scoped `system` entry (`clear_at`) followed by a settings-only entry (empty `content` plus `output_config`) |

## Reasoning (`reasoning/`)

Request bodies with reasoning settings, one per source protocol and shape. `internal/executor/httpapi` builds each one for every HTTP provider type and compares the upstream body and losses byte for byte with `reasoning/golden/<name>.golden.json` (`TestReasoningGoldens`; regenerate with `-update` and review the diff). The Claude Code shapes follow `docs/contracts/harness-wire-facts.md` §1.6.

| File | Shape it covers |
|---|---|
| `reasoning/anthropic_adaptive_effort.json` | Messages API, Claude Code style: `thinking:{type:adaptive,display}`, top-level `output_config.effort`, `context_management`, tools, streaming |
| `reasoning/anthropic_adaptive_max.json` | Messages API: adaptive thinking with `output_config.effort: max` |
| `reasoning/anthropic_effort_only.json` | Messages API: `output_config.effort` without `thinking` |
| `reasoning/anthropic_output_config_format.json` | Messages API: `output_config` with `effort` and a JSON schema `format` |
| `reasoning/anthropic_enabled_budget.json` | Messages API: `thinking:{type:enabled,budget_tokens,display}` and `context_management` |
| `reasoning/anthropic_disabled.json` | Messages API: `thinking:{type:disabled}` |
| `reasoning/anthropic_thinking_history.json` | Messages API: a signed `thinking` block before a tool call in the history |
| `reasoning/chat_effort_high.json` | Chat Completions: `reasoning_effort: high` |
| `reasoning/chat_effort_minimal.json` | Chat Completions: `reasoning_effort: minimal`, no output limit |
| `reasoning/chat_reasoning_content_history.json` | Chat Completions: an assistant turn with `reasoning_content` |
| `reasoning/responses_effort_summary.json` | Responses: `reasoning:{effort,summary}` with encrypted reasoning included |
| `reasoning/gemini_thinking_budget.json` | Gemini: `thinkingConfig` with a budget and `includeThoughts` |
| `reasoning/gemini_thinking_dynamic.json` | Gemini: `thinkingBudget: -1` (dynamic) |
| `reasoning/gemini_thinking_off.json` | Gemini: `thinkingBudget: 0` |
