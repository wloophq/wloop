---
title: The read model lives in memory
description: Every write notes the change log the in-memory read model rebuilds from.
---

- **Context**: deriving rows per request costs a query per row on every refresh.
- **Decision**: writes note an in-memory change log; the read model rebuilds rows from one store snapshot on change and serves deltas that clients pull after an event.
- **Consequence**: serving rows costs no SQL; a restart starts a new epoch and clients reload.
- **Enforced by**: `TestEveryWriteNotes`, `TestEveryExportedMethodIsReadOrNoted`, the projection tests.
