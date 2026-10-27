---
title: Text an agent reads resolves through the profile
description: Every text an agent reads is a profile file a user profile can replace.
---

- **Context**: a text fixed in Go can only be changed by a rebuild, and a user who wants the record step or the headless prompt in their own words has nowhere to write them.
- **Decision**: the turn partials live in `profiles/default/partials/` and the prompts of {{name}}'s own calls in `profiles/default/prompts/`; a file of the user's profile replaces the bundled file of its path. The descriptions of the MCP tools and their parameters, and what each tool answers, live in `profiles/default/tools.toml`, by tool, parameter path and answer name; Go hands an answer the fields `builtin.ToolAnswers` lists for it, and a user's `tools.toml` replaces the keys it sets. Go keeps the markers, the schemas and the limits around them, and checks that a record partial still states the record protocol.
- **Consequence**: a broken partial or wrapper of the user's falls back to the bundled one with a problem, so a profile edit never stops a turn from ending; the rendered turns of the bundled profile are unchanged.
- **Enforced by**: `TestProseIsEmbeddedOnlyFromProfiles`, `TestARecordPartialMissingTheProtocolLoadsTheBundledOne`, `TestTheBundledWrapperWritesTheOldPrompt`, `TestEveryToolIsDescribedByTheBundledProfile`, `TestAToolTextFailsAtLoad`, `TestAnswersComeFromTheProfile`, the scenario goldens. See [Profile first](/architecture/profile-first/).
