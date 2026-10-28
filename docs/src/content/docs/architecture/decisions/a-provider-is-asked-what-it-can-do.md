---
title: A provider is asked what it can do
description: A plugin declares its capabilities, and a provider id appears only in its plugin and the tables.
---

- **Context**: a branch on a provider's id outside its plugin ties the daemon, the loader and the clients to that plugin, so a new plugin needs edits all over.
- **Decision**: a plugin's module declares its capabilities (`Checkouts`, `Briefs`, `Links`, `Detail`), served to clients as `api.Plugin.Capabilities`; a unit's codec compares keys (`Equal`) names a unit by its address (`URL`, `KeyOf`) and says how a command line names one (`Target`). The default provider of a manifest without one is the profile's `defaults.toml`, and every bundled intent names its own, so a layer's default fills only the manifests that name none. Which intents get a turn skill is the manifest's `skill`. A user intent taking a bundled intent's role takes its shortcut and everything the role implies.
- **Consequence**: a provider id appears only in its plugin, the vocabulary tables, the unit package, the core-kept providers' codecs and the composition root, and an adapter spells only its own provider's id; the rest is debt listed by declaration with its count and reason, and clients name no plugin. Asking the GitHub plugin's package whether a unit is a pull request (`pr.RefOf`, `pr.Codec{}.Provider()`, the daemon's `isPullRequest`) is listed the same way.
- **Enforced by**: `TestTheCoreNamesNoProvider`, `TestProviderIdentityThroughAPluginPackageOnlyShrinks`, `TestClientsNameNoPlugin`, `TestCockpitScriptsNameNoPlugin`.
