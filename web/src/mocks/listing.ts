// The deployments list's filters, the Resources list of VMs and saved
// views in the mock backend (X1.2), over the seed's deployments.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];

function views(db: Db): S["SavedView"][] {
  const d = db as Db & { views?: S["SavedView"][] };
  d.views ??= [];
  return d.views;
}

const problem = (status: number, title: string, errors?: S["FieldError"][]) =>
  HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );

function paged<T>(items: T[], url: URL) {
  const limit = Math.min(200, Math.max(1, Number(url.searchParams.get("limit") ?? 50)));
  const offset = Number(atob(url.searchParams.get("cursor") ?? "") || 0);
  return {
    items: items.slice(offset, offset + limit),
    next_cursor: offset + limit < items.length ? btoa(String(offset + limit)) : null,
  };
}

const csv = (url: URL, name: string) =>
  (url.searchParams.get(name) ?? "")
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);

/** Compares two sort keys as the server does: text, a number padded. */
const compare = (a: string | number, b: string | number) =>
  typeof a === "number" && typeof b === "number" ? a - b : String(a).localeCompare(String(b));

/** The deployments of the person's projects, or of one of them, that aren't deleted. */
/** Whoever sees the project sees its deployments, or only their own where it shares none (ADR-0045). */
export function seesDeployment(db: Db, d: { project: { id: string }; owner: { id: string } }) {
  const held = db.me.projects.find((p) => p.project.id === d.project.id);
  if (!held) return false;
  return (
    db.projects.find((p) => p.id === d.project.id)?.deployment_sharing !== "owner_only" ||
    d.owner.id === db.me.user.id ||
    held.permissions.includes("deployments.read_all") ||
    db.me.permissions.includes("deployments.read_all")
  );
}

export function visibleDeployments(db: Db, projectId: string | null) {
  return db.deployments.filter(
    (d) => seesDeployment(db, d) && d.status !== "deleted" && (!projectId || d.project.id === projectId),
  );
}

const isVm = (r: S["Resource"]): r is S["VmResource"] => r.type === "vsphere.vm";

function vmItems(db: Db, projectId: string | null): S["VmListItem"][] {
  return visibleDeployments(db, projectId).flatMap((d) =>
    d.resources.filter(isVm).map((r) => ({
      ...r,
      deployment: { id: d.id, name: d.name, status: d.status },
      project: d.project,
      environment: d.environment,
      addresses: [...new Set(r.vm.nics.flatMap((n) => n.ip_addresses ?? []))],
    })),
  );
}

const vmSortKey: Record<string, (v: S["VmListItem"]) => string | number> = {
  name: (v) => v.name,
  deployment: (v) => v.deployment.name,
  project: (v) => v.project.name,
  environment: (v) => v.environment,
  power_state: (v) => v.vm.power_state,
  cpu_count: (v) => v.vm.cpu_count,
  memory_mb: (v) => v.vm.memory_mb,
  disk_gb: (v) => v.vm.disks.reduce((n, d) => n + d.capacity_gb, 0),
};

export function listingHandlers(db: Db, base: string, wait: () => Promise<void>) {
  return [
    http.get(`${base}/deployments/facets`, async ({ request }) => {
      await wait();
      const deps = visibleDeployments(db, new URL(request.url).searchParams.get("project_id"));
      const items = new Map(deps.map((d) => [d.catalog_item.id, d.catalog_item]));
      const templates = new Set(
        deps.flatMap((d) => d.resources.filter(isVm).map((r) => r.vm.template ?? "")).filter(Boolean),
      );
      return HttpResponse.json<S["DeploymentFacets"]>({
        items: [...items.values()].sort((a, b) => a.name.localeCompare(b.name)),
        templates: [...templates].sort(),
        environments: [...new Set(deps.map((d) => d.environment))].sort(),
      });
    }),

    http.get(`${base}/resources/vms/facets`, async ({ request }) => {
      await wait();
      const vms = vmItems(db, new URL(request.url).searchParams.get("project_id"));
      const clusters = new Map<string, Set<string>>();
      for (const v of vms) {
        if (!v.vm.vcenter) continue;
        const set = clusters.get(v.vm.vcenter) ?? new Set<string>();
        if (v.vm.cluster) set.add(v.vm.cluster);
        clusters.set(v.vm.vcenter, set);
      }
      return HttpResponse.json<S["VmFacets"]>({
        vcenters: [...clusters.entries()]
          .sort(([a], [b]) => a.localeCompare(b))
          .map(([name, set]) => ({ name, clusters: [...set].sort() })),
        networks: [...new Set(vms.flatMap((v) => v.vm.nics.map((n) => n.network)))].filter(Boolean).sort(),
      });
    }),

    http.get(`${base}/resources/vms`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const envs = csv(url, "environment");
      const power = csv(url, "power_state");
      const vcenter = url.searchParams.get("vcenter");
      const cluster = url.searchParams.get("cluster");
      const network = url.searchParams.get("network");
      const q = (url.searchParams.get("q") ?? "").trim().toLowerCase();
      const sort = url.searchParams.get("sort") ?? "name";
      const desc = sort.startsWith("-");
      const key = vmSortKey[sort.replace(/^-/, "")] ?? vmSortKey.name!;
      const items = vmItems(db, url.searchParams.get("project_id"))
        .filter((v) => envs.length === 0 || envs.includes(v.environment))
        .filter((v) => power.length === 0 || power.includes(v.vm.power_state))
        .filter((v) => !vcenter || v.vm.vcenter === vcenter)
        .filter((v) => !cluster || v.vm.cluster === cluster)
        .filter((v) => !network || v.vm.nics.some((n) => n.network === network))
        .filter((v) => !q || [v.name, v.vm.fqdn ?? "", ...v.addresses].join(" ").toLowerCase().includes(q))
        .sort((a, b) => (compare(key(a), key(b)) || a.name.localeCompare(b.name)) * (desc ? -1 : 1));
      return HttpResponse.json<S["VmPage"]>(paged(items, url));
    }),

    http.get(`${base}/me/views`, async ({ request }) => {
      await wait();
      const list = new URL(request.url).searchParams.get("list");
      return HttpResponse.json<S["SavedViewList"]>({
        items: views(db)
          .filter((v) => v.list === list)
          .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())),
      });
    }),
    http.post(`${base}/me/views`, async ({ request }) => {
      await wait();
      return save(db, null, (await request.json()) as S["SavedViewWrite"]);
    }),
    http.put(`${base}/me/views/:id`, async ({ params, request }) => {
      await wait();
      if (!views(db).some((v) => v.id === params.id)) return problem(404, "Not found");
      return save(db, String(params.id), (await request.json()) as S["SavedViewWrite"]);
    }),
    http.delete(`${base}/me/views/:id`, async ({ params }) => {
      await wait();
      const list = views(db);
      const at = list.findIndex((v) => v.id === params.id);
      if (at < 0) return problem(404, "Not found");
      list.splice(at, 1);
      return new HttpResponse(null, { status: 204 });
    }),
  ];
}

function save(db: Db, id: string | null, w: S["SavedViewWrite"]) {
  const name = w.name.trim();
  if (!name) {
    return problem(422, "Some inputs need attention", [
      { field: "/name", code: "required", message: "Give the view a name." },
    ]);
  }
  const list = views(db);
  if (list.some((v) => v.list === w.list && v.name === name && v.id !== id)) {
    return problem(422, "Some inputs need attention", [
      { field: "/name", code: "taken", message: "You have a view with this name: pick another, or update that one." },
    ]);
  }
  const now = new Date().toISOString();
  const existing = list.find((v) => v.id === id);
  const saved: S["SavedView"] = {
    id: existing?.id ?? `viw_${Math.random().toString(36).slice(2, 10)}`,
    list: w.list,
    name,
    query: w.query,
    created_at: existing?.created_at ?? now,
    updated_at: now,
  };
  if (existing) list.splice(list.indexOf(existing), 1, saved);
  else list.push(saved);
  return HttpResponse.json(saved, { status: existing ? 200 : 201 });
}
