<script lang="ts">
  import { fmtNumber } from "../../lib/i18n.svelte";
  import { multiCopy, type Lens, type Pipeline } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import { collapsed } from "../../state/store.svelte";
  import OnlyThis from "./OnlyThis.svelte";
  import RowView from "./RowView.svelte";
  import { groupKey, headClass, toggleCollapsed, type RowModel } from "./model";

  // res's groups of items as rows, model drawing each: under a sticky head per group when the lens groups, the
  // head a collapse toggle with its count and an "only this" filter; cls names the kind of list.
  let { res, model, lens, cls = "" }: { res: Pick<Pipeline, "groups" | "group">; model: (it: J, copy: string) => RowModel; lens: Lens; cls?: string } = $props();

  const grouped = $derived(!!res.group);
  const multi = $derived(grouped && multiCopy(res.group, lens.fields));
  const groups = $derived(res.groups.filter((g) => g.rows.length).map((g) => ({ g, key: groupKey(g), rows: g.rows.map((it) => model(it, multi ? g.key : "")) })));
</script>

<div class={cls ? "rows " + cls : "rows"}>
  {#each groups as { g, key, rows }, i (key)}
    {#if grouped}
      {@const shut = collapsed.has(key)}
      {@const id = `g-${lens.view}-${i}`}
      <section class="group" {id} aria-label={g.label}>
        <div class={headClass("g-head", g, lens)}>
          <button type="button" class="g-toggle" aria-expanded={shut ? "false" : "true"} aria-controls={id} data-focus={`group:${key}`} onclick={() => toggleCollapsed(key)}
            >{`${shut ? "▸" : "▾"} ${g.label}`}</button
          ><span class="count">{fmtNumber(g.rows.length)}</span><OnlyThis {g} {lens} />
        </div>
        {#each rows as m (m.scope)}<RowView model={m} {lens} hidden={shut} />{/each}
      </section>
    {:else}
      {#each rows as m (m.scope)}<RowView model={m} {lens} />{/each}
    {/if}
  {/each}
</div>
