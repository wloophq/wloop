---
title: Only the gates service resolves a gate
description: Every decision waiting on you is a gate, closed only by you, a trusted policy rule or a passed window.
---

- **Context**: a decision waiting on you lived in three places (drafts, plan items, Inbox messages), each with its own state and its own way to be closed, and an agent's session can call every client route.
- **Decision**: every decision waiting on you is a gate, and only the gates service closes one: by you, through a client; by a rule of a `[gates.approval]` table the service's trusted policy holds now; or by the rule `window`, to its default, once its deadline passed. A resolution by an agent or a plugin is refused, and a request that carries a session handle resolves as the agent. A gate's status comes from its intent's declaration in `gates.toml`, never from the client, and resolving it applies the one consequence its kind names. No other code calls `Resolve` on a gates store or writes `gates.status`. Every gate opens through the service too, a gated verb's and a missing record's included, so its policy decides it and it takes its unit's task. A post an agent makes at once is recorded as a gate already approved, which is no resolution.
- **Consequence**: one guard holds every decision, whatever later lanes move onto gates; a lane that closes a gate goes through the service, so no route around the guard can appear unseen.
- **Enforced by**: `TestOnlyTheGatesServiceResolvesAGate`, `TestNoAgentResolvesAGate`, `TestGatePathRefusesAnAgentSession`, `TestAnIntentsStatusComesFromGatesToml`, `TestRecordSentIsNotAResolution`, `TestAGatedVerbOpensItsGateThroughTheService`. See [Gates](/concepts/approval/#gates).
