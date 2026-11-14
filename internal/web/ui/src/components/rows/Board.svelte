<script lang="ts">
  import type { Group, Lens } from "../../lib/lens";
  import type { J } from "../../lib/util";
  import Lane from "./Lane.svelte";
  import { groupKey, type RowModel } from "./model";

  // A lane per group of the lens, each holding its items' cards. The board stays one node across syncs, so where
  // it was scrolled stays.
  let {
    groups,
    lens,
    title,
    model,
    id = undefined,
    class: cls = "board",
    hidden = false,
  }: { groups: Group[]; lens: Lens; title: string; model: (it: J, copy: string) => RowModel; id?: string; class?: string; hidden?: boolean } = $props();
</script>

<div {id} class={cls} {hidden}>
  {#each groups as g (groupKey(g))}<Lane {g} {lens} {title} {model} />{/each}
</div>
