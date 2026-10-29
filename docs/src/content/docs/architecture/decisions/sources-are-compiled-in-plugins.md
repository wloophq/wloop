---
title: Sources are plugins
description: A source is a plugin, a process of its own that imports only the SDK; the host names it in one file, and neither side imports the other.
---

- **Context**: the core must not change per source, and a source must be buildable and replaceable without the host's packages.
- **Decision**: a source is a plugin under `plugins/<id>`, a process of its own that imports only the SDK under `pkg/` and speaks the plugin protocol; the host lists it in `internal/cli/sources.go`, the one file that names one, with the module the composition root builds for what it still runs in the daemon's process. The core imports no plugin, no plugin imports another, and no host package imports a plugin's.
- **Consequence**: a disabled plugin leaves the daemon whole; the remaining edges from the core to the GitHub plugin's packages under `internal/` are listed as debt.
- **Enforced by**: `TestCoreImportsNoPluginDomain`, `TestOnlySourcesGoNamesASource`, `TestPluginsImportOnlyTheSDK`, `TestHostImportsNoPlugin`. See [Plugins](/architecture/plugins/).
