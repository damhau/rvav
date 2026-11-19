import { describe, expect, it } from "vitest";
import type { components } from "@/lib/api/schema";
import { adminState } from "./administration";
import { chooseLease, deploymentLease } from "./leases";
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
