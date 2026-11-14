---
title: The cockpit updates nodes; it never rebuilds them
description: A sync writes into the store in place and only the nodes bound to changed values move; no view rebuilds DOM to reflect data.
---

- **Context**: a cockpit that redraws whole panels on every sync destroys what the DOM owns, a textarea's dragged size, its caret and scroll, an open `details`, a board's scroll, and needs a helper to patch back each piece it remembers.
- **Decision**: the cockpit is Svelte components over one store. A sync reconciles the daemon's answer into the store in place, keyed by the ids the API serves, and every list is keyed by them, so only the nodes bound to a value that changed are touched. No module or component rebuilds DOM to reflect data (`replaceChildren`, `replaceWith`, `innerHTML`, `outerHTML`, `insertAdjacentHTML`, `{@html}`), markdown renders as components, and no component carries a `<style>`, so every color stays in `app.css`, where the token and contrast tests read it. Text a reload would lose lives in the store, which decides between reloading into a new build and offering to.
- **Consequence**: nothing restores focus, caret or scroll after a sync, since nothing takes them away; a list without a stable id in the API cannot be shown without rebuilding, so such a list is an API change first.
- **Enforced by**: `TestCockpitRebuildsNoDOM`. See [The daemon and its clients](/architecture/daemon-and-clients/).
