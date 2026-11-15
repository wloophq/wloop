<script lang="ts">
  import { t } from "../../lib/i18n.svelte";
  import { unitKey } from "../../lib/util";
  import { detailState } from "../../state/store.svelte";
  import UnitCard from "../detail/UnitCard.svelte";
  import { cardOf, closeDetail } from "./task";
  import TaskComposer from "./TaskComposer.svelte";
  import TaskGates from "./TaskGates.svelte";
  import TaskHead from "./TaskHead.svelte";
  import TaskProps from "./TaskProps.svelte";
  import TaskTabs from "./TaskTabs.svelte";
  import TaskVerbs from "./TaskVerbs.svelte";

  // The inside of the detail for the task it shows: its head, facts, units, the decisions that wait on it, its
  // verbs, its tabs and the composer; while it loads, or when it cannot, a note under the close button.
  const e = $derived(detailState.task && detailState.task.id === detailState.id ? detailState.task : null);
  const cards = $derived(e ? (e.units || []).map((u: { provider: string; key: string }) => cardOf(e, u)).filter(Boolean) : []);
</script>

{#if e}
  <TaskHead {e} />
  <TaskProps {e} />
  {#if cards.length}<div class="d-links">{#each cards as c (unitKey(c.card.unit))}<UnitCard s={c} signals={false} />{/each}</div>{/if}
  <TaskGates {e} />
  <TaskVerbs {e} />
  <TaskTabs {e} />
  <TaskComposer {e} />
{:else}
  <div class="d-head">
    <button type="button" class="close" aria-label={t("Close detail")} data-focus="detail:close" onclick={closeDetail}>×</button>
    <p class={detailState.error ? "plan-error" : "empty"}>{detailState.error ? t("Could not load the task: {error}", { error: detailState.error }) : t("Loading the task.")}</p>
  </div>
{/if}
