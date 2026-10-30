---
title: The plugins declare their vocabulary
description: The builtin tables hold the core's rows only; each plugin's facts, effects, roles and kinds come from its manifest.
---

- **Context**: the builtin tables named the GitHub and Slack plugins' facts, effects, roles and steps, so a new source needed an edit in the core.
- **Decision**: `internal/intent/builtin` declares the core's rows and imports no plugin's domain; `builtin.Merge` maps each plugin's `[source]` table onto the tables before the configuration and the profile load, for every plugin found, enabled or not.
- **Consequence**: a source plugs in with its manifest; a disabled plugin's keys still parse.
- **Enforced by**: `TestBuiltinHasNoProviderRows`.
