---
title: Only the window adapter imports Wails
description: The native window is one adapter behind the window client's port, and only it imports Wails.
---

- **Context**: the cockpit's native window and omnibox panel are drawn by Wails on macOS, built without cgo through its purego backend. Wails is a large framework, and a fallback host written on AppKit directly must be able to replace it.
- **Decision**: `internal/client/window` decides when and where the window and the panel show and declares the `Host` port. `internal/client/window/wails` implements it and is the only package that imports Wails, including its test files and the files a build tag leaves out. The composition root picks the host. The darwin release build passes the `purego` tag. Linux builds do not, and they keep a stub that reports no window.
- **Consequence**: swapping the host, or upgrading Wails, changes one package. The browser keeps working wherever no host runs.
- **Enforced by**: `TestOnlyTheWindowAdapterImportsWails`, `TestReleaseBuildsTheBinaryWithTheWindow`.
