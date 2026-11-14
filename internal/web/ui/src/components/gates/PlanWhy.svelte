<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { planWhy } from "./gates";

  // Why a plan item fired, folded.
  let { item }: { item: J } = $props();
  const why = $derived(planWhy(item));
</script>

{#if why}
  <details class="plan-why">
    <summary>{t("Why it fired")}</summary>
    <ul>{#each why as f, i (i)}<li title={f.title}>{f.text}</li>{/each}</ul>
  </details>
{/if}
