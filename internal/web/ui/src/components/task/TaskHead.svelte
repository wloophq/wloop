<script lang="ts">
import { stateLabel } from "../../state/verbs";
  import { fmtDate, fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { providerName, starred } from "../../lib/catalog";
  import { safeURL, type J } from "../../lib/util";
  import { unitRefHref } from "../detail/unit";
  import DetailWindowControls from "../window/DetailWindowControls.svelte";
  import { cardOf, closeDetail, titleWithoutRef, unitLabels } from "./task";
  import TaskWhen from "./TaskWhen.svelte";

  // The task's units, each opening in the cockpit and at its source, its title, state, whom it waits on and since
  // when, why, and its notes.
  let { e }: { e: J } = $props();

  const labels = $derived(unitLabels(e));
  const drafts = $derived((e.drafts || []).length);
</script>

<header class="d-head">
  <button type="button" class="close" aria-label={t("Close detail")} data-focus="detail:close" onclick={closeDetail}>×</button>
  <DetailWindowControls />
  {#if labels.length}
    <div class="d-units meta">{#each e.units || [] as u, i (u.provider + ":" + u.key)}{@const link = (cardOf(e, u) || { card: {} }).card.link || ""}<span class="d-unit"><a href={unitRefHref(u)} class="ref">{labels[i]}</a>{#if link}{" "}<a href={safeURL(link)} target="_blank" rel="noopener noreferrer" class="ext">{providerName(u.provider)}</a>{/if}</span>{/each}</div>
  {/if}
  <h1>{#if starred(e)}<span class="star">{"★ "}</span>{/if}{titleWithoutRef(e.title || e.id, labels)}</h1>
  <div class="meta"><span class="pill state" data-category={e.category || ""}>{stateLabel(e.state)}</span>{#if e.changed}{" "}<span class="flag">{t("changed")}</span>{/if}{#if e.owed_by}{" "}{t("owed by")}{" "}<strong>{e.owed_by}</strong>{/if}{#if e.wake && e.wake.until}{" "}{t("wakes {time}", { time: fmtDate(e.wake.until, "datetime") })}{/if}{#if drafts}{" "}<span class="draft-mark" role="img" title={tn("{n} draft waits for you", drafts)} aria-label={tn("{n} draft waits for you", drafts)}>{fmtNumber(drafts)}</span>{/if}</div>
  <TaskWhen {e} />
  {#if e.reason}<p class="why">{e.reason}</p>{/if}
  {#if e.notes}<p class="d-notes">{e.notes}</p>{/if}
</header>
