---
title: A kind that takes a basis is read by its plugin
description: A plugin whose draft kinds take a basis serves Drafter, or the host refuses to start it.
---

- **Context**: a draft kind with `basis = "agent"` is checked against the live value its plugin reads through `Drafter`; a plugin that declares such a kind and serves no `Drafter` answers an empty basis, and every draft of the kind reads stale.
- **Decision**: when the host starts a plugin, it asks its capabilities and refuses one whose manifest declares a kind with a basis while it serves no `Drafter`. The kind is not demoted to one with no basis: a basis guards against posting on a head the agent did not read, and dropping it silently would post stale.
- **Consequence**: a manifest and its binary that disagree fail at start, with the kinds named, rather than at every send.
- **Enforced by**: `TestAPluginWhoseKindsTakeABasisMustReadIt`.
