---
title: One daemon writes the store
description: Only the daemon, and the store commands under its lock, write the store.
---

- **Context**: several processes writing one SQLite file need two lock disciplines and still race, and a lock held across a network call stalls every other writer.
- **Decision**: only the daemon opens the store as its writer, and `store migrate` and `store restore` under the store's lock with the daemon stopped. Other processes go through the daemon's API, or read the store with `sqlite.OpenReadOnly` when the daemon is down.
- **Consequence**: one transaction is the unit of consistency; clients fail when the daemon is down instead of writing around it.
- **Enforced by**: `TestOnlyServeWritesTheStore`, `TestOnlyTheStoreTouchesSQL`. See [The daemon and its clients](/architecture/daemon-and-clients/).
