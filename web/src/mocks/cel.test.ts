import { describe, expect, it } from "vitest";
import { celBool, celEval, Missing, unmodelled } from "./cel";

const ctx = {
  environment: "prd",
  inputs: { size: "XL", memory_gb: 48, role: "front", post_config: ["linux-baseline", "idm-join"], empty: "" },
  project: { code: "web" },
};

describe("celEval", () => {
  it.each([
    ['environment == "prd" && inputs.size == "XL"', true],
    ['environment == "prd" && inputs.size == "M"', false],
    ["inputs.memory_gb > 32", true],
    ["inputs.memory_gb >= 48 && inputs.memory_gb < 64", true],
    ['inputs.size in ["L", "XL"]', true],
    ['"idm-join" in inputs.post_config', true],
    ['inputs.post_config.contains("role-nginx")', false],
    ['inputs.role.startsWith("fr")', true],
    ["size(inputs.post_config) == 2", true],
    ["!(environment == 'dev')", true],
    ["inputs.missing == null", true],
    ["inputs.missing.deeper == null", true],
    ['inputs.empty == "" || false', true],
  ])("%s → %s", (expr, expected) => {
    expect(celEval(expr, ctx)).toBe(expected);
  });

  it("does arithmetic, string concatenation and ternaries", () => {
    expect(celEval("inputs.memory_gb * 2 + 1", ctx)).toBe(97);
    expect(celEval('project.code + "-" + environment', ctx)).toBe("web-prd");
    expect(celEval('environment == "prd" ? "No expiry" : "30 days"', ctx)).toBe("No expiry");
  });

  it("reads maps, as a REST call's body has them", () => {
    expect(celEval("{'a': x, 'b': {'c': [1, x]}}", { x: 2 })).toEqual({ a: 2, b: { c: [1, 2] } });
    expect(celEval("{}", {})).toEqual({});
    expect(celEval("{'a': 1}.a == 1", {})).toBe(true);
  });

  it("rejects broken rules", () => {
    expect(() => celEval("inputs.size ==", ctx)).toThrow();
    expect(() => celEval("unknownFn(1)", ctx)).toThrow(/Unknown function/);
  });
});

describe("celBool", () => {
  it("falls back when a rule is empty or broken", () => {
    expect(celBool(undefined, ctx, true)).toBe(true);
    expect(celBool("((", ctx, false)).toBe(false);
  });
});

describe("celEval, strictly", () => {
  const vars = { ...ctx, steps: { ask: { answer: { size: null } }, vm: unmodelled } };

  it("refuses a field that isn't there, as the server does, where it reads null otherwise", () => {
    expect(celEval("inputs.absent == 'x'", vars)).toBe(false);
    expect(() => celEval("inputs.absent == 'x'", vars, true)).toThrow(Missing);
    expect(() => celEval("steps.skipped.status == 200", vars, true)).toThrow(Missing);
    expect(() => celEval("inputs.post_config[5] == 'x'", vars, true)).toThrow("index out of range");
    expect(celEval("steps.ask.answer.size == null", vars, true)).toBe(true);
  });

  it("asks has() whether it is there, and reads what the mocks don't model as null", () => {
    expect(celEval("has(steps.skipped) && steps.skipped.status == 200", vars, true)).toBe(false);
    expect(celEval("has(steps.vm) && steps.vm.fqdn == null", vars, true)).toBe(true);
  });
});
