---
title: Authority comes from you
description: Endorsing, approving, taking over and removing come only from your surfaces, never from an agent.
---

- **Context**: an agent reads text written by others and can be steered by it.
- **Decision**: agents report facts; endorsing a verdict, approving, taking over and removing a workspace come only from your surfaces. A session is identified by a header its manager fills, never by a tool argument.
- **Consequence**: an agent cannot endorse its own work through its channel.
- **Enforced by**: `TestAgentChannelsNeverEndorse`, `TestADraftNotificationNeverApproves`, `TestAgentAPIRefusesAHeaderThatIsNotAHandle`, `TestApprovalRulesReadTrustedFacts`. See [Security](/concepts/security/).
