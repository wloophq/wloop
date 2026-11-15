<script lang="ts">
  import { fmtNumber, t } from "../../lib/i18n.svelte";
  import type { J } from "../../lib/util";
  import { ariaKey, servedKey } from "../../state/keycaps.svelte";
  import { pickTab, selectedTab, shownTabs, stepTab, TAB_KEYS } from "./task";
  import TaskAsks from "./TaskAsks.svelte";
  import TaskBrief from "./TaskBrief.svelte";
  import TaskRelated from "./TaskRelated.svelte";
  import TaskRuns from "./TaskRuns.svelte";
  import TaskTimeline from "./TaskTimeline.svelte";

  // The task's tabs with something in them, the one picked and its panel; the strip is a tablist the arrow keys
  // move along.
  let { e }: { e: J } = $props();

  const shown = $derived(shownTabs(e));
  const selected = $derived(selectedTab(e));
  const steps = $derived([servedKey("tab:prev"), servedKey("tab:next")]);
  const hint = $derived(shown.length > 1 && steps.every(Boolean));

  function onkeydown(ev: KeyboardEvent) {
    if (!TAB_KEYS[ev.key]) return;
    ev.preventDefault();
    stepTab(e, TAB_KEYS[ev.key], true);
  }
</script>

<div class="d-tabs">
  <div class="tabs" role="tablist" tabindex="-1" aria-label={t("Sections")} aria-keyshortcuts={hint ? steps.map(ariaKey).join(" ") : undefined} {onkeydown}>
    {#each shown as x (x.id)}
      {@const on = x.id === selected}
      {@const n = x.count(e)}
      <button type="button" role="tab" class={"tab" + (on ? " on" : "")} id={"task-tab-" + x.id} aria-selected={on ? "true" : "false"} aria-controls="task-tabpanel" tabindex={on ? 0 : -1} data-focus={"task:tab:" + x.id} onclick={() => pickTab(x.id)}>{x.label()}{#if n}{" "}<span class="n">{fmtNumber(n)}</span>{/if}</button>
    {/each}
    {#if hint}<span class="keyhint" aria-hidden="true"><kbd>{steps[0]}</kbd>{" "}<kbd>{steps[1]}</kbd></span>{/if}
  </div>
  <div class="tabpanel" role="tabpanel" id="task-tabpanel" aria-labelledby={"task-tab-" + selected}>
    {#if selected === "brief"}<TaskBrief {e} />
    {:else if selected === "timeline"}<TaskTimeline {e} />
    {:else if selected === "asks"}<TaskAsks {e} />
    {:else if selected === "related"}<TaskRelated {e} />
    {:else if selected === "runs"}<TaskRuns {e} />
    {/if}
  </div>
</div>
