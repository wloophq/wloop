---
title: The store is the truth
description: Managers and boards receive a projection of the store and are never read back as state.
---

- **Context**: workspaces and sessions are deleted and recycled; a board can be moved by hand.
- **Decision**: turn state, questions, answers and history live in the store. Workspace managers and boards are sync targets: they receive a projection, fail alone and are reconciled on start; {{name}} never reads a card's column back as state.
- **Consequence**: any workspace or session can be recreated from stored context.
- **Enforced by**: `TestAMoveBetweenManagedColumnsIsReasserted`, `TestAFailedReconcileIsRedone`. See [Workspaces and board columns](/concepts/board/).
