import { describe, expect, it } from "vitest";
import type { DeploymentDetail, ServiceRequest, UpdatePreview, VmResource } from "@/lib/api/types";
import { db } from "./seed";

// The mocks' update of a deployment (ADR-0042), as internal/core/update.go
// plans it: in place where it can, refused where it can't, VMs added and
// removed with names that continue.

const api = async <T>(method: string, path: string, body?: unknown): Promise<T> => {
  const res = await fetch(`http://localhost/api/v1${path}`, {
    method,
    headers: {
      Authorization: "Bearer test",
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok)
    throw Object.assign(new Error(`${method} ${path}: ${res.status}`), { body: (await res.json()) as unknown });
  return (await res.json()) as T;
};

const until = async <T>(what: string, read: () => Promise<T | undefined>): Promise<T> => {
  for (let i = 0; i < 500; i++) {
    const got = await read();
    if (got !== undefined) return got;
    await new Promise((r) => setTimeout(r, 10));
  }
  throw new Error(what);
};

const named = (name: string) => db.deployments.find((d) => d.name === name)!;
const vms = (d: DeploymentDetail) =>
  d.resources.filter((r): r is VmResource => r.type === "vsphere.vm").map((v) => v.name);
const preview = (d: DeploymentDetail, inputs: Record<string, unknown>) =>
  api<UpdatePreview>("POST", `/deployments/${d.id}/update/preview`, { version_id: d.version.id, inputs });
const apply = async (d: DeploymentDetail, inputs: Record<string, unknown>) => {
  const r = await api<ServiceRequest>("POST", `/deployments/${d.id}/update`, { version_id: d.version.id, inputs });
  return until(`${r.number} done`, async () => {
    const now = await api<ServiceRequest>("GET", `/requests/${r.number}`);
    return now.status === "succeeded" ? now : undefined;
  });
};

describe("the mocks' updates", () => {
  it("rerun every job on a failed deployment, resize it, and leave it ready", async () => {
    const d = named("web-dev-worker");
    const p = await preview(d, { ...d.inputs, size: "M" });
    expect(p.allowed).toBe(true);
    expect(p.changes.map((c) => c.title)).toEqual([
      "Set 4 vCPU and 16 GB on web-dev-worker002",
      "Run the AWX job linux-baseline",
      "Run the AWX job idm-join",
    ]);
    expect(p.quota.map((q) => [q.resource, q.adding])).toEqual([
      ["vcpu", 2],
      ["memory_gb", 12],
    ]);

    const r = await apply(d, { ...d.inputs, size: "M" });
    expect(r.kind).toBe("update");
    const after = await api<DeploymentDetail>("GET", `/deployments/${d.id}`);
    expect(after.status).toBe("ready");
    expect(after.inputs.size).toBe("M");
    const vm = after.resources.find((x): x is VmResource => x.type === "vsphere.vm")!;
    expect([vm.vm.cpu_count, vm.vm.memory_mb]).toEqual([4, 16 * 1024]);
  });

  it("refuse what they can't change in place, and an update that changes nothing", async () => {
    const d = named("web-dev-api");
    const moved = await preview(d, { ...d.inputs, network: "dvpg-app-tst-vl410" });
    expect(moved.refused.map((x) => x.setting)).toEqual(["network"]);
    const grown = await preview(d, { ...d.inputs, size: "L" });
    expect(grown.refused[0]?.reason).toMatch(/has snapshots/);

    await expect(
      api("POST", `/deployments/${d.id}/update`, { version_id: d.version.id, inputs: d.inputs }),
    ).rejects.toMatchObject({ body: { errors: [{ code: "no_change" }] } });
  });

  it("list each VM's changes in place together, then the jobs, as the server does", async () => {
    const d = named("web-prd-cache");
    const p = await preview(d, { ...d.inputs, size: "L", post_config: ["linux-baseline", "idm-join"] });
    expect(p.changes.map((c) => [c.kind, c.vm ?? null])).toEqual([
      ["disk_extend", "web-prd-cache001"],
      ["resize", "web-prd-cache001"],
      ["disk_extend", "web-prd-cache002"],
      ["resize", "web-prd-cache002"],
      ["job", null],
    ]);
  });

  it("start a deployment adopted without inputs from its version's initial values, refusing nothing", async () => {
    const item = db.items.find((i) => i.slug === "rhel9-server")!;
    await api("POST", "/admin/onboarding", {
      vms: [{ vcenter: "vc01.corp.example", moref: "vm-9152" }],
      project_id: "prj_web",
      environment: "dev",
      owner_id: db.me.user.id,
      catalog_item_id: item.id,
    });
    const d = named("app-old01");
    expect(d.inputs).toEqual({});
    const form = db.forms[item.id]!;
    // What the form sends before any edit: its initial values, and the first cluster, datastore and network.
    const p = await preview(d, {
      ...form.initial_values,
      cluster: "GVA-DEV-CL01",
      datastore: "vsan-gva-dev-cl01",
      network: "dvpg-app-dev-vl310",
    });
    expect(p.refused).toEqual([]);
    expect(p.changes).toEqual([]);
    const moved = await preview(d, { ...form.initial_values, cluster: "GVA-TST-CL01" });
    expect(moved.refused.map((x) => x.setting)).toEqual(["cluster"]);
  });

  it("offer no update of the PostgreSQL cluster's deployment, an item made of others", async () => {
    const d = named("data-prd-pg-ledger");
    expect(d.catalog_item.slug).toBe("postgres-cluster");
    await expect(preview(d, d.inputs)).rejects.toThrow(/: 409$/);
  });

  it("scale a multi-VM deployment out and in, the names continuing", async () => {
    const draft = {
      catalog_item_id: "itm_webapp",
      version_id: db.items.find((i) => i.id === "itm_webapp")!.version.id,
      project_id: "prj_web",
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
    };
    const req = await api<ServiceRequest>("POST", "/requests", draft);
    const built = await until("built", async () => {
      const r = await api<ServiceRequest>("GET", `/requests/${req.number}`);
      return r.status === "succeeded" ? r : undefined;
    });
    const d = db.deployments.find((x) => x.id === built.deployment_ids?.[0])!;
    const web = vms(d).filter((n) => n.includes("shopweb"));
    expect(web).toHaveLength(2);

    const out = await preview(d, { ...d.inputs, web_nodes: 3 });
    expect(out.changes.map((c) => c.kind)).toEqual(["vm_add"]);
    await apply(d, { ...d.inputs, web_nodes: 3 });
    const third = vms(d).filter((n) => n.includes("shopweb"));
    expect(third).toHaveLength(3);
    expect(Number(third[2]!.slice(-3))).toBe(Number(web[1]!.slice(-3)) + 1);

    const inward = await preview(d, { ...d.inputs, web_nodes: 1 });
    expect(inward.changes.map((c) => [c.kind, c.vm, c.destructive])).toEqual([
      ["vm_remove", third[1], true],
      ["vm_remove", third[2], true],
    ]);
    expect(inward.quota.find((q) => q.resource === "vm_count")?.adding).toBe(-2);
    const disks = (name: string) =>
      d.resources.find((r): r is VmResource => r.type === "vsphere.vm" && r.name === name)!.vm.disks;
    const freed = [third[1]!, third[2]!].flatMap(disks).reduce((n, x) => n + x.capacity_gb, 0);
    expect(inward.quota.find((q) => q.resource === "storage_gb")?.adding).toBe(-freed);
    await apply(d, { ...d.inputs, web_nodes: 1 });
    expect(vms(d).filter((n) => n.includes("shopweb"))).toEqual([third[0]]);
    expect(d.resources.some((r) => r.type === "dns.record" && r.name.startsWith(`${third[2]}.`))).toBe(false);
  });
});
