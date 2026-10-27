---
title: Behavior lives in the profile
description: Behavior is profile files; Go provides a closed vocabulary and never enumerates intents.
---

- **Context**: behavior and text in Go cannot be changed by the person who uses {{name}}, in their language or their way of working.
- **Decision**: intents, workflows and judgment blocks are profile files, and a user profile overrides any bundled one; Go provides a closed vocabulary of facts, steps, roles, effects, builtins and gates, and never enumerates intents.
- **Consequence**: a new behavior is a profile change; Go grows only by a fact, a step or a gate.
- **Enforced by**: `TestNoIntentEnumerations`, `TestProseIsEmbeddedOnlyFromProfiles`, the boundary rules, `TestBuiltinNamesLiveInTheirTables`. See [Profile first](/architecture/profile-first/).
