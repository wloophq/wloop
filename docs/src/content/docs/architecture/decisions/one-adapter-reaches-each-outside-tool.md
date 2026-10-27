---
title: One adapter reaches each outside tool
description: Each outside tool is reached by one adapter, and its name stays there and in the root.
---

- **Context**: tool names and protocols scattered through the code make a second agent or manager a rewrite.
- **Decision**: only `internal/github` reaches GitHub, only the Orca and Claude Desktop managers reach their apps, only the store touches SQL, and provider names live in adapters and the root. GitHub is read in process with budgets; Orca through its socket.
- **Consequence**: a change of tool or protocol stays in one package.
- **Enforced by**: `TestProvidersStayInAdaptersAndTheRoot`, `TestOnlyOrcaAdapterReachesOrca`, `TestOnlyClaudeDesktopManagerReachesDesktop`, `TestOnlyGitHubReachesGitHub`, `TestOnlyTheStoreTouchesSQL`.
