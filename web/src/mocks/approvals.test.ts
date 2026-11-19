import { describe, expect, it } from "vitest";
import { checkAnswer } from "./approvals";
import { limitState } from "./limits";
import { deploymentUsage, quotaState } from "./quotas";
import { db } from "./seed";

describe("an answer to a step's form in the mocks", () => {
  const form = [{ key: "why", label: "Why", type: "string" as const }];

  it("takes text of 4096 characters at most, counted as the server counts them", () => {
    for (const c of ["é", "𝄞"]) {
      expect(checkAnswer(form, { why: c.repeat(4096) }).errors).toEqual([]);
      expect(checkAnswer(form, { why: c.repeat(4097) }).errors).toEqual([
        { field: "/answer/why", code: "invalid", message: "At most 4096 characters." },
      ]);
    }
  });
});

const approve = async (number: string, inputs: Record<string, unknown>) => {
  const level = db.approvals.find((a) => a.request.number === number && a.status === "pending")!;
  const res = await fetch(`http://localhost/api/v1/approvals/${level.id}/decision`, {
    method: "POST",
    headers: {
      Authorization: "Bearer test",
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ decision: "approve", comment: "", inputs }),
  });
  return { status: res.status, body: (await res.json()) as { errors?: { code: string; message: string }[] } };
};

// As the server's applyEdits (ADR-0047): the request's owner's hard quotas
// and limits, for what the edit adds to what the request holds already.
describe("an approver's edit in the mocks", () => {
  const thomasHolds = () =>
    db.deployments
      .filter((d) => d.project.id === "prj_web" && d.owner.id === "usr_tnguyen" && d.status !== "deleted")
      .reduce((n, d) => n + deploymentUsage(d).memory_gb, 0);
  const tightVcpu = () =>
    limitState(db).policies.push({
      id: "dlp_t",
      name: "Tight",
      project_id: null,
      environment: null,
      priority: 100,
      enabled: true,
      enforcement: "hard",
      deployments_per_user: null,
      deployments_per_project: null,
      vcpu_per_deployment: 4,
      memory_gb_per_deployment: null,
      set_by_admin: true,
    });
  const asMedium = () => {
    const r = db.requests.find((x) => x.request.number === "REQ-10477")!;
    r.request.inputs = { ...r.request.inputs, size: "M" };
  };

  it("counts what it adds against the owner's quota in the project", async () => {
    asMedium();
    quotaState(db).perUser.prj_web = [
      { resource: "memory_gb", limit: thomasHolds() + 8, enforcement: "hard", set_by_admin: true },
    ];
    const refused = await approve("REQ-10477", { size: "L" });
    expect(refused.status).toBe(422);
    expect(refused.body.errors).toEqual([
      {
        field: "",
        code: "quota_exceeded",
        message: `This change needs 16 GB of memory but Thomas Nguyen's quota in Web Platform has 8 left. Choose a smaller size or ask a project admin to raise the quota.`,
      },
    ]);
    quotaState(db).perUser.prj_web![0]!.limit = thomasHolds() + 16;
    expect((await approve("REQ-10477", { size: "L" })).status).toBe(200);
  });

  it("holds a growth to the size limits, and lets a smaller size through", async () => {
    tightVcpu();
    expect((await approve("REQ-10477", { size: "L" })).status).toBe(200);
    expect(db.requests.find((x) => x.request.number === "REQ-10477")!.request.inputs?.size).toBe("L");
  });

  it("refuses a growth past a hard size limit", async () => {
    asMedium();
    tightVcpu();
    const refused = await approve("REQ-10477", { size: "L" });
    expect(refused.body.errors?.map((e) => e.code)).toEqual(["limit_exceeded"]);
    expect(refused.body.errors?.[0]?.message).toMatch(
      /^Deployments in Web Platform may have at most 4 vCPU under Tight/,
    );
  });
});
