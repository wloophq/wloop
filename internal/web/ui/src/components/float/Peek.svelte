<script lang="ts">
  import type { J } from "../../lib/util";
  import { itemRef, itemText, toneClass } from "./float";

  // What a peek says of the item that arrived: its kind word in its tone, then its ref and its text.
  let { item }: { item: J } = $props();
</script>

<b class={toneClass(item.tone)}>{item.word || item.kind}</b>{[itemRef(item), itemText(item)].filter(Boolean).map((s) => " · " + s).join("")}
