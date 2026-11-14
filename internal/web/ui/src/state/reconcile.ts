import { unitKey, type J } from "../lib/util";

// identity is what an item of a served list is known by across reads: its id, else its unit; undefined for an
// item that carries neither, which a list then matches by place.
export function identity(x: J): string | undefined {
  if (!x || typeof x !== "object" || Array.isArray(x)) return undefined;
  if (typeof x.id === "string" || typeof x.id === "number") return "id:" + x.id;
  if (x.unit && typeof x.unit === "object" && typeof x.unit.provider === "string") return "unit:" + unitKey(x.unit);
  return undefined;
}

const isObject = (x: unknown): x is Record<string, unknown> => !!x && typeof x === "object" && !Array.isArray(x);

// reconcile brings target up to source in place and returns it: an object keeps itself and each of its values
// that source still holds, a list keeps each item source holds again by identity, in source's order, and only a
// value that changed is written. When target and source are not both objects or both lists, it returns source,
// which the caller stores in target's place.
export function reconcile<T>(target: T, source: T): T {
  if (Array.isArray(target) && Array.isArray(source)) return reconcileList(target, source) as T;
  if (isObject(target) && isObject(source)) return reconcileObject(target, source) as T;
  return source;
}

function reconcileObject(target: Record<string, unknown>, source: Record<string, unknown>): Record<string, unknown> {
  for (const k of Object.keys(target)) if (!Object.hasOwn(source, k)) delete target[k];
  for (const [k, v] of Object.entries(source)) {
    const next = reconcile(target[k], v);
    if (target[k] !== next) target[k] = next;
  }
  return target;
}

function reconcileList(target: unknown[], source: unknown[]): unknown[] {
  const byID = new Map<string, unknown>();
  for (const x of target) {
    const id = identity(x);
    if (id !== undefined && !byID.has(id)) byID.set(id, x);
  }
  const next = source.map((x, i) => {
    const id = identity(x);
    const old = id !== undefined ? byID.get(id) : identity(target[i]) === undefined ? target[i] : undefined;
    if (id !== undefined) byID.delete(id);
    return old === undefined ? x : reconcile(old, x);
  });
  if (next.length === target.length && next.every((x, i) => x === target[i])) return target;
  target.splice(0, target.length, ...next);
  return target;
}
