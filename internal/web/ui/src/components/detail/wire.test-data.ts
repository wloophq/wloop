import { readFileSync } from "node:fs";
import type { J } from "../../lib/util";

// wire reads the daemon's served fixtures (internal/daemon/testdata/wire) that the Go wire tests keep in step.
export function wire(): { specs: J; rows: Record<string, J>; detail: J } {
  const read = (name: string) => JSON.parse(readFileSync(new URL("../../../../../daemon/testdata/wire/" + name, import.meta.url), "utf8"));
  return { specs: read("specs.json"), rows: read("actions.json"), detail: read("detail.json") };
}
