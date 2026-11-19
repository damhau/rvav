// The mocks' deployment sharing and soft policies against the server's
// (internal/e2e TestDeploymentSharing, TestProjectPolicies and
// TestPolicyEnforcement, ADR-0045): an owners-only project hides others'
// deployments from its members, project admins write their projects' own
// policies only, and a soft policy warns until the requester goes on.
import { afterEach, describe, expect, it } from "vitest";
import { api, call, isApiError } from "@/lib/api/client";
import type { LiveEvent, RequestDraft } from "@/lib/api/types";
import { adminState } from "./administration";
import { matches, type Envelope } from "./events";
import { db, resetDb } from "./seed";
import { warning as named } from "./warnings";

const web = "prj_web";

const as = (persona: string) => {
  localStorage.setItem("rvav.mock.as", persona);
  resetDb();
};

afterEach(() => localStorage.removeItem("rvav.mock.as"));

const status = async (p: Promise<unknown>) => {
  try {
    await p;
    return 200;
  } catch (e) {
    return isApiError(e) ? e.status : 0;
  }
};

const webNames = async () =>
  (await call(api.GET("/deployments", { params: { query: { project_id: web, limit: 200 } } }))).items
    .map((d) => d.name)
    .sort();

const key = () => ({ header: { "Idempotency-Key": crypto.randomUUID() } });

describe("the mocks' deployment sharing", () => {
  it("shows a member of an owners-only project only their own, and those who read all of them everything", async () => {
    const all = await webNames();
    await call(
      api.PUT("/projects/{projectId}/deployment-sharing", {
        params: { path: { projectId: web } },
        body: { deployment_sharing: "owner_only" },
      }),
    );
    expect(await webNames()).toEqual(all);

    as("member");
    const own = db.deployments
      .filter((d) => d.project.id === web && d.owner.id === db.me.user.id && d.status !== "deleted")
      .map((d) => d.name)
      .sort();
    expect(own.length).toBeGreaterThan(0);
    expect(await webNames()).toEqual(own);
    const others = db.deployments.find((d) => d.name === "web-dev-api")!;
    expect(
      await status(call(api.GET("/deployments/{deploymentId}", { params: { path: { deploymentId: others.id } } }))),
    ).toBe(404);
    const requests = await call(api.GET("/requests", { params: { query: { project_id: web, limit: 200 } } }));
    expect(requests.items.every((r) => r.requester.id === db.me.user.id || r.on_behalf_of?.id === db.me.user.id)).toBe(
      true,
    );
    expect(
      await status(
        call(
          api.PUT("/projects/{projectId}/deployment-sharing", {
            params: { path: { projectId: web } },
            body: { deployment_sharing: "all_members" },
          }),
        ),
      ),
    ).toBe(403);
  });

  it("warns a member of a soft day-2 policy, and runs the action once they go on", async () => {
    as("member");
    const d = db.deployments.find((x) => x.name === "web-tst-search")!;
    const vm = d.resources.find((r) => r.name === "web-tst-search001")!;
    const { items } = await call(
      api.GET("/deployments/{deploymentId}/actions", { params: { path: { deploymentId: d.id } } }),
    );
    const off = items.find((a) => a.key === "power_off" && a.resource_id === vm.id)!;
    const warning = named(
      "Powering off web VMs",
      "Web Platform's VMs serve the checkout: tell the on-call team before you power one off.",
    );
    expect(off).toMatchObject({ enabled: true, approval: { required: false }, warnings: [warning] });

    const run = (accept: boolean) =>
      call(
        api.POST("/deployments/{deploymentId}/actions", {
          params: { path: { deploymentId: d.id }, ...key() },
          body: { action: "power_off", resource_id: vm.id, inputs: {}, ...(accept ? { accept_warnings: true } : {}) },
        }),
      );
    await expect(run(false)).rejects.toMatchObject({
      status: 422,
      fieldErrors: [{ field: "/accept_warnings", code: "warning", message: warning.message, warning }],
    });
    const req = await run(true);
    expect(req.warnings).toEqual([warning]);
  });

  it("refuses an action whose warnings changed since the menu was listed, until they are accepted", async () => {
    as("member");
    const d = db.deployments.find((x) => x.name === "web-tst-search")!;
    const vm = d.resources.find((r) => r.name === "web-tst-search001")!;
    const listed = (
      await call(api.GET("/deployments/{deploymentId}/actions", { params: { path: { deploymentId: d.id } } }))
    ).items.find((a) => a.key === "power_off" && a.resource_id === vm.id)!;
    const seen = listed.warnings!.map((w) => w.id);
    adminState(db).day2.find((p) => p.name === "Powering off web VMs")!.reason = "Ask the on-call team first.";
    const run = (body: { accepted_warnings?: string[]; accept_warnings?: boolean }) =>
      call(
        api.POST("/deployments/{deploymentId}/actions", {
          params: { path: { deploymentId: d.id }, ...key() },
          body: { action: "power_off", resource_id: vm.id, inputs: {}, ...body },
        }),
      );
    const now = named("Powering off web VMs", "Ask the on-call team first.");
    await expect(run({ accepted_warnings: seen })).rejects.toMatchObject({
      status: 422,
      fieldErrors: [{ field: "/accept_warnings", code: "warning", warning: now }],
    });
    await expect(run({ accepted_warnings: [], accept_warnings: true })).rejects.toMatchObject({ status: 422 });
    expect((await run({ accepted_warnings: [now.id] })).warnings).toEqual([now]);
  });

  it("refuses a soft allow, and a hard deny turns the action off", async () => {
    const write = (enforcement: "hard" | "soft", effect: "allow" | "deny") =>
      call(
        api.POST("/admin/day2-policies", {
          body: {
            name: `${enforcement} ${effect}`,
            priority: 1,
            actions: ["power_off"],
            roles: [],
            condition: "true",
            effect,
            enforcement,
            levels: [],
            project_id: web,
            enabled: true,
            reason: "Not today.",
          },
        }),
      );
    await expect(write("soft", "allow")).rejects.toMatchObject({
      status: 422,
      fieldErrors: [expect.objectContaining({ field: "/enforcement" })],
    });
    await write("hard", "deny");
    const d = db.deployments.find((x) => x.name === "web-tst-search")!;
    const { items } = await call(
      api.GET("/deployments/{deploymentId}/actions", { params: { path: { deploymentId: d.id } } }),
    );
    expect(items.find((a) => a.key === "power_off" && a.enabled)).toBeUndefined();
    expect(items.find((a) => a.key === "power_off" && a.resource_name === "web-tst-search001")).toMatchObject({
      disabled_reason: "Not today.",
    });
  });

  it("lets a project admin write their projects' policies only, and read every project's", async () => {
    as("project-admin");
    const body = (project_id: string | null) => ({
      name: "Deny snapshots",
      priority: 40,
      actions: ["snapshot_*"],
      roles: [],
      condition: "true",
      effect: "deny" as const,
      enforcement: "soft" as const,
      levels: [],
      project_id,
      enabled: true,
    });
    expect(await status(call(api.POST("/admin/day2-policies", { body: body(null) })))).toBe(403);
    expect(await status(call(api.POST("/admin/day2-policies", { body: body("prj_data") })))).toBe(403);
    const made = await call(api.POST("/admin/day2-policies", { body: body(web) }));
    expect(made).toMatchObject({ project_id: web, enforcement: "soft" });
    const listed = await call(api.GET("/admin/day2-policies"));
    expect(listed.items.map((p) => p.project_id ?? null).every((p) => p === null || p === web)).toBe(true);
    const global = listed.items.find((p) => !p.project_id)!;
    expect(await status(call(api.DELETE("/admin/day2-policies/{id}", { params: { path: { id: global.id } } })))).toBe(
      403,
    );
    await expect(
      call(
        api.PUT("/admin/lease-policies", {
          body: {
            environment: "dev",
            project_id: web,
            default_days: 30,
            max_days: 120,
            max_extensions: 2,
            grace_days: 7,
            expiry_action: "power_off_then_delete",
          },
        }),
      ),
    ).rejects.toMatchObject({ status: 422, fieldErrors: [expect.objectContaining({ field: "/max_days" })] });
  });

  it("warns of a longer lease where the lease is soft, and keeps the warning on the request", async () => {
    await call(
      api.PUT("/admin/lease-policies", {
        body: {
          environment: "dev",
          project_id: null,
          default_days: 30,
          max_days: 90,
          max_extensions: 2,
          grace_days: 7,
          expiry_action: "power_off_then_delete",
          enforcement: "soft",
        },
      }),
    );
    const draft: RequestDraft = {
      catalog_item_id: "itm_webapp",
      version_id: db.items.find((i) => i.id === "itm_webapp")!.version.id,
      project_id: web,
      environment: "dev",
      inputs: {
        app: "shop",
        web_nodes: 2,
        size: "M",
        cluster: "GVA-DEV-CL01",
        datastore: "vsan-gva-dev-cl01",
        network: "dvpg-app-dev-vl310",
      },
      count: 1,
      lease_days: 180,
    };
    const preview = await call(api.POST("/requests/preview", { body: draft }));
    const warning = named("Lease in Development", "Leases in Development should be at most 90 days.");
    expect(preview.lease).toMatchObject({ max_days: 90, enforcement: "soft" });
    expect(preview.warnings).toEqual([warning]);
    await expect(
      call(api.POST("/requests", { params: key(), body: { ...draft, accepted_warnings: [] } })),
    ).rejects.toMatchObject({
      status: 422,
      fieldErrors: [{ field: "/accept_warnings", code: "warning", message: warning.message, warning }],
    });
    const req = await call(
      api.POST("/requests", { params: key(), body: { ...draft, accepted_warnings: [warning.id] } }),
    );
    expect(req.warnings).toEqual([warning]);
    const scripted = await call(api.POST("/requests", { params: key(), body: { ...draft, accept_warnings: true } }));
    expect(scripted.warnings).toEqual([warning]);
  });
});

describe("what the mocks tell a member of an owners-only project", () => {
  it("streams them the events of their own requests and deployments, and of those they follow", () => {
    const env = (type: LiveEvent["type"], extra: Partial<Envelope> = {}): Envelope => ({
      event: { id: "evt_1", type, at: "", data: {} } as unknown as LiveEvent,
      projectId: web,
      ...extra,
    });
    const ownOnly = () => false;
    const theirs = env("deployment.updated", { deploymentId: "dep_1", viewers: ["usr_me"] });
    const others = env("deployment.updated", { deploymentId: "dep_2", viewers: ["usr_other"] });
    expect(matches(theirs, ["deployments"], null, "usr_me", ownOnly)).toBe(true);
    expect(matches(others, ["deployments"], null, "usr_me", ownOnly)).toBe(false);
    expect(matches(others, ["deployment:dep_2"], null, "usr_me", ownOnly)).toBe(true);
    expect(matches(others, ["deployments"], null, "usr_me", () => true)).toBe(true);
    const step = env("request.step", { requestId: "req_1", viewers: undefined });
    expect(matches(step, ["requests"], null, "usr_me", ownOnly)).toBe(false);
    expect(matches(step, ["request:req_1"], null, "usr_me", ownOnly)).toBe(true);
    // Approvals go to their approvers, whatever they see of the project.
    expect(matches(env("approval.updated", { userIds: ["usr_me"] }), ["approvals"], null, "usr_me", ownOnly)).toBe(
      true,
    );
  });

  it("notifies them only of what they see", async () => {
    as("member");
    const seen = await call(api.GET("/me/notifications"));
    const front = db.deployments.find((d) => d.name === "web-prd-front")!;
    expect(seen.items.some((n) => n.link === `/deployments/${front.id}`)).toBe(true);
    front.owner = db.deployments.find((d) => d.name === "web-dev-api")!.owner;
    const now = await call(api.GET("/me/notifications"));
    expect(now.items.some((n) => n.link === `/deployments/${front.id}`)).toBe(false);
    expect(now.unread).toBe(seen.unread - 1);
  });

  it("decides day-2 actions with the policies whose condition holds", async () => {
    as("member");
    adminState(db).day2.push({
      id: "d2p_prd_power",
      set_by_admin: true,
      name: "No power-off in production",
      reason: "Production VMs stay on: ask the on-call team.",
      priority: 5,
      actions: ["power_off"],
      roles: [],
      condition: 'environment.production && resource.type == "vsphere.vm"',
      effect: "deny",
      enforcement: "hard",
      project_id: null,
      enabled: true,
    });
    const powerOff = async (name: string) => {
      const d = db.deployments.find((x) => x.name === name)!;
      const actions = await call(
        api.GET("/deployments/{deploymentId}/actions", { params: { path: { deploymentId: d.id } } }),
      );
      return actions.items.find((a) => a.key === "power_off" && a.resource_id);
    };
    expect(await powerOff("web-prd-front")).toMatchObject({
      enabled: false,
      disabled_reason: "Production VMs stay on: ask the on-call team.",
    });
    expect(await powerOff("web-tst-search")).toMatchObject({ enabled: true });
  });
});

describe("the mocks' approval levels", () => {
  it("ask a saved policy's project role holders and the requester's manager", async () => {
    adminState(db).approvals.push({
      id: "pol_dev_web",
      set_by_admin: true,
      name: "Web Platform in development",
      reason: "Development requests in Web Platform need a look",
      condition: 'environment == "dev"',
      item_type: null,
      project_id: web,
      priority: 50,
      enabled: true,
      levels: [
        { name: "Members", roles: ["member"], mode: "any" },
        { name: "Manager", manager: true, mode: "any" },
      ],
    });
    const draft: RequestDraft = {
      catalog_item_id: "itm_webapp",
      version_id: db.items.find((i) => i.id === "itm_webapp")!.version.id,
      project_id: web,
      environment: "dev",
      inputs: {
        app: "shop",
        web_nodes: 2,
        size: "M",
        cluster: "GVA-DEV-CL01",
        datastore: "vsan-gva-dev-cl01",
        network: "dvpg-app-dev-vl310",
      },
      count: 1,
      lease_days: 30,
    };
    const preview = await call(api.POST("/requests/preview", { body: draft }));
    expect(preview.approval.policies.map((p) => p.name)).toContain("Web Platform in development");
    const req = await call(api.POST("/requests", { params: key(), body: draft }));
    expect(req.status).toBe("pending_approval");
    const levels = db.approvals.filter((a) => a.request.id === req.id);
    expect(
      levels.map((l) => ({
        name: l.level.name,
        approvers: l.approvers.map((u) => u.id),
        roles: l.roles?.map((r) => r.key),
        manager: l.manager,
      })),
    ).toEqual([
      { name: "Members", approvers: ["usr_tnguyen"], roles: ["member"], manager: undefined },
      { name: "Manager", approvers: ["usr_mkeller"], roles: undefined, manager: true },
    ]);
  });
});
