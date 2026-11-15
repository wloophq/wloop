<script lang="ts">
  import { t, tn } from "../../lib/i18n.svelte";
  import { personName } from "../../lib/tasks";
  import type { J } from "../../lib/util";
  import { viewHash } from "../../state/route.svelte";
  import { detailState } from "../../state/store.svelte";
  import DraftCard from "../gates/DraftCard.svelte";
  import UnitCard from "./UnitCard.svelte";
  import UnitComposer from "./UnitComposer.svelte";
  import { itemStateWord, RESOLVED_ITEMS_SHOWN } from "./unit";

  // What a unit detail shows beside the row: its card, its items with whom each waits on and the task that holds
  // it, its composer, the drafts that wait on it, and the open tasks that hold it.
  let { d }: { d: J } = $props();

  let unfolded = $state(false);
  const tasks = $derived(detailState.unitKey ? detailState.unitTasks : []);
  const drafts = $derived(detailState.unitKey ? detailState.unitDrafts : []);
  const items = $derived((d && d.items) || []);
  const isOpen = (it: J) => it.state !== "resolved";
  const open = $derived(items.filter(isOpen));
  const resolved = $derived(items.filter((it: J) => !isOpen(it)));
  const hidden = $derived(unfolded ? 0 : Math.max(0, resolved.length - RESOLVED_ITEMS_SHOWN));
  const listed = $derived([...open, ...resolved.slice(0, resolved.length - hidden)]);
  const taskHref = (id: string) => "#" + (viewHash({ task: id, pr: "" }) || "task=" + encodeURIComponent(id));
</script>

<div class="d-unit">
  {#if d && d.card}<UnitCard s={d.card} />{/if}
  {#if items.length}
    <h2 class="d-sub">{t("Items")}</h2>
    <ul class="d-items">
      {#each listed as it (it.id)}
        {@const tie = (d.item_ties || {})[it.id] || {}}
        {@const st = it.state || "open"}
        {@const people = tie.waits_on || []}
        <li class={"item-" + st}><span class="pill">{itemStateWord(st)}</span> <span>{it.summary || it.id}</span>{#if isOpen(it)}{" "}<span class="meta">{#if people.length}{t("Waiting on")} <strong>{people.map((p: J) => (p.me ? t("you") : personName(p))).join(", ")}</strong>{:else}{t("no owner")}{/if}</span>{/if}{#if tie.task}{" · "}<a href={taskHref(tie.task)} class="ref" data-focus={"unit-item-task:" + it.id}>{tie.task_title || tie.task}</a>{/if}</li>
      {/each}
    </ul>
    {#if hidden}<button type="button" class="act" data-focus="unit:items:more" onclick={() => (unfolded = true)}>{tn("+{n} resolved", hidden)}</button>{/if}
  {/if}
  {#if d && d.row}<UnitComposer u={d.row.unit} />{/if}
  {#if drafts.length}
    <section class="d-gates" aria-label={t("Open gates")}>
      <h2 class="d-sub">{t("Waiting on you")}</h2>
      {#each drafts as dr (dr.id)}<DraftCard task={null} draft={dr} scope="unit" />{/each}
    </section>
  {/if}
  {#if tasks.length}
    <h2 class="d-sub">{t("Tasks")}</h2>
    <ul class="d-tasks">
      {#each tasks as e (e.id)}
        <li><a href={"#" + viewHash({ task: e.id, pr: "" })} data-focus={"unit-task:" + e.id}>{e.title || e.id}</a> <span class="pill">{e.state}</span></li>
      {/each}
    </ul>
  {/if}
</div>
