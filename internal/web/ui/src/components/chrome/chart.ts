import type { J } from "../../lib/util";

// charts is how the dashboard reaches ECharts, the vendored classic script's global; tests replace it.
export const charts = {
  lib: (): J => (globalThis as J).echarts,
  dark: (): boolean => typeof matchMedia === "function" && matchMedia("(prefers-color-scheme: dark)").matches,
  observe: (el: Element, resize: () => void): (() => void) => {
    if (typeof ResizeObserver === "undefined") return () => {};
    const o = new ResizeObserver(resize);
    o.observe(el);
    return () => o.disconnect();
  },
};

export interface ChartArg {
  option: J;
  theme: string;
  click: (p: J) => void;
}

// chart is a Svelte action that draws arg.option in el as an SVG chart: it inits on mount, sets each new option in
// place, inits again when the theme changes, and disposes on destroy.
export function chart(el: HTMLElement, arg: ChartArg) {
  let theme = "";
  let c: J = null;
  let unobserve = () => {};
  const drop = () => {
    unobserve();
    if (c) c.dispose();
    c = null;
  };
  const draw = (a: ChartArg) => {
    if (!c || a.theme !== theme) {
      drop();
      theme = a.theme;
      c = charts.lib().init(el, theme, { renderer: "svg" });
      c.on("click", (p: J) => a.click(p));
      unobserve = charts.observe(el, () => c && c.resize());
    }
    if (a.option) c.setOption(a.option, { notMerge: true });
    else c.clear();
  };
  draw(arg);
  return { update: draw, destroy: drop };
}
