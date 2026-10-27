import { describe, expect, it } from "vitest";
import { celBool, celEval } from "./cel";

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
