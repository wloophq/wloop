---
title: Payload facts are what is posted
description: A fact a draft kind declares reads a required enum field of the payload the publisher posts verbatim, so a rule over it is trusted and an allow-list.
---

- **Context**: a rule that releases a draft by what it says (a review's event) must read the value that is posted, and an absent fact reads as zero, so a rule written as a deny-list would release a draft whose field is missing.
- **Decision**: a draft kind declares a fact with a pointer into its payload, and the loader refuses a pointer that is not required, a string and an `enum` in the kind's schema. The publisher posts the payload verbatim, and the card shows all of it. A rule over such a fact is an allow-list (`in [...]`). A payload that holds an object key twice is refused at propose and at every revision, so every reader, the card, the rules and the publisher, reads the same value.
- **Consequence**: a fact is as trusted as the payload the person can see, and an unknown or missing value never releases a draft.
- **Enforced by**: `TestFactPointerMustBeRequiredEnum`, `TestReviewSendsEventVerbatim`, `TestValidateJSONRefusesADuplicateKey`. See [The Outbox](/concepts/approval/#drafts-the-daemon-posts).
