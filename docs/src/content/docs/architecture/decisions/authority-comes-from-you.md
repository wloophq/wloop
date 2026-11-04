---
title: Authority comes from you
description: Endorsing, approving and acting for you come from your surfaces or a session you drive, never from an unattended agent.
---

- **Context**: an agent reads text written by others and can be steered by it, and every agent session already runs as you, with your `gh`, your Slack token and the daemon's socket.
- **Decision**: authority comes from you, through a person's client or an agent session you drive. A session is user-driven when the loop never typed into it, or you prompted it since the loop last did, and no turn the loop sent runs in it (`turn.Session.UserDriven`); an unattended session reports facts and never resolves a gate, endorses a verdict or runs a verb. The daemon resolves a request's handle to its session and keys the check on the session's stable id; the gates service checks it again through its own port, and refuses a session a gate whose draft or question that session's own turn produced. A session is identified by a header its manager fills, never by a tool argument.
- **Consequence**: an agent cannot endorse or approve its own work, and a worker cannot approve another's; the session you type into acts as you would, and the timeline names the session each change came through.
- **Enforced by**: `TestUserDriven`, `TestOnlyAUserDrivenSessionResolvesAGateOfEveryKind`, `TestNoUnattendedAgentResolvesAGate`, `TestAVerbFromASessionNeedsTheUserToDriveIt`, `TestTheGatePathTakesTheResolutionOfASessionTheUserDrives`, `TestAgentChannelsNeverEndorse`, `TestADraftNotificationNeverApproves`, `TestAgentAPIRefusesAHeaderThatIsNotAHandle`, `TestApprovalRulesReadTrustedFacts`. See [Security](/concepts/security/).
