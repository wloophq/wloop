---
title: Ports and adapters, dependencies inward
description: The domain and the core import inward only; adapters implement the core's ports.
---

- **Context**: use cases that shell out or speak HTTP cannot be tested without the tools, and every new tool edits them.
- **Decision**: the domain imports only itself; the core imports the domain, its ports and the meter; adapters implement the ports; one composition root picks them.
- **Consequence**: the core runs in tests with no network and no process; a new agent, manager or sink is an adapter and a registry entry.
- **Enforced by**: `TestArchitecture`, `TestCoreImports`, `TestDomainImports`, `TestDaemonImports`, `TestReadModelImports`. See [Layers](/architecture/layers/).
