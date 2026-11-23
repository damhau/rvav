// The mocks refuse what the server refuses, at the field it names: IP ranges
// (internal/core/ip_ranges.go), Active Directory OUs (ad_placements.go), NSX
// Managers (nsx_managers_admin.go), ServiceNow (servicenow.go) and
// integration settings (config.CheckSettings); and the placement preview
// prefers a network's IP ranges, as ip_range_placement.go does.
import { describe, expect, it } from "vitest";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adminState } from "./administration";
import { keptFromDeletion } from "./day2";
import { VCENTER } from "./inventory";
import { db } from "./seed";

type S = components["schemas"];

const fields = async (p: Promise<unknown>) => {
  const e = (await p.then(
    () => ({}),
    (x: unknown) => x,
  )) as { status?: number; fieldErrors?: S["FieldError"][] };
  return { status: e.status, at: (e.fieldErrors ?? []).map((f) => `${f.field} ${f.code}`).sort() };
};

const lab = (over: Partial<S["IpRangeWrite"]> = {}): S["IpRangeWrite"] => ({
  name: "Lab two",
  vcenter: VCENTER,
  network: "dvpg-app-tst-vl410",
  cidr: "10.41.10.0/24",
  start_address: "10.41.10.150",
  end_address: "10.41.10.160",
  gateway: "10.41.10.1",
  dns_servers: ["10.41.10.53"],
  dns_search_domains: ["corp.example"],
  dns_domain: "lab.corp.example",
  excluded: [],
  ...over,
});

const without = (permission: string) => {
  db.me.permissions = db.me.permissions.filter((p) => p !== permission);
};

describe("the IP range mock", () => {
  it("refuses a subnet with host bits set, and a gateway at its network or broadcast address", async () => {
    expect(await fields(call(api.POST("/admin/ip-ranges", { body: lab({ cidr: "10.41.10.7/24" }) })))).toEqual({
      status: 422,
      at: ["/cidr invalid"],
    });
    expect(await fields(call(api.POST("/admin/ip-ranges", { body: lab({ gateway: "10.41.10.0" }) })))).toEqual({
      status: 422,
      at: ["/gateway invalid"],
    });
    expect(await fields(call(api.POST("/admin/ip-ranges", { body: lab({ gateway: "10.41.10.255" }) })))).toEqual({
      status: 422,
      at: ["/gateway invalid"],
    });
  });

  it("checks the DNS domains, and says by field what another range of the network has otherwise", async () => {
    const bad = lab({ dns_search_domains: ["corp_example"], dns_domain: "-lab.corp.example" });
    expect(await fields(call(api.POST("/admin/ip-ranges", { body: bad })))).toEqual({
      status: 422,
      at: ["/dns_domain invalid", "/dns_search_domains/0 invalid"],
    });
    const other = lab({ dns_servers: ["10.41.10.54"], dns_search_domains: [], dns_domain: "corp.example" });
    expect(await fields(call(api.POST("/admin/ip-ranges", { body: other })))).toEqual({
      status: 422,
      at: ["/dns_domain differs", "/dns_search_domains differs", "/dns_servers differs"],
    });
  });

  it("says an address a VM holds at the field that would lose it, the gateway and the end included", async () => {
    const range = await call(api.GET("/admin/ip-ranges/{id}", { params: { path: { id: "ipr_lab" } } }));
    const body: S["IpRangeWrite"] = {
      ...lab(),
      name: range.name,
      start_address: "10.41.10.201",
      end_address: range.end_address,
      excluded: range.excluded,
    };
    const put = (over: Partial<S["IpRangeWrite"]>) =>
      fields(
        call(api.PUT("/admin/ip-ranges/{id}", { params: { path: { id: "ipr_lab" } }, body: { ...body, ...over } })),
      );
    expect(await put({})).toEqual({ status: 422, at: ["/start_address in_use"] });
    const kept = { start_address: range.start_address };
    expect(await put({ ...kept, gateway: "10.41.10.2" })).toEqual({ status: 422, at: ["/gateway in_use"] });
    expect(await put({ ...kept, end_address: "10.41.10.204" })).toEqual({ status: 422, at: ["/end_address in_use"] });
    expect(await put({ ...kept, excluded: [...range.excluded, "10.41.10.205"] })).toEqual({
      status: 422,
      at: ["/excluded/2 in_use"],
    });
  });

  it("refuses a field the write doesn't take, and a caller who doesn't manage the infrastructure", async () => {
    await expect(
      call(api.POST("/admin/ip-ranges", { body: { ...lab(), colour: "red" } as S["IpRangeWrite"] })),
    ).rejects.toMatchObject({ status: 400 });
    without("infrastructure.manage");
    await expect(call(api.POST("/admin/ip-ranges", { body: lab() }))).rejects.toMatchObject({ status: 403 });
    await expect(
      call(api.DELETE("/admin/ip-ranges/{id}", { params: { path: { id: "ipr_lab" } } })),
    ).rejects.toMatchObject({ status: 403 });
  });

  it("is what the placement preview gives a network, before EfficientIP's subnet", async () => {
    const preview = await call(
      api.GET("/admin/placement-preview", { params: { query: { project_id: "prj_web", environment: "tst" } } }),
    );
    expect(preview.networks.find((n) => n.name === "dvpg-app-tst-vl410")).toMatchObject({
      subnet: "10.41.10.0/24",
      dns_zone: "lab.corp.example",
      source: "rvav",
    });
  });

  it("hints each VM a request adds its own next address of the network's ranges, across components (#209)", async () => {
    const form = await call(
      api.GET("/catalog/items/{itemId}/form", {
        params: { path: { itemId: "web-app" }, query: { project_id: "prj_web" } },
      }),
    );
    const inputs = { ...form.initial_values, app: "shop", web_nodes: 2, network: "dvpg-app-tst-vl410" };
    const preview = await call(
      api.POST("/requests/preview", {
        body: {
          catalog_item_id: form.item_id,
          version_id: form.version_id,
          project_id: "prj_web",
          environment: "tst",
          count: 1,
          inputs,
        },
      }),
    );
    // 10.41.10.200 to .202 and .205 are given.
    expect(preview.components?.map((c) => c.nics[0])).toMatchObject([
      { ipam: "ip_range", address: "10.41.10.203", addresses: ["10.41.10.203", "10.41.10.204"] },
      { ipam: "ip_range", address: "10.41.10.206", addresses: ["10.41.10.206"] },
    ]);
  });
});

describe("the Active Directory OU mock", () => {
  const ou = (over: Partial<S["AdPlacementWrite"]> = {}): S["AdPlacementWrite"] => ({
    project_id: "prj_web",
    environment: "dev",
    ou: "OU=Dev,OU=Web",
    groups: [],
    allow_blueprint_ou: false,
    ...over,
  });

  it("refuses an environment there is none of, and an OU or group the directory hasn't", async () => {
    expect(
      await fields(call(api.POST("/admin/ad-placements", { body: ou({ project_id: null, environment: "qa" }) }))),
    ).toEqual({ status: 422, at: ["/environment unknown"] });
    const missing = ou({ ou: "OU=Missing,OU=Web", groups: ["CN=Web servers,OU=Groups", "CN=Missing,OU=Groups"] });
    expect(await fields(call(api.POST("/admin/ad-placements", { body: missing })))).toEqual({
      status: 422,
      at: ["/groups/1 unknown", "/ou unknown"],
    });
  });

  it("refuses a field the write doesn't take, and every project's OU to a caller placing VMs in some only", async () => {
    await expect(
      call(api.POST("/admin/ad-placements", { body: { ...ou(), group: [] } as S["AdPlacementWrite"] })),
    ).rejects.toMatchObject({ status: 400 });
    without("placement.manage");
    await expect(call(api.POST("/admin/ad-placements", { body: ou({ project_id: null }) }))).rejects.toMatchObject({
      status: 403,
    });
    await expect(
      call(api.DELETE("/admin/ad-placements/{id}", { params: { path: { id: "adp_prd" } } })),
    ).rejects.toMatchObject({ status: 403 });
  });

  it("answers 502, with no field, when the directory can't be reached", async () => {
    adminState(db).integrations.ad!.settings.url = "ldaps://unreachable.corp.example";
    await expect(call(api.POST("/admin/ad-placements", { body: ou() }))).rejects.toMatchObject({
      status: 502,
      fieldErrors: [],
    });
  });
});

describe("the NSX Manager mock", () => {
  const nsx02: S["NsxManagerWrite"] = {
    name: "nsx02.corp.example",
    url: "https://nsx02.corp.example",
    username: "admin",
    password: "secret",
  };

  it("checks a write it tests as one it saves", async () => {
    const body = { ...nsx02, url: "http://nsx02.corp.example", password: "" };
    expect(
      await fields(
        call(api.POST("/admin/nsx-managers/{name}/test", { params: { path: { name: "nsx02.corp.example" } }, body })),
      ),
    ).toEqual({ status: 422, at: ["/password required", "/url invalid"] });
    await expect(
      call(api.POST("/admin/nsx-managers", { body: { ...nsx02, passwrd: "x" } as S["NsxManagerWrite"] })),
    ).rejects.toMatchObject({ status: 400 });
  });

  it("keeps one deployments have NSX objects on", async () => {
    await call(api.POST("/admin/nsx-managers", { body: nsx02 }));
    adminState(db).nsxObjects["nsx02.corp.example"] = [db.deployments[0]!.id];
    await expect(
      call(api.DELETE("/admin/nsx-managers/{name}", { params: { path: { name: "nsx02.corp.example" } } })),
    ).rejects.toMatchObject({
      status: 409,
      problem: {
        detail:
          "Deployments have NSX segments or security groups on nsx02.corp.example: delete them, or keep the NSX Manager.",
      },
    });
  });
});

describe("the onboarding mock", () => {
  it("keeps a VM adopted as kept from deletion from Delete, until an administrator allows it", async () => {
    const made = await call(
      api.POST("/admin/onboarding", {
        body: {
          vms: [{ vcenter: VCENTER, moref: "vm-9152", prevent_delete: true }],
          project_id: "prj_web",
          environment: "dev",
          owner_id: db.me.user.id,
          catalog_item_id: db.items.find((i) => i.slug === "rhel9-server")!.id,
        },
      }),
    );
    const deploymentId = made.deployments[0]!.id;
    const actions = async () =>
      (await call(api.GET("/deployments/{deploymentId}/actions", { params: { path: { deploymentId } } }))).items.filter(
        (a) => a.key === "delete" || a.key === "allow_delete",
      );
    expect(await actions()).toMatchObject([
      { key: "allow_delete", enabled: true },
      {
        key: "delete",
        enabled: false,
        disabled_reason:
          "Delete protection keeps app-old01 from being deleted: an administrator allows it first (Allow deletion).",
      },
    ]);
    keptFromDeletion(db)[deploymentId] = [];
    expect(await actions()).toMatchObject([
      { key: "allow_delete", enabled: false, disabled_reason: "Nothing keeps its VMs from being deleted." },
      { key: "delete", enabled: true },
    ]);
  });
});

describe("the ServiceNow and integration settings mocks", () => {
  it("keep the CMDB on with no secret, and reconcile again after the sync interval", async () => {
    const it = adminState(db).integrations.servicenow!;
    it.secret = false;
    it.settings.sync_interval = "1h30m";
    const r = await call(api.POST("/admin/integrations/servicenow/cmdb/reconcile"));
    expect(r.enabled).toBe(true);
    expect(Date.parse(r.next_reconcile_at!) - Date.parse(r.reconciled_at!)).toBe(90 * 60_000);
  });

  it("audit a new token with when it expires, and its revocation on the integration", async () => {
    const made = await call(api.POST("/admin/integrations/servicenow/token"));
    await call(api.DELETE("/admin/integrations/servicenow/token"));
    const audit = async (action: string) => {
      const page = await call(api.GET("/audit", { params: { query: { action } } }));
      return typeof page === "string" ? undefined : page.items[0];
    };
    expect(await audit("servicenow.token")).toMatchObject({
      target_id: made.token.id,
      detail: { revoked: 1, expires_at: made.token.expires_at },
    });
    expect(await audit("servicenow.token_revoked")).toMatchObject({
      target_type: "integration",
      target_id: "servicenow",
    });
  });

  it("refuse the token and the CMDB to a caller who doesn't manage integrations", async () => {
    without("integrations.manage");
    await expect(call(api.GET("/admin/integrations/servicenow/token"))).rejects.toMatchObject({ status: 403 });
    await expect(call(api.POST("/admin/integrations/servicenow/token"))).rejects.toMatchObject({ status: 403 });
    await expect(call(api.DELETE("/admin/integrations/servicenow/token"))).rejects.toMatchObject({ status: 403 });
    await expect(call(api.GET("/admin/integrations/servicenow/cmdb"))).rejects.toMatchObject({ status: 403 });
  });

  it("refuse an AD base DN with no DC, and a ServiceNow data source its queries can't hold", async () => {
    const put = (kind: "ad" | "servicenow", settings: Record<string, unknown>) =>
      fields(call(api.PUT("/admin/integrations/{kind}", { params: { path: { kind } }, body: { settings } })));
    expect(await put("ad", { base_dn: "OU=Servers" })).toEqual({ status: 422, at: ["/settings/base_dn invalid"] });
    expect(await put("servicenow", { data_source: "rvav^ORsys_id" })).toEqual({
      status: 422,
      at: ["/settings/data_source invalid"],
    });
  });
});
