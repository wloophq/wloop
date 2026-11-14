import { expect, test } from "vitest";
import { brand } from "./brand";

test("the brand stays placeholders the server replaces", () => {
  expect(brand).toEqual({ name: "{{name}}", command: "{{command}}", scheme: "{{scheme}}" });
});
