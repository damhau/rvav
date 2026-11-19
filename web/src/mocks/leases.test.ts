import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adminState } from "./administration";
import { chooseLease, deploymentLease, leaseTermsErrors } from "./leases";
import { db } from "./seed";

type Rule = components["schemas"]["LeaseRule"];

const rule = (r: Partial<Rule> & Pick<Rule, "id" | "name">): Rule => ({
  condition: "true",
  environment: null,
  project_id: null,
  priority: 100,
  default_days: 14,
  max_days: 30,
  max_extensions: 1,
  grace_days: 7,
  expiry_action: "delete",
  enforcement: "hard",
  max_lifetime_days: 60,
  set_by_admin: true,
  ...r,
});

// As the server's (ADR-0048): a deployment whose lease a project admin's
// rule decided keeps the administrators' rule that held it, so editing or
// removing the project admin's rule later gives it no more.
describe("the mocks' lease rules", () => {
  const dep = () => db.deployments.find((d) => d.name === "web-dev-api")!;
  const setUp = () => {
    const st = adminState(db);
    st.leaseRules.push(
      rule({ id: "lsr_all", name: "All builds", priority: 50 }),
      rule({
        id: "lsr_web",
        name: "Web builds",
        project_id: "prj_web",
        set_by_admin: false,
        priority: 10,
        default_days: 7,
        max_days: 20,
        max_lifetime_days: 30,
      }),
    );
    const choice = chooseLease(db, "prj_web", "dev", {}, true);
    expect(choice.rule?.id).toBe("lsr_web");
    expect(choice.cap?.id).toBe("lsr_all");
    st.leaseRuleOf[dep().id] = choice.rule!.id;
    st.leaseCapOf[dep().id] = choice.cap!.id;
    return st;
  };

  it("hold a project admin's rule to the administrators' one, field by field, once it is edited", () => {
    const st = setUp();
    Object.assign(
      st.leaseRules.find((r) => r.id === "lsr_web")!,
      {
        max_days: 90,
        max_extensions: 5,
        enforcement: "soft",
        max_lifetime_days: null,
      },
    );
    expect(deploymentLease(db, dep())).toMatchObject({
      policy: "Web builds",
      terms: { default_days: 7, max_days: 30, max_extensions: 1, enforcement: "hard", max_lifetime_days: 60 },
    });
  });

  it("give its deployments the administrators' rule once it is removed", () => {
    const st = setUp();
    st.leaseRules = st.leaseRules.filter((r) => r.id !== "lsr_web");
    expect(deploymentLease(db, dep())).toMatchObject({
      policy: "All builds",
      terms: { max_days: 30, max_extensions: 1, max_lifetime_days: 60 },
    });
  });

  it("keep no other rule when an administrator's decides", () => {
    adminState(db).leaseRules.push(rule({ id: "lsr_all", name: "All builds" }));
    expect(chooseLease(db, "prj_web", "dev", {}, true).cap).toBeUndefined();
  });
});

// As the server's leaseTerms and policyNameLength (internal/core/lease_rules.go).
describe("the mocks' lease rule and limit policy writes", () => {
  const terms = {
    default_days: 7,
    max_days: 30,
    max_extensions: -1,
    grace_days: -1,
    expiry_action: "delete",
    enforcement: "hard",
    max_lifetime_days: 20,
  } as const;

  it("put each problem with lease terms where the server does", () => {
    expect(leaseTermsErrors(terms)).toEqual([
      { field: "/max_lifetime_days", code: "minimum", message: "The maximum can't be longer than the lifetime." },
      { field: "/max_extensions", code: "minimum", message: "Extensions are zero or more." },
      { field: "/grace_days", code: "minimum", message: "Grace days are zero or more." },
    ]);
    expect(leaseTermsErrors({ ...terms, max_days: null, default_days: 30, max_extensions: 0, grace_days: 0 })).toEqual([
      { field: "/max_lifetime_days", code: "minimum", message: "The default can't be longer than the lifetime." },
    ]);
  });

  it("refuse a name longer than 120 characters", async () => {
    const tooLong = {
      status: 422,
      fieldErrors: [{ field: "/name", code: "too_long", message: "120 characters at most." }],
    };
    const rule = { condition: "true", max_extensions: 0, grace_days: 0, expiry_action: "delete" } as const;
    await expect(
      call(api.POST("/admin/lease-rules", { body: { ...rule, name: "x".repeat(121) } })),
    ).rejects.toMatchObject(tooLong);
    await expect(
      call(
        api.POST("/admin/deployment-limit-policies", {
          body: { name: "x".repeat(121), enabled: true, deployments_per_user: 2 },
        }),
      ),
    ).rejects.toMatchObject(tooLong);
    const name = ` ${"x".repeat(120)} `;
    const saved = [
      await call(api.POST("/admin/lease-rules", { body: { ...rule, name } })),
      await call(
        api.POST("/admin/deployment-limit-policies", { body: { name, enabled: true, deployments_per_user: 2 } }),
      ),
    ];
    expect(saved.map((x) => x.name)).toEqual([name.trim(), name.trim()]);
  });
});
