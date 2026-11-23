import { describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { quotaWhere, tightest } from "./quota";

type QuotaImpact = components["schemas"]["QuotaImpact"];

const line = (l: Partial<QuotaImpact>): QuotaImpact => ({
  resource: "vcpu",
  level: "project",
  used: 0,
  adding: 2,
  exceeds: false,
  limit: null,
  ...l,
});

describe("the tightest quota", () => {
  it("keeps, of each resource, the line with the least left", () => {
    const project = line({ limit: 160, used: 112 });
    const member = line({ level: "project_user", limit: 8, used: 4 });
    const memory = line({ resource: "memory_gb", limit: null });
    expect(tightest([project, member, memory])).toEqual([member, memory]);
  });

  it("prefers a line that refuses to a soft one further over", () => {
    const soft = line({ level: "global", limit: 1, used: 4, exceeds: true, enforcement: "soft" });
    const hard = line({ level: "project_user", limit: 4, used: 3, exceeds: true, enforcement: "hard" });
    expect(tightest([soft, hard])).toEqual([hard]);
  });

  it("names where each level counts", () => {
    const envName = (key: string) => ({ dev: "Development" })[key] ?? key;
    expect(quotaWhere({ level: "project" }, envName)).toBe("");
    expect(quotaWhere({ level: "environment", environment: "dev" }, envName)).toBe(" in Development");
    expect(quotaWhere({ level: "project_user" }, envName)).toBe(" · per member");
    expect(quotaWhere({ level: "global" }, envName)).toBe(" · all of rvav");
    expect(quotaWhere({ level: "global_user" }, envName)).toBe(" · per user, all of rvav");
  });
});
