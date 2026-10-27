---
title: One engine plans automatic work, and planning writes nothing
description: Workflows are the only planner of automatic work, and evaluating a plan writes nothing.
---

- **Context**: two automation mechanisms side by side disagree, and a plan that writes while it is read changes under the reader.
- **Decision**: workflows run by `internal/core/automation` are the only planner of automatic rounds and cleanup; evaluating the plan writes nothing, spawns nothing and reads no GitHub; irreversible steps ship in `observe` and automatic runs never force.
- **Consequence**: `{{command}} plan` is the dry run.
- **Enforced by**: `TestNothingImportsTheGate`, `TestTheEngineImportsNoProvider`, the engine's tests.
