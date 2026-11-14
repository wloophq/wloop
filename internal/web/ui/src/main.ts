import { mount } from "svelte";
import App from "./App.svelte";
import { brand } from "./brand";

export function start(target: HTMLElement): typeof brand {
  mount(App, { target });
  return brand;
}
