// The mocks' quotas against the server's (internal/core/quota.go,
// quotas_admin.go): every level held, a soft one over asks the seeded
// approval, and What applies here counts what each level holds.
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { RequestDraft } from "@/lib/api/types";
import { adminState } from "./administration";
import { deploymentUsage } from "./quotas";
import { db } from "./seed";

const draft = (): RequestDraft => ({
  catalog_item_id: "itm_rhel9",
  version_id: db.items.find((i) => i.id === "itm_rhel9")!.version.id,
  project_id: "prj_web",
  environment: "dev",
  inputs: { role: "app", size: "M" },
  count: 1,
});
const preview = () => call(api.POST("/requests/preview", { body: draft() }));

describe("the mocks' quotas", () => {
  it("refuse past a hard project quota in the server's words, and ask approval past a soft one", async () => {
    const web = db.quota.prj_web!;
    web.limits.vm_count = web.used.vm_count;
    let p = await preview();
    expect(p.errors.map((e) => e.message)).toContain(
      "This request needs 1 VM but Web Platform has 0 left. Choose a smaller size or ask a project admin to raise the quota.",
    );
    expect(p.approval.required).toBe(false);

    adminState(db).quotas.prj_web = [{ environment: "", resource: "vm_count", limit: web.used.vm_count, enforcement: "soft" }]; // prettier-ignore
    p = await preview();
    expect(p.errors.filter((e) => e.code === "quota_exceeded")).toEqual([]);
    expect(p.quota.find((l) => l.level === "project" && l.resource === "vm_count")).toMatchObject({
      exceeds: true,
      enforcement: "soft",
    });
    expect(p.approval.policies.map((x) => [x.policy_id, x.reason])).toEqual([
      ["pol_soft_quota", "Over the project's soft quota"],
    ]);
  });

  it("hold a project's quota in one environment to what its deployments there hold", async () => {
    const inDev = db.deployments
      .filter((d) => d.project.id === "prj_web" && d.environment === "dev" && d.status !== "deleted")
      .reduce((n, d) => n + deploymentUsage(d).vm_count, 0);
    adminState(db).quotas.prj_web = [
      { environment: "", resource: "vcpu", limit: 500, enforcement: "hard" },
      { environment: "dev", resource: "vm_count", limit: inDev, enforcement: "hard" },
      { environment: "prd", resource: "vm_count", limit: 1, enforcement: "hard" },
    ];
    const p = await preview();
    expect(p.errors.map((e) => e.message)).toContain(
      "This request needs 1 VM but Web Platform in Development has 0 left. Choose a smaller size or ask a project admin to raise the quota.",
    );

    const explained = await call(
      api.POST("/admin/policies/explain", { body: { project_id: "prj_web", environment: "dev" } }),
    );
    expect(explained.quota.map((l) => [l.level, l.resource, l.scope, l.used])).toEqual([
      ["project", "vcpu", "", db.quota.prj_web!.used.vcpu],
      ["environment", "vm_count", "dev", inDev],
    ]);
  });
});
