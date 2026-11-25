<script lang="ts">
  import { onMount } from "svelte";
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { typing } from "../../lib/util";
  import { FLOAT_HOVER_CLOSE_MS, FLOAT_HOVER_OPEN_MS, floatDeps, floatState } from "../../state/float.svelte";
  import { notice } from "../../state/store.svelte";
  import Agents from "./Agents.svelte";
  import Pill from "./Pill.svelte";
  import Queue from "./Queue.svelte";

  // The floating widget's page: the pill, and beside it the panel with the agents and the queue. The page is
  // transparent and reports its size, so the window that hosts it fits what it draws.
  const f = floatState();
  const open = $derived(f.mode === "panel");
  const n = $derived(f.items.length);

  let root: HTMLElement | undefined = $state();
  let hover: ReturnType<typeof setTimeout> | undefined;
  const sent = { w: 0, h: 0 };

  function report(): void {
    if (!root) return;
    const r = root.getBoundingClientRect();
    const w = Math.ceil(r.width);
    const h = Math.ceil(r.height);
    if (w === sent.w && h === sent.h) return;
    Object.assign(sent, { w, h });
    floatDeps.post({ type: "float:size", w, h });
  }

  function later(fn: () => void, ms: number): void {
    clearTimeout(hover);
    hover = setTimeout(fn, ms);
  }

  const enterPill = () => {
    if (!open) later(() => f.setMode("panel"), FLOAT_HOVER_OPEN_MS);
    else clearTimeout(hover);
  };
  const enterPanel = () => clearTimeout(hover);
  const leave = () => {
    if (f.pinned) return clearTimeout(hover);
    later(() => f.setMode("pill"), open ? FLOAT_HOVER_CLOSE_MS : 0);
  };

  function toggle(): void {
    clearTimeout(hover);
    if (open && f.pinned) f.setMode("pill");
    else f.setPinned(true);
  }

  function keydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      f.setMode("pill");
      return;
    }
    if (!open || e.metaKey || e.ctrlKey || e.altKey) return;
    const inField = typing(e.target as Element);
    if (inField && e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
    const moves: Record<string, number> = { ArrowUp: -1, ArrowDown: 1, k: -1, j: 1 };
    if (e.key in moves) {
      e.preventDefault();
      f.move(moves[e.key]);
    } else if (e.key === "Enter" && !(e.target instanceof HTMLButtonElement)) {
      e.preventDefault();
      void f.act();
    } else if (e.key === "o") {
      e.preventDefault();
      f.open();
    }
  }

  onMount(() => {
    document.documentElement.classList.add("float-page");
    const ro = new ResizeObserver(report);
    if (root) ro.observe(root);
    report();
    return () => {
      ro.disconnect();
      clearTimeout(hover);
      document.documentElement.classList.remove("float-page");
    };
  });
</script>

<svelte:window onkeydown={keydown} />

<div
  class={["fl", open && "open", !f.online && "offline", f.place.up && "up"]}
  data-edge={f.place.edge || undefined}
  bind:this={root}
  role="presentation"
  onpointerleave={leave}
>
  <Pill {f} onenter={enterPill} ontoggle={toggle} />
  {#if open}
    <section class="fl-panel" aria-label={f.title || undefined} onpointerenter={enterPanel}>
      <header class="fl-head">
        <i class="fl-mark" aria-hidden="true"></i>
        <h2>{n ? tn("{n} wait on you", n) : t("Nothing waits on you")}</h2>
        <span class="fl-sp"></span>
        {#if n}<span class="fl-meta">{t("{n} of {total}", { n: fmtNumber(f.current + 1), total: fmtNumber(n) })}</span>{/if}
        <button type="button" class="fl-quiet" aria-pressed={f.pinned ? "true" : "false"} onclick={() => f.setPinned(!f.pinned)}>{t("Pin")}</button>
      </header>
      <div class="fl-body">
        {#if !f.online}
          <p class="fl-off">{t("The daemon is not answering.")}</p>
        {:else}
          <Agents turns={f.turns} />
          <Queue {f} />
        {/if}
      </div>
      {#if notice.err && notice.text}<p class="fl-err" role="alert">{notice.text}</p>{/if}
    </section>
  {/if}
</div>
