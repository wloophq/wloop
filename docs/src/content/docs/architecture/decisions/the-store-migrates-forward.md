---
title: The store migrates forward
description: Every schema change is a new version with a migration, and a newer store is refused.
---

- **Context**: a user's store holds work that cannot be recreated.
- **Decision**: every schema change is a new version with a migration, run after a backup, and a store newer than the binary is refused.
- **Consequence**: `{{command}} store restore` takes a store back to a backup; a migration in a release is never edited.
- **Enforced by**: `TestSchemaSQLIsFrozenAtSchemaVersion`, `TestMigrationsAreOneVersionEachUpToSchemaVersion`, `TestEveryReleasedSchemaMigratesToSchemaSQL`, `TestNewerStores`, which fail on a schema change without a migration. See [Changing the store's schema](/architecture/development/#changing-the-stores-schema).
