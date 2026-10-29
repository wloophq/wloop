---
title: A gate's consequence runs once
description: A resolved gate records when its consequence ran; the tick reruns one a stopped daemon left, and every consequence is idempotent.
---

- **Context**: a gate's resolution is written before its consequence runs, and no transaction spans the gates and the ledger, the outbox or the turns, so a daemon that stopped in between lost the consequence: an approved proposal read accepted with its operation never applied.
- **Decision**: a resolved gate records when its consequence ran (`gates.applied_at`), whether it succeeded or failed, unless its context ended first. The gates' tick, which runs at the daemon's start and after it, runs the consequence of every resolved gate without the mark that no call runs now. Every consequence runs once for its gate: the outbox queues only a held draft, the ledger records the proposals a decision was applied to (`item_proposals.decided_at`) in the decision's own commit, and the turns store an answer once per gate.
- **Consequence**: a new gate kind's consequence must be idempotent for its gate; a consequence that failed is a person's to retry, never the tick's.
- **Enforced by**: `TestAConsequenceACrashInterruptedRunsAtTheNextTick`, `TestTheTickLeavesAConsequenceInFlight`, `TestAFailedConsequenceIsNotRetriedByTheTick`, `TestADraftApprovedBeforeACrashIsSentOnceAfterIt`, `TestAProposalDecidedBeforeACrashAppliesOnce`, `TestAQuestionAnsweredBeforeACrashReachesItsSessionOnce`, `TestADecisionAppliesOnce`, `TestAnAnswerToTheSameGateIsDeliveredOnce`. See [Gates](/concepts/approval/#gates).
