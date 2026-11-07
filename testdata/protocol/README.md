# Protocol fixtures

Synthetic request and response bodies for the protocol adapters. Each one is
written by hand from the provider's public API documentation. None is a
captured request, and none contains a real prompt.

| File | Shape it covers |
|---|---|
| `anthropic/messages_system_mid_conversation.json` | Messages API: a `system`-role entry with string content, appended after the last user turn |
| `anthropic/messages_system_before_assistant.json` | Messages API: a `system`-role entry with text blocks, followed by an assistant turn |
| `anthropic/messages_system_settings.json` | Messages API: a turn-scoped `system` entry (`clear_at`) followed by a settings-only entry (empty `content` plus `output_config`) |
