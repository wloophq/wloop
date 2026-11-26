<script lang="ts">
  import { onMount } from "svelte";
  import { FLOAT_HOVER_OPEN_MS, FLOAT_STATE_EVERY_MS, floatDeps, floatState } from "../../state/float.svelte";
  import { floatSize } from "./float";
  import Pill from "./Pill.svelte";

  // The floating widget's tab page: the pill alone, upright on a docked edge. It reports its size, which depends on
  // where it is docked and on its counts only, asks the host for the panel, which opens in a window of its own, and
  // tells it how its last read of the queue went, as it changes and every FLOAT_STATE_EVERY_MS, which decides
  // whether the menu bar's banners stand in for it.
  const f = floatState();

  let root: HTMLElement | undefined = $state();
  let hover: ReturnType<typeof setTimeout> | undefined;

  const report = (force = false) => floatSize(root, "", force);

  $effect(() => {
    void [f.items.length, f.read, f.place.edge, f.turns, f.loaded];
    queueMicrotask(() => report());
  });

  let told = "";
  $effect(() => {
    const read = f.loaded || f.read !== "ok" ? f.read : "";
    if (!read || read === told) return;
    told = read;
    floatDeps.post({ type: "float:state", read });
  });

  function enter(): void {
    clearTimeout(hover);
    hover = setTimeout(() => f.askPanel(), FLOAT_HOVER_OPEN_MS);
  }

  function toggle(): void {
    clearTimeout(hover);
    f.askPanel(true);
  }

  onMount(() => {
    document.documentElement.classList.add("float-page");
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => report());
    if (ro && root) ro.observe(root);
    report();
    const beat = setInterval(() => {
      if (told) floatDeps.post({ type: "float:state", read: told });
    }, FLOAT_STATE_EVERY_MS);
    return () => {
      clearInterval(beat);
      if (ro) ro.disconnect();
      clearTimeout(hover);
      document.documentElement.classList.remove("float-page");
    };
  });
</script>

<div class="fl" data-edge={f.place.edge || undefined} bind:this={root} role="presentation">
  <Pill {f} onenter={enter} onleave={() => clearTimeout(hover)} ontoggle={toggle} ondrop={() => report(true)} />
</div>
