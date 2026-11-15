<script lang="ts">
  import { onMount } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { clampDetailWidth, defaultDetailWidth, DETAIL_WIDTH_MIN, DETAIL_WIDTH_STEP, maxDetailWidth, saveDetailWidth, storedDetailWidth } from "./width";

  // The separator that sizes the detail panel: dragged, moved with the arrows, or reset with a double click; the
  // width the person chose is kept and clamped again to every viewport.
  let preferred = storedDetailWidth(window.innerWidth);
  let width = $state(preferred);
  let viewport = $state(window.innerWidth);
  let handle: HTMLDivElement;

  function layout() {
    viewport = window.innerWidth;
    width = clampDetailWidth(preferred, viewport);
    document.documentElement.style.setProperty("--detail-width", width + "px");
  }

  function choose(px: number) {
    preferred = clampDetailWidth(px, window.innerWidth);
    layout();
  }

  onMount(layout);

  function release(e: PointerEvent) {
    if (!handle.hasPointerCapture(e.pointerId)) return;
    handle.releasePointerCapture(e.pointerId);
    document.body.classList.remove("resizing");
    saveDetailWidth(preferred);
  }
</script>

<svelte:window onresize={layout} />

<!-- A focusable separator is a widget (WAI-ARIA "separator"), which the checker does not know. -->
<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
<div
  bind:this={handle}
  id="detail-resize"
  role="separator"
  aria-orientation="vertical"
  aria-label={t("Resize the detail panel")}
  aria-valuenow={width}
  aria-valuemin={DETAIL_WIDTH_MIN}
  aria-valuemax={maxDetailWidth(viewport)}
  tabindex="0"
  onpointerdown={(e) => {
    handle.setPointerCapture(e.pointerId);
    document.body.classList.add("resizing");
    e.preventDefault();
  }}
  onpointermove={(e) => {
    if (handle.hasPointerCapture(e.pointerId)) choose(window.innerWidth - e.clientX);
  }}
  onpointerup={release}
  onpointercancel={release}
  onkeydown={(e) => {
    const delta = e.key === "ArrowLeft" ? DETAIL_WIDTH_STEP : e.key === "ArrowRight" ? -DETAIL_WIDTH_STEP : 0;
    if (!delta) return;
    e.preventDefault();
    choose(width + delta);
    saveDetailWidth(preferred);
  }}
  ondblclick={() => {
    document.documentElement.style.removeProperty("--detail-width");
    choose(defaultDetailWidth());
    saveDetailWidth(null);
  }}></div>
