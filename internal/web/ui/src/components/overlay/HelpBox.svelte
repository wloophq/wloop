<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import type { HelpGroup } from "../../state/overlays.svelte";
  import { focusBack } from "./focus";
  import Shade from "./Shade.svelte";

  // Every binding by context; Esc or ? closes it.
  let { groups, finish }: { groups: HelpGroup[]; finish: () => void } = $props();

  const restore = focusBack();
  let box: HTMLDivElement;

  function close() {
    restore();
    finish();
  }

  function key(e: KeyboardEvent) {
    if (e.key !== "Escape" && e.key !== "?") return;
    e.preventDefault();
    close();
  }

  onMount(() => box.focus());
</script>

<Shade dismiss={close}>
  <div bind:this={box} class="pal" role="dialog" aria-label={t("Keys")} tabindex="-1" onkeydown={key}>
    <div class="pal-head">{t("Keys (Esc closes)")}</div>
    <dl>
      {#each groups as g, i (i)}
        <dt>{g.head}</dt>
        {#each g.bindings as b (b.key + " " + b.action)}<dd><span>{b.help || b.action}</span><kbd>{b.key}</kbd></dd>{/each}
      {/each}
    </dl>
  </div>
</Shade>
