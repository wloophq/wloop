---
title: Clients are thin and speak one versioned API
description: The daemon computes what clients show; clients speak internal/api and decide nothing.
---

- **Context**: a rule computed in a client drifts from the same rule in another.
- **Decision**: the daemon computes rows, actions, facets, counters and notifications; clients parse, call one operation and render. `internal/api` is the only schema, versioned by its major.
- **Consequence**: the CLI, the terminal client, the menu bar and the cockpit show the same thing; a new surface is rendering only.
- **Enforced by**: `TestArchitecture`, `TestCockpitScriptsNameNoPlugin`; the wire goldens.
