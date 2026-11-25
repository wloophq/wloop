<script lang="ts">
  import { onMount } from "svelte";
  import { fmtNumber, t, tn } from "../../lib/i18n.svelte";
  import { typing } from "../../lib/util";
  import { FLOAT_HOVER_CLOSE_MS, FLOAT_HOVER_OPEN_MS, floatState, type FloatHide } from "../../state/float.svelte";
  import { notice } from "../../state/store.svelte";
  import Agents from "./Agents.svelte";
  import { floatSize, hovered } from "./float";
  import Peek from "./Peek.svelte";
  import Pill from "./Pill.svelte";
  import Queue from "./Queue.svelte";

  // The floating widget's page: the pill, and beside it the panel with the agents and the queue, or, while a peek
  // shows on the tab of a docked pill, what arrived. The page is transparent and reports its size, so the window that
  // hosts it fits what it draws.
  const f = floatState();
  const open = $derived(f.mode === "panel");
  const n = $derived(f.items.length);
  const flyout = $derived(!open && f.place.edge && f.peeked);
  const maxW = $derived(f.place.maxW || window.screen.availWidth);
  const maxH = $derived(f.place.maxH || window.screen.availHeight);

  let root: HTMLElement | undefined = $state();
  let hover: ReturnType<typeof setTimeout> | undefined;
  let menu = $state(false);

  const HIDES: { mode: FloatHide; label: () => string }[] = [
    { mode: "quiet", label: () => t("Hide until something new") },
    { mode: "hour", label: () => t("Hide for 1 hour") },
    { mode: "tomorrow", label: () => t("Hide until tomorrow") },
  ];

  const report = (force = false) => floatSize(root, f.mode, force);

  $effect(() => {
    void [f.mode, f.items.length, f.peeked, f.read, f.place.edge, f.place.up, maxW, maxH, menu, f.turns];
    queueMicrotask(() => report());
  });

  function hide(mode: FloatHide): void {
    menu = false;
    f.hide(mode);
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
    f.setMode(open ? "pill" : "panel");
  }

  function blur(): void {
    if (f.pinned) return;
    clearTimeout(hover);
    f.blur();
  }

  function keydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      f.setMode("pill");
      return;
    }
    if (!open || e.metaKey || e.ctrlKey || e.altKey || typing(e.target as Element)) return;
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
    } else if (e.key === "u") {
      e.preventDefault();
      f.openUnit();
    }
  }

  onMount(() => {
    document.documentElement.classList.add("float-page");
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => report());
    if (ro && root) ro.observe(root);
    report();
    return () => {
      if (ro) ro.disconnect();
      clearTimeout(hover);
      document.documentElement.classList.remove("float-page");
    };
  });
</script>

<svelte:window onkeydown={keydown} onblur={blur} />

<div
  class={["fl", open && "open", !f.online && "offline", f.place.up && "up"]}
  data-mode={f.mode}
  data-edge={f.place.edge || undefined}
  style:--fl-max-w="{maxW}px"
  style:--fl-max-h="{maxH}px"
  bind:this={root}
  role="presentation"
  onpointerleave={leave}
>
  <Pill {f} onenter={enterPill} ontoggle={toggle} ondrop={() => report(true)} />
  {#if flyout}
    <button type="button" class="fl-flyout" use:hovered onclick={() => f.setMode("panel")}><span class="fl-flyout-text"><Peek item={flyout} /></span></button>
  {/if}
  {#if open}
    <section class="fl-panel" aria-label={f.title || undefined} onpointerenter={enterPanel}>
      <header class="fl-head">
        <i class="fl-mark" aria-hidden="true"></i>
        <h2>{n ? tn("{n} wait on you", n) : t("Nothing waits on you")}</h2>
        <span class="fl-sp"></span>
        {#if n}<span class="fl-meta">{t("{n} of {total}", { n: fmtNumber(f.current + 1), total: fmtNumber(n) })}</span>{/if}
        <button type="button" class="fl-quiet" aria-pressed={f.pinned ? "true" : "false"} onclick={() => f.setPinned(!f.pinned)}>{t("Pin")}</button>
        <span class="fl-menu">
          <button type="button" class="fl-quiet" aria-haspopup="menu" aria-expanded={menu ? "true" : "false"} aria-label={t("Hide")} onclick={() => (menu = !menu)}>⋯</button>
          {#if menu}
            <span class="fl-menu-list" role="menu">
              {#each HIDES as h (h.mode)}<button type="button" role="menuitem" data-hide={h.mode} use:hovered onclick={() => hide(h.mode)}>{h.label()}</button>{/each}
            </span>
          {/if}
        </span>
      </header>
      <div class="fl-body">
        {#if f.read === "offline"}
          <p class="fl-off">{t("The daemon is not answering.")}</p>
        {:else if f.read === "failed"}
          <p class="fl-off">{t("Could not read the queue.")}</p>
        {:else}
          <Agents turns={f.turns} failed={f.turnsFailed} />
          <Queue {f} />
        {/if}
      </div>
      {#if f.failed || (notice.err && notice.text)}<p class="fl-err" role="alert">{f.failed || notice.text}</p>{/if}
    </section>
  {/if}
</div>
