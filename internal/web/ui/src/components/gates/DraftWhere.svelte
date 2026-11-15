<script lang="ts">
  import { fmtLanguage, t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";

  // Where a draft posts, as its publisher named it, the raw target marked unknown when it could not, and the
  // language its text is written in.
  let { draft }: { draft: J } = $props();
  const pending = $derived(["held", "failed", "queued", "sending"].includes(draft.state));
</script>

<div class="meta">{t("To")} {#if draft.where}<span class="ref">{draft.where}</span>{:else}<span class="ref">{draft.target || draft.label || draft.unit || ""}</span>{#if pending}{" (" + t("destination unknown") + ")"}{/if}{/if}{#if draft.language}{" · "}<span class="lang" lang={draft.language}>{fmtLanguage(draft.language)}</span>{/if}</div>
