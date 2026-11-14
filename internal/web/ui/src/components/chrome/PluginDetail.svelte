<script lang="ts">
  import { providerName } from "../../lib/catalog";
  import { t } from "../../lib/i18n.svelte";
  import { reasonText, rowByKey } from "../../lib/lens";
  import { labelOf, safeURL, unitKey, unitOf, type J } from "../../lib/util";
  import ActionButtons from "../actions/ActionButtons.svelte";
  import { waitingLabel } from "./facts";

  // A unit's detail as a plugin view shows it: its title, label and service link, whom it waits on, why, and its
  // actions.
  let { unit }: { unit: J } = $props();
  const r = $derived(rowByKey(unitKey(unit)));
  const why = $derived(r ? reasonText(r) : "");
</script>

{#if !r}
  <p class="empty">{t("{unit} is not in the rows.", { unit: unit.key })}</p>
{:else}
  <div class="plugin-detail">
    <h2>{r.title || labelOf(r)}</h2>
    <div class="meta">
      <span class="ref">{labelOf(r)}</span>
      <a href={safeURL(r.link)} target="_blank" rel="noopener noreferrer" class="ext">{providerName(unitOf(r).provider)}</a>
    </div>
    <div>{t("Waiting on")} <strong>{waitingLabel(r)}</strong></div>
    {#if why}<div class="why">{why}</div>{/if}
    <div class="d-actions"><ActionButtons subject={r} surface="detail" scope="plugin-detail" /></div>
  </div>
{/if}
