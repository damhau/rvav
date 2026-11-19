// The mocks' deployment sharing and soft policies against the server's
// (internal/e2e TestDeploymentSharing, TestProjectPolicies and
// TestPolicyEnforcement, ADR-0045): an owners-only project hides others'
// deployments from its members, project admins write their projects' own
// policies only, and a soft policy warns until the requester goes on.
import { afterEach, describe, expect, it } from "vitest";
import { api, call, isApiError } from "@/lib/api/client";
import type { RequestDraft } from "@/lib/api/types";
import { db, resetDb } from "./seed";

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
    const warning = {
      policy: "Powering off web VMs",
      message: "Web Platform's VMs serve the checkout: tell the on-call team before you power one off.",
    };
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
      fieldErrors: [{ field: "/accept_warnings", code: "warning", message: warning.message }],
    });
    const req = await run(true);
    expect(req.warnings).toEqual([warning]);
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
    const warning = { policy: "Lease in Development", message: "Leases in Development should be at most 90 days." };
    expect(preview.lease).toMatchObject({ max_days: 90, enforcement: "soft" });
    expect(preview.warnings).toEqual([warning]);
    await expect(call(api.POST("/requests", { params: key(), body: draft }))).rejects.toMatchObject({
      status: 422,
      fieldErrors: [{ field: "/accept_warnings", code: "warning", message: warning.message }],
    });
    const req = await call(api.POST("/requests", { params: key(), body: { ...draft, accept_warnings: true } }));
    expect(req.warnings).toEqual([warning]);
  });
});
