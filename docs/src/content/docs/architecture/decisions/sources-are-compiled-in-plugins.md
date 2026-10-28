---
title: Sources are compiled-in plugins
description: A source is a compiled-in plugin; the core imports no plugin and no plugin another.
---

- **Context**: the core must not change per source, and a plugin framework costs more than the two sources need.
- **Decision**: a source is a `source.Plugin` listed in `internal/cli/sources.go`, contributing through optional `source.Module` fields; the core imports no plugin and no plugin imports another.
- **Consequence**: a disabled plugin leaves the daemon whole; the remaining edges from the core to the GitHub plugin are listed as debt.
- **Enforced by**: `TestCoreImportsNoPluginDomain`, `TestOnlySourcesGoNamesASource`. See [Plugins](/architecture/plugins/).
