---
title: Translate the interface
description: Pick the interface language, correct a translation and add a language to your profile.
---

The cockpit, the menu bar, notifications and the terminal client show the profile's texts in one language. The command line and `--help` stay in English.

## Pick the language

By default the daemon follows the system's preferred languages (macOS's language list, else `LANGUAGE`, `LC_ALL`, `LC_MESSAGES` or `LANG`) and picks the nearest language the profile holds texts in: `pt-PT` reads the `pt-BR` texts, and a language no layer translates reads English. To choose, set it in `config.toml`:

```toml
[ui]
locale = "pt-BR"
```

The change applies without a restart. A language the profile holds no texts in, such as `fr`, shows English texts and still writes dates and numbers the French way.

## Correct a translation

A translation is a file of a layer under `locales/<tag>/`, mirroring the file it translates, with only the texts. To change one text, write it in your profile's mirror:

```toml
# <profile>/locales/pt-BR/actions.toml
[pause]
label = "Segurar"
```

A plugin's texts live in its layer's `ui.toml`; your translation of them goes in `<profile>/plugins/<id>/locales/<tag>/ui.toml`. Run `{{command}} profile check` to see the problems: a key that names no text, a typo such as `pause.lable`, or a translation that uses a field its English text does not.

## Rewrite a text

A text you rewrite in the file itself (`<profile>/actions.toml`, `label = "Hold"`) reads as you wrote it in every language, since a translation of the old text no longer fits it. Add your own translation beside it in `locales/<tag>/` to read it in another language too. A copied file that keeps a text as the bundled one has it still reads the bundled translation.

## Add a language

Create `<profile>/locales/<tag>/` with a BCP 47 tag, such as `es` or `de-AT`, and mirror the files you translate: `actions.toml`, `views.toml`, `tasks.toml`, `gates.toml`, `keys.toml`, `notices.toml`, `strings.toml`, each workflow's `workflow.toml` and `text.md.tmpl`, and each intent's `intent.toml`. Every text you leave out shows in English. The [language reference](/reference/language/) marks each key that is a text with "localized".

Where a count meets a noun, write the plural with `plural`, whose forms follow the language's rules (`one`, `other`, and `zero`, `two`, `few` or `many` where the language has them):

```toml
questions_title = "{{.n}} {{plural .n \"one\" \"pergunta aberta\" \"other\" \"perguntas abertas\"}}"
```
