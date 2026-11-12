---
title: A record's verdicts are its plugins'
description: The verdicts a turn's record may give come from the draft kinds of the plugins its intent serves; the profile names which conclude.
---

- **Context**: the record, the MCP schema and the board named GitHub's three review verdicts, so a third plugin's verdicts were refused at record time.
- **Decision**: a draft kind's `verdict` points at its payload's verdict field, a required string enum. An intent whose outcome carries a verdict takes the enum values of the verdict fields of its plugins' kinds; the MCP schema takes any string and the record check refuses another. The verdicts that end the work, which the board shows done, are the intent's `[record] concludes`.
- **Consequence**: a plugin adds verdicts through its manifest, and a profile decides what concludes.
- **Enforced by**: `TestAVerdictIsOneItsPluginsDeclare`, `TestVerdictsAreTheServedPluginsKindsVerdicts`, `TestAReviewIsDoneOnTheVerdictsItsProfileConcludesOn`.
