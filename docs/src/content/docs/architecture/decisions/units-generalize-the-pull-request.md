---
title: Units generalize the pull request
description: Work is keyed by unit.Unit, and only a provider's codec mints and parses a key.
---

- **Context**: a loop keyed by pull request cannot hold a Slack thread, a session or a workspace.
- **Decision**: work is keyed by `unit.Unit{Provider, Key}`; only the provider's codec mints and parses a key.
- **Consequence**: the turns, the workflows, the task core and the read model serve any provider.
- **Enforced by**: `TestLoopCodeImportsNoPR`, `TestTheCoreNamesNoProvider`.
