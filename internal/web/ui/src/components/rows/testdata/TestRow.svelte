<script lang="ts">
  import type { Lens } from "../../../lib/lens";
  import RowView from "../RowView.svelte";
  import type { RowModel } from "../model";

  let { make, card = false }: { make: () => { model: RowModel; lens: Lens }; card?: boolean } = $props();
  const r = $derived(make());
</script>

{#if card}<ol><RowView model={r.model} lens={r.lens} card /></ol>{:else}<RowView model={r.model} lens={r.lens} />{/if}
