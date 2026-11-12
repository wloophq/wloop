---
title: Agents write nothing outside
description: A session whose intent sets source_writes = false proposes drafts and writes nothing to its source; the denied commands come from the plugins' manifests.
---

- **Context**: an agent that ran `gh api --method POST` after you approved was denied by the agent's own classifier, which takes authorization only from your turns, and a deny list of GitHub commands in the core named a provider.
- **Decision**: review, address and nudge sessions set `[session] source_writes = false` and propose drafts; the daemon's publisher posts what you or a rule of yours approve. Each plugin's manifest declares the commands that write to its source (`[source.session] write_commands`), and the session policy denies their union. The core holds no list of such commands and no provider name.
- **Consequence**: a source write is made by the daemon, under a gate, and never by a command an agent runs; a new source gets the same protection by declaring its commands.
- **Enforced by**: `TestReviewAddressNudgeSourceWritesOff`, `TestArchNoManifestNamesInCore`. See [The Outbox](/concepts/approval/#drafts-the-daemon-posts).
