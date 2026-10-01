# Embedder operations — v1.0.0

**Status:** proposal · **Target:** v1.0.0

Pulse will often run **inside someone else's process**: a downstream library, a web service, a batch job, an MCP server. Those hosts need two things Pulse doesn't offer today:

| Part | What | Decided stance |
|---|---|---|
| [01 — Resource limits](01-resource-limits.md) | instance-level guards against runaway requests | add them, with **high defaults**. Tuning is a deliberate step, not something developers discover through blocked requests |
| [02 — Observability](02-observability.md) | logging, timing callbacks, metrics | a configurable logger; timing callbacks; metrics hooks that are available but **opt-in**; must work whether or not the host is a server |

Both are configured on `pulse.Options`, and both may also appear in a feature-profile file. A profile's `limits` and `observability` sections are **behaviour**, not features: omitted means engine defaults, unlike the profile's feature allowlist.

Context management stays the developer's responsibility (planning decision). Pulse continues to honour `context.Context` cancellation and deadlines, already checked at about 60 sites in decode and processing. The limits below are *additional* guards, not a replacement for the caller's context.
