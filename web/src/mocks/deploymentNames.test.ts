// The mocks name deployments as core.evaluateDeployments does
// (internal/core/deployment_names.go): numbered when there are several,
// refused when taken whatever the case, when it is a VM name, or when the
// item makes no deployment (ADR-0019).
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { RequestDraft } from "@/lib/api/types";
import { db } from "./seed";

const web = () => db.projects.find((p) => p.code === "web")!;

async function draftFor(slug: string, extra: Partial<RequestDraft>): Promise<RequestDraft> {
  const form = await call(
    api.GET("/catalog/items/{itemId}/form", { params: { path: { itemId: slug }, query: { project_id: web().id } } }),
  );
  return {
    catalog_item_id: form.item_id,
    version_id: form.version_id,
    project_id: web().id,
    environment: "dev",
    inputs: { ...form.initial_values, role: "app" },
    ...extra,
    count: extra.count ?? 1,
  };
}

const errorsOf = async (d: RequestDraft) =>
  (await call(api.POST("/requests/preview", { body: d }))).errors.filter((e) => e.field.startsWith("/deployment"));

describe("deployment names in the mocks", () => {
  it("numbers several deployments and refuses a taken name, whatever the case", async () => {
    const form = await call(
      api.GET("/catalog/items/{itemId}/form", {
        params: { path: { itemId: "rhel9-server" }, query: { project_id: web().id } },
      }),
    );
    expect(form.names_deployments).toBe(true);
    const many = await call(
      api.POST("/requests/preview", {
        body: await draftFor("rhel9-server", { count: 3, deployment_name: " Payroll " }),
      }),
    );
    expect(many.deployment_names).toEqual(["Payroll 1", "Payroll 2", "Payroll 3"]);
    expect(await errorsOf(await draftFor("rhel9-server", { deployment_name: "Web-Prd-Front" }))).toEqual([
      {
        field: "/deployment_name",
        code: "taken",
        message: "Web Platform already has a deployment named web-prd-front.",
      },
    ]);
  });

  it("refuses a VM name, a long or two-line name, and a long description", async () => {
    const preview = await call(api.POST("/requests/preview", { body: await draftFor("rhel9-server", {}) }));
    const vm = preview.names![0]!;
    const codes = async (extra: Partial<RequestDraft>) =>
      (await errorsOf(await draftFor("rhel9-server", extra))).map((e) => `${e.field} ${e.code}`);
    expect(await codes({ deployment_name: vm.toUpperCase() })).toEqual(["/deployment_name naming"]);
    expect(await codes({ deployment_name: "é".repeat(81) })).toEqual(["/deployment_name too_long"]);
    expect(await codes({ deployment_name: "a\tb" })).toEqual(["/deployment_name invalid"]);
    expect(await codes({ deployment_name: "é".repeat(80), deployment_description: "ü".repeat(2001) })).toEqual([
      "/deployment_description too_long",
    ]);
  });

  it("refuses both for an item that makes no deployment", async () => {
    const form = await call(
      api.GET("/catalog/items/{itemId}/form", {
        params: { path: { itemId: "dns-record" }, query: { project_id: web().id } },
      }),
    );
    expect(form.names_deployments).toBeUndefined();
    const errors = await errorsOf(
      await draftFor("dns-record", { deployment_name: "Records", deployment_description: "Some" }),
    );
    expect(errors.map((e) => `${e.field} ${e.code}`).sort()).toEqual([
      "/deployment_description no_deployment",
      "/deployment_name no_deployment",
    ]);
  });
});
