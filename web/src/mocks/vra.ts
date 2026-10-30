// The vRA importer in the mock backend (P2.5, P2.7), as the server plans:
// an estate read after an earlier import (the RHEL item made, published and
// offered to Web Platform), the dry run from it, project mapping (by name,
// chosen, or kept in vRA), an import that can run again and adopts only what
// onboarding would take (a published item, offered to the project), and
// the cutover.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import type { Db } from "./seed";

type S = components["schemas"];

interface State {
  snapshot: S["VraSnapshot"];
  /** vRA project ID → rvav project ID, "" when kept in vRA. */
  map: Record<string, string>;
  imported: Set<string>;
  published: Set<string>;
  /** The shares the import made, "template/rvav project". */
  shared: Set<string>;
  adopted: Set<string>;
  moved: Record<string, { at: string; by: string; note?: string }>;
}

const templates: { id: string; name: string; slug: string; status: S["VraItemPlan"]["status"]; version?: string; shared: string[]; notes: S["VraNote"][] }[] = [
  { id: "bp-rhel9", name: "RHEL 9 Server", slug: "rhel-9-server", status: "complete", version: "2.0", shared: ["vra-prj-web", "vra-prj-data", "vra-prj-legacy"],
    notes: [{ level: "info", where: "naming", text: "vRA's machine naming of Web Platform: web-{environment}-{inputs.role}{seq:03}." },
      { level: "warning", where: "resources.Cloud_vSphere_Machine_1.constraints", text: "Placement by the tag env:${input.environment}: write an rvav placement rule for it." }] },
  { id: "bp-win", name: "Windows Server 2022", slug: "windows-server-2022", status: "complete", version: "3.0", shared: ["vra-prj-web", "vra-prj-legacy"],
    notes: [{ level: "warning", where: "naming", text: "The project's naming template can't be translated: rvav's default naming applies." }] },
  { id: "bp-app", name: "Three-tier application", slug: "three-tier-application", status: "partial", version: "1.0", shared: ["vra-prj-web", "vra-prj-legacy"],
    notes: [{ level: "unmapped", where: "resources.lb", text: "Cloud.NSX.LoadBalancer has no rvav equivalent: an AWX job in a composite item can make it." }] },
  { id: "bp-db", name: "Database server", slug: "database-server", status: "partial", version: "1.0", shared: ["vra-prj-data", "vra-prj-legacy"],
    notes: [{ level: "unmapped", where: "form.dbVersion", text: "Its choices come from the vRO action com.corp.db/getVersions: give the field an rvav value source." }] },
  { id: "bp-draft", name: "Work in progress", slug: "work-in-progress", status: "skipped", shared: [],
    notes: [{ level: "unmapped", where: "versions", text: "No released version: nothing to import." }] },
]; // prettier-ignore

const vraProjects = [
  { id: "vra-prj-web", name: "Web Platform" },
  { id: "vra-prj-data", name: "Data Services" },
  { id: "vra-prj-legacy", name: "Legacy Apps" },
];

/** vRA's deployments: their project, template, owner and VMs; a reason when they can't be adopted whatever rvav has. */
const vraDeployments: { id: string; name: string; project: string; template: string; owner: string; vms: string[]; why?: string }[] = [
  { id: "dep-vra-1", name: "vra-web-001", project: "vra-prj-web", template: "bp-rhel9", owner: "thomas.nguyen@corp.example", vms: ["vra-web-001"] },
  { id: "dep-vra-2", name: "VRA Web Pair", project: "vra-prj-web", template: "bp-rhel9", owner: "lea.martin@corp.example", vms: ["vra-api-001", "vra-api-002"] },
  { id: "dep-vra-3", name: "vra-db-001", project: "vra-prj-data", template: "bp-db", owner: "lea.martin@corp.example", vms: ["vra-db-001"] },
  { id: "dep-vra-4", name: "vra-gone", project: "vra-prj-web", template: "bp-rhel9", owner: "thomas.nguyen@corp.example", vms: [],
    why: "Its VMs (vra-gone-001) aren't in the vCenters rvav reads: deleted, or in a vCenter rvav doesn't have." },
]; // prettier-ignore

const rvavName = (n: string) => n.toLowerCase().replace(/[^a-z0-9._-]+/g, "-");

function state(db: Db): State {
  const d = db as Db & { vra?: State };
  d.vra ??= {
    snapshot: {
      id: "vrs_mock1", read_at: new Date(Date.now() - 3600_000).toISOString(), source: "vra", url: "https://vra.corp.example",
      created_at: new Date(Date.now() - 3600_000).toISOString(), created_by: "Marc Keller",
      counts: { projects: 3, templates: 5, catalog_items: 6, deployments: 4, policies: 3, subscriptions: 4, vro_workflows: 7, vro_actions: 4 },
    },
    map: {},
    imported: new Set(["bp-rhel9"]),
    published: new Set(["bp-rhel9"]),
    shared: new Set(["bp-rhel9/prj_web"]),
    adopted: new Set(),
    moved: {},
  }; // prettier-ignore
  return d.vra;
}

function rvavOf(db: Db, st: State, vra: { id: string; name: string }): [S["VraProjectPlan"]["how"], string?, string?] {
  if (vra.id in st.map) {
    const mapped = db.projects.find((p) => p.id === st.map[vra.id]);
    return mapped ? ["mapped", mapped.id, mapped.name] : st.map[vra.id] === "" ? ["stays"] : ["none"];
  }
  const same = db.projects.find((p) => p.name.toLowerCase() === vra.name.toLowerCase());
  return same ? ["same_name", same.id, same.name] : ["none"];
}

function plan(db: Db, st: State): S["VraPlan"] {
  const projects: S["VraProjectPlan"][] = vraProjects.map((v) => {
    const [how, id, name] = rvavOf(db, st, v);
    const items = templates.filter((t) => t.shared.includes(v.id)).length;
    const deployments = vraDeployments.filter((d) => d.project === v.id).length;
    return { vra_id: v.id, name: v.name, how, rvav_project_id: id, rvav_project_name: name, items, deployments };
  });
  const project = (id: string) => projects.find((p) => p.vra_id === id)!;
  const items: S["VraItemPlan"][] = templates.map((t) => {
    const done = st.imported.has(t.id);
    const mapped = t.shared.map((id) => project(id)).filter((p) => p.how !== "stays");
    return {
      blueprint_id: t.id, name: t.name, slug: t.slug, status: t.status, version: t.version,
      action: t.status === "skipped" ? "skip" : done ? "unchanged" : "create",
      rvav_item_id: done ? `itm_${t.slug}` : undefined, notes: t.notes,
      share_add: t.status === "skipped" ? [] : mapped.flatMap((p) => (p.rvav_project_id && !st.shared.has(`${t.id}/${p.rvav_project_id}`) ? [p.rvav_project_id] : [])),
      share_unmapped: t.status === "skipped" ? [] : mapped.filter((p) => p.how === "none").map((p) => p.name),
    };
  }); // prettier-ignore
  const deployments: S["VraDeploymentPlan"][] = vraDeployments.map((d) => {
    const p = project(d.project);
    const t = templates.find((x) => x.id === d.template)!;
    const base = { vra_id: d.id, name: d.name, rvav_name: rvavName(d.name), vra_project: p.name, owner: d.owner, notes: [] as S["VraNote"][],
      vms: d.vms.map((n, i) => ({ vcenter: "vc01.corp.example", moref: `vm-${7100 + i}`, name: n })) }; // prettier-ignore
    const skip = (where: string, text: string): S["VraDeploymentPlan"] => ({
      ...base,
      status: "skipped",
      notes: [{ level: "unmapped", where, text }],
    });
    if (st.adopted.has(d.id)) return { ...base, status: "adopted", deployments: [`dep_${rvavName(d.name)}`] };
    if (d.why) return skip("vms", d.why);
    if (p.how === "stays") return skip("project", `vRA project ${p.name} stays in vRA, as chosen.`);
    if (!p.rvav_project_id)
      return skip("project", `vRA project ${p.name} has no rvav project: map it to one, or create it.`);
    if (!st.imported.has(t.id))
      return skip("item", "The item it was requested from isn't imported into rvav: import its cloud template first.");
    if (!st.published.has(t.id)) return skip("item", `${t.slug} isn't published in rvav: publish it, then adopt.`);
    if (!st.shared.has(`${t.id}/${p.rvav_project_id}`))
      return skip("item", `${t.slug} isn't offered to ${p.rvav_project_name} in rvav: share it there.`);
    return { ...base, status: "adopt", project_id: p.rvav_project_id, environment: "dev", catalog_item_id: `itm_${t.slug}`,
      notes: base.name === base.rvav_name ? [] : [{ level: "info", where: "name", text: `Named ${base.rvav_name} in rvav: lower-case letters, digits, dots and dashes.` }] }; // prettier-ignore
  });
  const count = (f: (x: S["VraItemPlan"]) => boolean) => items.filter(f).length;
  const cutover: S["VraCutover"] = {
    total: projects.length,
    moved: Object.keys(st.moved).length,
    deployments_left: deployments.filter((d) => d.status !== "adopted").map((d) => d.name),
    switch_off: false,
    projects: projects.map((p) => {
      const mine = templates.filter((t) => t.shared.includes(p.vra_id) && t.status !== "skipped");
      const pending = mine.filter((t) => !st.published.has(t.id));
      const deps = deployments.filter((d) => d.vra_project === p.name);
      const left = deps.filter((d) => d.status !== "adopted");
      const m = st.moved[p.vra_id];
      const unshared = mine.filter(
        (t) => st.imported.has(t.id) && p.rvav_project_id && !st.shared.has(`${t.id}/${p.rvav_project_id}`),
      );
      const checks: S["VraCheck"][] = [
        p.rvav_project_id
          ? { key: "project", label: "rvav project", state: "ok", detail: `${p.rvav_project_name}, ${p.how === "mapped" ? "mapped" : "by name"}.` }
          : { key: "project", label: "rvav project", state: "todo", detail: p.how === "stays" ? "It stays in vRA, as chosen: map it to an rvav project to move it." : "Map it to an rvav project, or create one." },
        pending.length === 0
          ? { key: "items", label: "Catalog items", state: "ok", detail: mine.length ? `All ${mine.length} published in rvav.` : "vRA offers it no cloud template." }
          : { key: "items", label: "Catalog items", state: "warn", detail: `${mine.length - pending.length} of ${mine.length} published in rvav. Not yet: ${pending.map((t) => `${t.name} (${st.imported.has(t.id) ? "a draft" : "not imported"})`).join(", ")}.` },
        ...(p.rvav_project_id ? [unshared.length
          ? { key: "sharing", label: "Shared in rvav", state: "todo", detail: `Not yet offered to ${p.rvav_project_name}: ${unshared.map((t) => t.name).join(", ")}. The import shares them.` }
          : { key: "sharing", label: "Shared in rvav", state: "ok", detail: `Its items are offered to ${p.rvav_project_name} in rvav.` }] : []),
        left.length === 0
          ? { key: "deployments", label: "Deployments adopted", state: "ok", detail: deps.length ? `All ${deps.length} adopted.` : "It has none in vRA." }
          : { key: "deployments", label: "Deployments adopted", state: "warn", detail: `${deps.length - left.length} of ${deps.length} adopted. Still in vRA: ${left.map((d) => d.name).join(", ")}.` },
        { key: "people", label: "People", state: "ok", detail: "All 2 have signed in to rvav." },
        { key: "subscriptions", label: "vRA subscriptions", state: "manual", detail: "vRA runs these on its deployments' events: CMDB on provision (compute.provision.post). rvav subscriptions must do what they did." },
        { key: "policies", label: "Policies", state: "manual", detail: "Set by hand from the plan: Production needs a lead, Deployment limit." },
        ...(m ? [{ key: "vra_offer", label: "vRA offers it nothing", state: "warn", detail: `vRA still offers it ${mine.length} items: vRA's administrators remove them.` }] : []),
      ]; // prettier-ignore
      return {
        vra_id: p.vra_id, name: p.name, rvav_project_id: p.rvav_project_id, rvav_project_name: p.rvav_project_name,
        moved_at: m?.at, moved_by: m?.by, note: m?.note, ready: Boolean(p.rvav_project_id) && pending.length === 0 && unshared.length === 0, checks,
      }; // prettier-ignore
    }),
  };
  return {
    snapshot: st.snapshot,
    summary: {
      items_create: count((i) => i.action === "create"), items_update: 0, items_unchanged: count((i) => i.action === "unchanged"),
      items_blocked: count((i) => i.action === "skip" || i.action === "conflict"), shares_add: items.reduce((n, i) => n + i.share_add.length, 0),
      deployments_adopt: deployments.filter((d) => d.status === "adopt").length, deployments_adopted: deployments.filter((d) => d.status === "adopted").length,
      deployments_skipped: deployments.filter((d) => d.status === "skipped").length,
    },
    projects, items, deployments, cutover,
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
      else if (b.stays_in_vra) st.map[id] = "";
      else delete st.map[id];
      return new HttpResponse(null, { status: 204 });
    }),
    http.put(`${base}/admin/vra/projects/:id/cutover`, async ({ params, request }) => {
      await wait();
      const st = state(db);
      const id = String(params.id);
      const vp = vraProjects.find((p) => p.id === id);
      if (!vp) return problem(404, "Not found");
      const b = (await request.json()) as S["VraCutoverWrite"];
      if (b.moved && !rvavOf(db, st, vp)[1])
        return problem(422, "Not valid", [
          { field: "/moved", code: "unmapped", message: `${vp.name} has no rvav project: map it first.` },
        ]);
      if (b.moved) st.moved[id] = { at: new Date().toISOString(), by: "Marc Keller", note: b.note };
      else delete st.moved[id];
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/vra/import`, async ({ request }) => {
      await wait();
      const st = state(db);
      const b = (await request.json()) as S["VraImportRequest"];
      const before = plan(db, st);
      const out: S["VraImportResult"] = { created: [], updated: [], shared: 0, adopted: [], failed: [], plan: before };
      // Only the chosen vRA projects' sharing and deployments.
      const chosen = (vraID: string) => !b.projects || b.projects.includes(vraID);
      const shareTo = new Set(
        before.projects.filter((p) => p.rvav_project_id && chosen(p.vra_id)).map((p) => p.rvav_project_id),
      );
      if (b.catalog ?? true) {
        for (const it of before.items) {
          if (it.action === "create") {
            // Imported as drafts: nothing is published.
            st.imported.add(it.blueprint_id);
            out.created.push(it.name);
          }
          for (const p of it.share_add.filter((x) => shareTo.has(x))) {
            st.shared.add(`${it.blueprint_id}/${p}`);
            out.shared++;
          }
        }
      }
      if (b.deployments) {
        for (const d of plan(db, st).deployments) {
          const vd = vraDeployments.find((x) => x.id === d.vra_id)!;
          if (d.status !== "adopt" || !chosen(vd.project)) continue;
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
