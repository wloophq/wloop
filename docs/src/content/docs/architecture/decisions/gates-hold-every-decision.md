---
title: Gates hold every decision
description: A decision waiting on you is a gate; no rule that releases one reads an untrusted fact, and no unattended agent resolves one.
---

- **Context**: a send, a run or an answer decided by a rule or by an agent would let a thread's or an agent's text act under your name: an agent writes task properties, a classification's output and a brief's extra fields, and a thread's author writes what the plugin reads.
- **Decision**: every decision waiting on you (a draft, a run waiting for approval or trust, a question, an agent's post, a proposal, a gated step) is a gate, and drafts, plan items, messages and proposals keep only their own lifecycles. A `[gates.approval]` rule that releases a gate (a draft gate's rule of any mode, any other gate's of mode `send`, `run` or `delay`) fails to load when it reads a fact the facts table marks untrusted, a brief's extra field, a classification's output or a property an agent or a plugin writes, in an intent's or a workflow's manifest and in `config.toml`. A resolution by a plugin is refused, and so is one by an agent unless it comes through a session you drive, as [Authority comes from you](/architecture/decisions/authority-comes-from-you/) says.
- **Consequence**: what an agent writes can inform you and order your lists, never release a post or a run; a new fact, property or record kind that an agent or a plugin writes is untrusted from its first use, and a rule that needs it waits for you.
- **Enforced by**: `TestApprovalRulesReadTrustedFacts`, `TestNoUnattendedAgentResolvesAGate`. See [Gates](/concepts/approval/#gates).
