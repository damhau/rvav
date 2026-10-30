// The vRA importer in the mock backend (P2.5): an estate already read, the
// dry run from it, project mapping and an import that can run again.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];

interface State {
  snapshot: S["VraSnapshot"];
  map: Record<string, string>;
  imported: Set<string>;
  adopted: Set<string>;
}

const templates: { id: string; name: string; slug: string; status: S["VraItemPlan"]["status"]; version?: string; shared: string[]; notes: S["VraNote"][] }[] = [
  { id: "bp-rhel9", name: "RHEL 9 Server", slug: "rhel-9-server", status: "complete", version: "2.0", shared: ["vra-prj-web", "vra-prj-data", "vra-prj-legacy"],
    notes: [{ level: "info", where: "naming", text: "vRA's machine naming of Web Platform: web-{environment}-{inputs.role}{seq:03}." },
      { level: "warning", where: "resources.Cloud_vSphere_Machine_1.constraints", text: "Placement by the tag env:${input.environment}: write an rvav placement rule for it." }] },
  { id: "bp-win", name: "Windows Server 2022", slug: "windows-server-2022", status: "complete", version: "3.0", shared: ["vra-prj-web"],
    notes: [{ level: "warning", where: "naming", text: "The project's naming template can't be translated: rvav's default naming applies." }] },
  { id: "bp-app", name: "Three-tier application", slug: "three-tier-application", status: "partial", version: "1.0", shared: ["vra-prj-web"],
    notes: [{ level: "unmapped", where: "resources.lb", text: "Cloud.NSX.LoadBalancer has no rvav equivalent: an AWX job in a composite item can make it." }] },
  { id: "bp-db", name: "Database server", slug: "database-server", status: "partial", version: "1.0", shared: ["vra-prj-data"],
    notes: [{ level: "unmapped", where: "form.dbVersion", text: "Its choices come from the vRO action com.corp.db/getVersions: give the field an rvav value source." }] },
  { id: "bp-draft", name: "Work in progress", slug: "work-in-progress", status: "skipped", shared: [],
    notes: [{ level: "unmapped", where: "versions", text: "No released version: nothing to import." }] },
]; // prettier-ignore

const vraProjects = [
  { id: "vra-prj-web", name: "Web Platform", deployments: 3 },
  { id: "vra-prj-data", name: "Data Services", deployments: 1 },
  { id: "vra-prj-legacy", name: "Legacy Apps", deployments: 1 },
];

function state(db: Db): State {
  const d = db as Db & { vra?: State };
  d.vra ??= {
    snapshot: {
      id: "vrs_mock1", read_at: new Date(Date.now() - 3600_000).toISOString(), source: "vra", url: "https://vra.corp.example",
      created_at: new Date(Date.now() - 3600_000).toISOString(), created_by: "Marc Keller",
      counts: { projects: 3, templates: 5, catalog_items: 6, deployments: 5, policies: 3, subscriptions: 4, vro_workflows: 7, vro_actions: 4 },
    },
    map: {},
    imported: new Set(),
    adopted: new Set(),
  }; // prettier-ignore
  return d.vra;
}

function rvavOf(db: Db, st: State, vra: { id: string; name: string }): [S["VraProjectPlan"]["how"], string?, string?] {
  const mapped = st.map[vra.id] && db.projects.find((p) => p.id === st.map[vra.id]);
  if (mapped) return ["mapped", mapped.id, mapped.name];
  const same = db.projects.find((p) => p.name.toLowerCase() === vra.name.toLowerCase());
  return same ? ["same_name", same.id, same.name] : ["none"];
}

function plan(db: Db, st: State): S["VraPlan"] {
  const projects: S["VraProjectPlan"][] = vraProjects.map((v) => {
    const [how, id, name] = rvavOf(db, st, v);
    const items = templates.filter((t) => t.shared.includes(v.id)).length;
    return {
      vra_id: v.id,
      name: v.name,
      how,
      rvav_project_id: id,
      rvav_project_name: name,
      items,
      deployments: v.deployments,
    };
  });
  const rvavOfVra = (id: string) => projects.find((p) => p.vra_id === id)?.rvav_project_id;
  const items: S["VraItemPlan"][] = templates.map((t) => {
    const done = st.imported.has(t.id);
    const mapped = t.shared.map(rvavOfVra);
    return {
      blueprint_id: t.id, name: t.name, slug: t.slug, status: t.status, version: t.version,
      action: t.status === "skipped" ? "skip" : done ? "unchanged" : "create",
      rvav_item_id: done ? `itm_${t.slug}` : undefined, notes: t.notes,
      share_add: done || t.status === "skipped" ? [] : mapped.filter((x): x is string => Boolean(x)),
      share_unmapped: t.shared.filter((_, i) => !mapped[i]).map((id) => vraProjects.find((p) => p.id === id)!.name),
    };
  }); // prettier-ignore
  const dep = (vra_id: string, name: string, project: string, owner: string, vms: string[], why?: string): S["VraDeploymentPlan"] => {
    const adopted = st.adopted.has(vra_id);
    const ready = !why && st.imported.has("bp-rhel9");
    return {
      vra_id, name, vra_project: project, owner, environment: "dev",
      status: adopted ? "adopted" : ready ? "adopt" : "skipped",
      vms: vms.map((n, i) => ({ vcenter: "vc01.corp.example", moref: `vm-${7100 + i}`, name: n })),
      notes: why || !ready ? [{ level: "unmapped", where: why ? "vms" : "item", text: why ?? "The item it was requested from isn't imported into rvav: import its cloud template first." }] : [],
    };
  }; // prettier-ignore
  const deployments = [
    dep("dep-vra-1", "vra-web-001", "Web Platform", "thomas.nguyen@corp.example", ["vra-web-001"]),
    dep("dep-vra-2", "vra-web-pair", "Web Platform", "lea.martin@corp.example", ["vra-api-001", "vra-api-002"]),
    dep("dep-vra-4", "vra-gone", "Web Platform", "thomas.nguyen@corp.example", [], "Its VMs (vra-gone-001) aren't in the vCenters rvav reads."),
    dep("dep-vra-5", "vra-legacy", "Legacy Apps", "former.admin@corp.example", [], "vRA project Legacy Apps has no rvav project: map it to one, or create it."),
  ]; // prettier-ignore
  const count = (f: (x: S["VraItemPlan"]) => boolean) => items.filter(f).length;
  return {
    snapshot: st.snapshot,
    summary: {
      items_create: count((i) => i.action === "create"), items_update: 0, items_unchanged: count((i) => i.action === "unchanged"),
      items_blocked: count((i) => i.action === "skip" || i.action === "conflict"), shares_add: items.reduce((n, i) => n + i.share_add.length, 0),
      deployments_adopt: deployments.filter((d) => d.status === "adopt").length, deployments_adopted: deployments.filter((d) => d.status === "adopted").length,
      deployments_skipped: deployments.filter((d) => d.status === "skipped").length,
    },
    projects, items, deployments,
    policies: [
      { vra_id: "pol-approval-prd", name: "Production needs a lead", type: "approval", becomes: "An approval policy for catalog requests, in every project (Administration › Policies › Approvals)",
        notes: [{ level: "info", where: "criteria", text: 'Condition: environment == "prd"' }, { level: "info", where: "approvers", text: "Level 1: infra-leads@corp.example, any of them." }] },
      { vra_id: "pol-lease-dev", name: "Development leases", type: "lease", vra_project: "Web Platform", becomes: "The lease policy of the project Web Platform maps to (Administration › Policies › Leases), for each of its environments",
        notes: [{ level: "info", where: "leaseTermMax", text: "Maximum days: 30." }, { level: "info", where: "leaseGrace", text: "Grace days: 7, between the end of the lease and the deletion." }] },
      { vra_id: "pol-limit", name: "Deployment limit", type: "limit", becomes: "The sizes of the items requested in every project",
        notes: [{ level: "unmapped", where: "limits", text: "rvav has no limit per deployment: the sizes an item offers bound each request." }] },
    ],
    triage: {
      read_at: st.snapshot.read_at, groups: { A: 1, B: 2, C: 1, D: 1 }, library: 2,
      items: [
        { kind: "workflow", id: "wf-tag-vm", name: "Tag VM with cost centre", category: "Corp/vCenter", runs: 790, last_run: new Date(Date.now() - 86400_000).toISOString(),
          used_by: [{ kind: "subscription", name: "Cost centre tag", detail: "compute.provision.post, provisioning.post in rvav, blocking" }], systems: ["vCenter"],
          group: "A", becomes: "Built-in rvav steps or a composite item", reasons: ["It only drives vCenter: rvav's built-in steps (or a composite item chaining them) do the same."] },
        { kind: "workflow", id: "wf-ad-group", name: "Create AD group", category: "Corp/Active Directory", runs: 120, last_run: new Date(Date.now() - 3 * 86400_000).toISOString(),
          used_by: [{ kind: "catalog_item", name: "Create AD group" }], systems: ["Active Directory"], group: "B", becomes: "An AWX job template",
          reasons: ["It calls Active Directory: a playbook does, with microsoft.ad."] },
        { kind: "abx_action", id: "abx-backup-reg", name: "abx-backup-reg", runs: 0, used_by: [{ kind: "subscription", name: "Backup registration" }], systems: [],
          group: "B", becomes: "An AWX job template", reasons: ["An ABX action: the importer doesn't read its code."] },
        { kind: "workflow", id: "wf-decom", name: "Decommission application", category: "Corp/Lifecycle", runs: 4, last_run: new Date(Date.now() - 40 * 86400_000).toISOString(),
          used_by: [{ kind: "day2_action", name: "Decommission", detail: "on Deployment" }], systems: ["REST", "vCenter"], group: "C", becomes: "A custom Temporal workflow",
          reasons: ["It waits for people (a user interaction): a custom workflow asks with rvav's approvals."] },
        { kind: "workflow", id: "wf-old-snap", name: "Snapshot cleanup (old)", category: "Corp/Old", runs: 2, last_run: new Date(Date.now() - 400 * 86400_000).toISOString(),
          used_by: [{ kind: "subscription", name: "Old snapshot cleanup", disabled: true }], systems: ["vCenter"], group: "D", becomes: "Nothing: retire it",
          reasons: ["Only disabled subscriptions or actions use it."] },
      ],
    },
  }; // prettier-ignore
}

function problem(status: number, title: string, errors?: S["FieldError"][]) {
  return HttpResponse.json(
    { type: "https://rvav.dev/problems/x", title, status, request_id: "req_mock", ...(errors ? { errors } : {}) },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}

export function vraHandlers(db: Db, base: string, wait: () => Promise<void>) {
  return [
    http.get(`${base}/admin/vra`, async () => {
      await wait();
      return HttpResponse.json<S["VraImportStatus"]>({
        configured: true,
        url: "https://vra.corp.example",
        snapshot: state(db).snapshot,
      });
    }),
    http.post(`${base}/admin/vra/snapshots`, async ({ request }) => {
      await wait();
      const st = state(db);
      const text = await request.text();
      const now = new Date().toISOString();
      st.snapshot = {
        ...st.snapshot,
        id: `vrs_mock${Date.now()}`,
        read_at: now,
        created_at: now,
        source: text ? "file" : "vra",
      };
      return HttpResponse.json<S["VraSnapshot"]>(st.snapshot, { status: 201 });
    }),
    http.get(`${base}/admin/vra/plan`, async () => {
      await wait();
      return HttpResponse.json<S["VraPlan"]>(plan(db, state(db)));
    }),
    http.put(`${base}/admin/vra/projects/:id`, async ({ params, request }) => {
      await wait();
      const st = state(db);
      const id = String(params.id);
      if (!vraProjects.some((p) => p.id === id)) return problem(404, "Not found");
      const b = (await request.json()) as S["VraProjectMapping"];
      if (b.rvav_project_id && !db.projects.some((p) => p.id === b.rvav_project_id))
        return problem(422, "Not valid", [{ field: "/rvav_project_id", code: "unknown", message: "No such project." }]);
      if (b.rvav_project_id) st.map[id] = b.rvav_project_id;
      else delete st.map[id];
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/vra/import`, async ({ request }) => {
      await wait();
      const st = state(db);
      const b = (await request.json()) as S["VraImportRequest"];
      const before = plan(db, st);
      const out: S["VraImportResult"] = { created: [], updated: [], shared: 0, adopted: [], failed: [], plan: before };
      if (b.catalog ?? true) {
        for (const it of before.items) {
          if (it.action !== "create") continue;
          st.imported.add(it.blueprint_id);
          out.created.push(it.name);
          out.shared += it.share_add.length;
        }
      }
      if (b.deployments) {
        for (const d of plan(db, st).deployments) {
          if (d.status !== "adopt") continue;
          st.adopted.add(d.vra_id);
          out.adopted.push(d.name);
        }
      }
      out.plan = plan(db, st);
      return HttpResponse.json(out);
    }),
    http.get(`${base}/admin/vra/triage`, async () => {
      await wait();
      return new HttpResponse("# vRO triage\n\n| Group | Items |\n|---|---|\n| A. Native in rvav | 1 |\n", {
        headers: { "Content-Type": "text/markdown; charset=utf-8" },
      });
    }),
  ];
}
