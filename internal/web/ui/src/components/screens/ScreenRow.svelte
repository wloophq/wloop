<script lang="ts">
  import type { J } from "../../lib/util";
  import { minute } from "../../state/clock.svelte";
  import RowView from "../rows/RowView.svelte";
  import { screenRow } from "./screen";

  // Task e's row as the list of view draws it.
  let { task, view = "" }: { task: J; view?: string } = $props();
  const r = $derived(screenRow(task, view, minute()));
</script>

<RowView model={r.model} lens={r.lens} />
