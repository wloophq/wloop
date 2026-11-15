<script lang="ts">
  import { fmtNumber } from "../../lib/i18n.svelte";
  import { railTipID, type RailItem } from "./rail";

  let { item }: { item: RailItem } = $props();
  const tipID = $derived(item.tip ? railTipID(item.hash) : undefined);
</script>

<li>
  <a
    href={"#" + item.hash}
    class={"nav " + (item.on ? "on " : "") + (item.cls || "")}
    aria-current={item.on ? "page" : undefined}
    aria-describedby={tipID}
    data-focus={"rail:" + item.hash}
    ><span class="label" title={item.tip ? undefined : item.label}>{item.label}</span><span class="count"
      >{typeof item.count === "number" ? fmtNumber(item.count) : item.count}</span
    >{#if item.tip}<span class="rail-tip" role="tooltip" id={tipID}>{item.tip}</span>{/if}</a>
</li>
