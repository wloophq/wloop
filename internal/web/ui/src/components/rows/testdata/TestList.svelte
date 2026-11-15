<script lang="ts">
  import type { Lens, Pipeline } from "../../../lib/lens";
  import type { J } from "../../../lib/util";
  import Board from "../Board.svelte";
  import RowList from "../RowList.svelte";
  import type { RowModel } from "../model";

  let { make, board = false }: { make: () => { res: Pipeline; lens: Lens; model: (it: J, copy: string) => RowModel }; board?: boolean } = $props();
  const r = $derived(make());
</script>

{#if board}<Board groups={r.res.groups} lens={r.lens} title={r.lens.view} model={r.model} />{:else}<RowList res={r.res} lens={r.lens} model={r.model} />{/if}
