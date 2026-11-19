<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import DraftCard from "../gates/DraftCard.svelte";
  import GateCard from "../gates/GateCard.svelte";
  import { taskGates } from "./task";

  // The decisions that wait on the task: its open gates, the draft it carries as a draft's own task, and the
  // drafts that wait on it, each a card.
  let { e }: { e: J } = $props();

  const g = $derived(taskGates(e));
</script>

{#if g}
  <section class="d-gates" aria-label={t("Open gates")}>
    <h2 class="d-sub">{g.heading}</h2>
    {#each g.gates as gate (gate.id)}<GateCard task={e} {gate} />{/each}
    {#if g.own}
      <DraftCard task={e} draft={g.own} scope="detail" />
    {/if}
    {#each g.drafts as d (d.id)}<DraftCard task={e} draft={d} scope="detail" />{/each}
  </section>
{/if}
