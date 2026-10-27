---
title: What the loop proposes to post is a draft
description: What the loop proposes to post is a draft, published once and only when approved.
---

- **Context**: a post under your name cannot be taken back, and a retry after a crash can post twice.
- **Decision**: what {{name}} proposes to post is a draft in the Outbox; only an approved draft reaches the plugin's publisher, once, with no automatic retry, and `Verify` settles a draft a crash left in `sending`.
- **Consequence**: approval policies decide which drafts go out unattended; the loop never hands an agent a source's token.
- **Enforced by**: `TestOnlyApprovedDraftsArePublished`, `TestFailedDraftIsNeverRetriedAutomatically`, `TestRecoverNeverRepublishes`. See [The Outbox](/concepts/approval/#the-outbox).
