---
title: Every write in your name is a draft
description: Every write in your name is a draft, published once and only when approved.
---

- **Context**: a post under your name cannot be taken back, and a retry after a crash can post twice.
- **Decision**: what {{name}} posts is a draft in the Outbox, held by a draft gate that holds its decision; only a draft its gate approved reaches the plugin's publisher, once, with no automatic retry, and `Verify` settles a draft a crash left in `sending`. The draft keeps only its delivery, and the sender refuses a queued draft whose gate did not approve it. A write with `draft: false` is a draft too, whose gate a session you drive approves at once.
- **Consequence**: approval policies decide which draft gates resolve unattended; the loop never hands an agent a source's token.
- **Enforced by**: `TestOutboxSendsOnlyApprovedDrafts`, `TestOnlyApprovedDraftsArePublished`, `TestFailedDraftIsNeverRetriedAutomatically`, `TestRecoverNeverRepublishes`, `TestDraftFalsePostsOnlyFromASessionTheUserDrives`. See [The Outbox](/concepts/approval/#the-outbox).
