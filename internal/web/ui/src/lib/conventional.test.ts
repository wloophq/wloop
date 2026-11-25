import { expect, test } from "vitest";
import { conventionalLabel, tally } from "./conventional";

const bodies = (...texts: string[]) => texts.map((body) => ({ body }));

test("the tally counts each label, the blocking ones first", () => {
  const got = tally(bodies(
    "suggestion: name the retry budget",
    "issue (blocking): the error is dropped",
    "nit: trailing space",
    "suggestion: split the handler",
    "issue (blocking, security): the token is logged",
    "suggestion(non-blocking): add a test",
  ));
  expect(got).toEqual([
    { label: "issue", blocking: true, n: 2 },
    { label: "suggestion", blocking: false, n: 3 },
    { label: "nit", blocking: false, n: 1 },
  ]);
});

test("a comment with no label counts in no group", () => {
  expect(tally(bodies("Looks fine to me.", "", "question: why 3?"))).toEqual([{ label: "question", blocking: false, n: 1 }]);
});

test("the label of a comment reads its decorations", () => {
  expect(conventionalLabel("praise: clean split")).toEqual({ label: "praise", blocking: false });
  expect(conventionalLabel("  Issue (blocking): stop")).toEqual({ label: "issue", blocking: true });
  expect(conventionalLabel("http://acme.test: not a label")).toBeNull();
});
