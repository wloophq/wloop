---
title: Decision log
description: The architecture constraints in force, each with its context, its consequence and the test that keeps it.
---

An entry records a constraint that future code must obey, that someone could break without knowing, and that a test or an `internal/archtest` rule keeps. A choice of format, library or behavior is no entry: its reasons belong in the pull request that made it, and the behavior in the concepts, guides and reference pages that describe it. When in doubt, write no entry.

Each entry is a file under `docs/src/content/docs/architecture/decisions/`, with a `title`, a one-sentence `description`, and the lines **Context**, **Decision**, **Consequence** and **Enforced by**, which names the tests that keep it (`TestEveryDecisionNamesATestThatExists`). A pull request that changes a constraint changes its entry. `make docs-ref` writes the table below from the entries.

| Decision | What it holds |
|---|---|
| [A draft is what its proposer declares](/architecture/decisions/a-draft-is-what-its-proposer-declares/) | A draft carries its purpose, delivery and effects; nothing follows from its kind, and a comment waits for a person. |
| [A provider is asked what it can do](/architecture/decisions/a-provider-is-asked-what-it-can-do/) | A plugin declares its capabilities, and a provider id appears only in its plugin and the tables. |
| [A verb a person runs is an event](/architecture/decisions/a-verb-a-person-runs-is-an-event/) | Every verb a person runs on an entry records one verb event, and a verb a workflow runs records none. |
| [Architecture rules are tests](/architecture/decisions/architecture-rules-are-tests/) | Each architecture rule is a test with a self-test, and its reference page is generated. |
| [Authority comes from you](/architecture/decisions/authority-comes-from-you/) | Endorsing, approving, taking over and removing come only from your surfaces, never from an agent. |
| [Behavior lives in the profile](/architecture/decisions/behavior-lives-in-the-profile/) | Behavior is profile files; Go provides a closed vocabulary and never enumerates intents. |
| [Clients are thin and speak one versioned API](/architecture/decisions/clients-are-thin-and-speak-one-versioned-api/) | The daemon computes what clients show; clients speak internal/api and decide nothing. |
| [Every spawn is metered](/architecture/decisions/every-spawn-is-metered/) | A package that spawns a process imports the meter, which counts each boundary call. |
| [One adapter reaches each outside tool](/architecture/decisions/one-adapter-reaches-each-outside-tool/) | Each outside tool is reached by one adapter, and its name stays there and in the root. |
| [One brand package](/architecture/decisions/one-brand-package/) | Every user-visible name derives from internal/brand. |
| [One daemon writes the store](/architecture/decisions/one-daemon-writes-the-store/) | Only the daemon, and the store commands under its lock, write the store. |
| [One engine plans automatic work, and planning writes nothing](/architecture/decisions/one-engine-plans-automatic-work-and-planning-writes-nothing/) | Workflows are the only planner of automatic work, and evaluating a plan writes nothing. |
| [One grammar for the profile language](/architecture/decisions/one-grammar-for-the-profile-language/) | Every reserved key of the profile and the configuration is a row of builtin.Grammar. |
| [Ports and adapters, dependencies inward](/architecture/decisions/ports-and-adapters-dependencies-inward/) | The domain and the core import inward only; adapters implement the core's ports. |
| [Small ports owned by their consumer](/architecture/decisions/small-ports-owned-by-their-consumer/) | A port belongs to the core package that uses it and has at most five methods. |
| [Sources are compiled-in plugins](/architecture/decisions/sources-are-compiled-in-plugins/) | A source is a compiled-in plugin; the core imports no plugin and no plugin another. |
| [Text an agent reads resolves through the profile](/architecture/decisions/text-an-agent-reads-resolves-through-the-profile/) | Every text an agent reads is a profile file a user profile can replace. |
| [The read model lives in memory](/architecture/decisions/the-read-model-lives-in-memory/) | Every write notes the change log the in-memory read model rebuilds from. |
| [The store is the truth](/architecture/decisions/the-store-is-the-truth/) | Managers and boards receive a projection of the store and are never read back as state. |
| [The store migrates forward](/architecture/decisions/the-store-migrates-forward/) | Every schema change is a new version with a migration, and a newer store is refused. |
| [Units generalize the pull request](/architecture/decisions/units-generalize-the-pull-request/) | Work is keyed by unit.Unit, and only a provider's codec mints and parses a key. |
| [What the loop proposes to post is a draft](/architecture/decisions/what-the-loop-proposes-to-post-is-a-draft/) | What the loop proposes to post is a draft, published once and only when approved. |
