---
title: One brand package
description: Every user-visible name derives from internal/brand.
---

- **Context**: the product's name must change in one place.
- **Decision**: every user-visible name derives from `internal/brand`; internal names are brand-neutral (`X-Loop-*`, `LOOP_*`); the docs write the name through placeholders.
- **Consequence**: a build with another name writes nothing that names the default one.
- **Enforced by**: `TestBrandLiteralOnlyInBrand`, `make test-brand`, `make scan-brand`.
