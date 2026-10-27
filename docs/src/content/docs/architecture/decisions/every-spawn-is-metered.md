---
title: Every spawn is metered
description: A package that spawns a process imports the meter, which counts each boundary call.
---

- **Context**: spawning `gh` or `orca` on a hot path made the daemon slow and the cost invisible.
- **Decision**: a package that spawns a process imports `internal/meter`, which counts each boundary call by port; GitHub and Orca are reached in process or over a socket.
- **Consequence**: `{{command}} doctor --load` shows what each job and route costs; budget tests assert it.
- **Enforced by**: `TestSpawningPackagesImportMeter`, `TestMeterIsALeaf`.
