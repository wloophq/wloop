---
title: Architecture rules are tests
description: Each architecture rule is a test with a self-test, and its reference page is generated.
---

- **Context**: a rule only in prose is broken by the next change that does not read it.
- **Decision**: each rule is a test in `internal/archtest` with a self-test that plants a violation and a doc comment that states the rule; allowlists name their reason and fail when stale. The rules reference page is generated from those sources.
- **Consequence**: exceptions are visible and only shrink, and the list of rules cannot drift from the tests.
- **Enforced by**: the self-tests, `TestEveryRuleTestStatesItsRule`, `TestRulesDocIsCurrent`. See [Testing](/architecture/testing/#the-architecture-tests) and [Architecture rules](/reference/architecture-rules/).
