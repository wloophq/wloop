---
title: Small ports owned by their consumer
description: A port belongs to the core package that uses it and has at most five methods.
---

- **Context**: a wide interface every consumer mocks whole couples them all to every capability.
- **Decision**: a port is declared by the core package that uses it, with at most five methods; a capability only some adapters have is an optional interface found by type assertion.
- **Consequence**: a manager without a board, or an agent without questions, plugs in without stubs.
- **Enforced by**: `TestArchitecture`. See [Ports and adapters](/architecture/ports/).
