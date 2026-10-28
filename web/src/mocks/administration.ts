// Administration in the mock backend (S9, ADR-0020): projects, members,
// group mappings, environments, placement, network mappings, templates,
// sizes, naming, quotas, the lease, approval and day-2 policies, the
// explain query and integration settings. Checks follow the server's where
// the UI shows them.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { celCheck } from "./cel";
import { audited } from "./governance";
import { clusters, datastores, networks } from "./inventory";
import type { Db } from "./seed";

type S = components["schemas"];

interface Member {
  userId: string;
  role: S["ProjectRole"];
}

interface AdminState {
  extra: Record<string, { archived_at?: string; created_at: string }>;
  members: Record<string, Member[]>;
  groups: S["GroupMapping"][];
  envs: Record<string, { snapshot_max_count: number; snapshot_max_age_days: number | null }>;
  rules: S["PlacementRule"][];
  mappings: S["NetworkMapping"][];
  templates: S["VmTemplate"][];
  sizes: S["SizePreset"][];
  naming: S["NamingPolicy"][];
  quotas: Record<string, S["QuotaLimit"][]>;
  leases: S["LeasePolicy"][];
  approvals: S["ApprovalPolicy"][];
  day2: S["Day2Policy"][];
  integrations: Record<string, { settings: Record<string, unknown>; secret: boolean; updated_at?: string }>;
  seq: number;
}

const now = () => new Date().toISOString();

const tags: Record<string, string[]> = {
  "GVA-PRD-CL01": ["rvav-tier/gold"],
  "GVA-DEV-CL01": ["rvav-tier/bronze"],
  "vsan-gva-prd-cl01": ["rvav-tier/gold"],
  "nfs-gva-bronze-01": ["rvav-tier/bronze"],
};

function state(db: Db): AdminState {
  const d = db as Db & { administration?: AdminState };
  if (d.administration) return d.administration;
  const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();
  const ref = (id: string) => {
    const p = db.projects.find((x) => x.id === id)!;
    return { id: p.id, code: p.code, name: p.name };
  };
  const rules: S["PlacementRule"][] = [];
  for (const p of db.projects) {
    for (const env of p.environments) {
      rules.push({
        id: `plc_${p.code}_${env}`,
        project: ref(p.id),
        environment: env,
        priority: 100,
        clusters: [`GVA-${env.toUpperCase()}-*`],
        datastores: ["*"],
        networks: [`dvpg-*-${env}-*`],
        folder: `/GVA/vm/rvav/${p.code}/${env}`,
        cluster_tags: [],
        datastore_tags: [],
      });
    }
  }
  const level = (name: string, group: string): S["ApprovalLevel"] => ({ name, group, mode: "any" });
  d.administration = {
    seq: 1000,
    extra: Object.fromEntries(db.projects.map((p) => [p.id, { created_at: ago(400) }])),
    members: {
      prj_web: [
        { userId: "usr_lmartin", role: "project-admin" },
        { userId: "usr_tnguyen", role: "member" },
      ],
      prj_data: [{ userId: "usr_sbrunner", role: "project-admin" }],
    },
    groups: [
      { id: "gmp_admins", oidc_group: "rvav-admins", project: null, role: "admin", users: 1 },
      { id: "gmp_catalog", oidc_group: "rvav-catalog-admins", project: null, role: "catalog-admin", users: 1 },
      { id: "gmp_web", oidc_group: "rvav-web", project: ref("prj_web"), role: "member", users: 1 },
      { id: "gmp_data", oidc_group: "rvav-data", project: ref("prj_data"), role: "member", users: 1 },
    ],
    envs: Object.fromEntries(db.environments.map((e) => [e.key, { snapshot_max_count: 3, snapshot_max_age_days: 7 }])),
    rules,
    mappings: networks.map((n) => ({
      network: n.name,
      subnet: n.subnet,
      gateway: n.gateway,
      dns_zone: "corp.example",
      dns_servers: ["10.10.0.53"],
      source: "efficientip" as const,
      subnet_name: n.name.replace("dvpg-", ""),
      synced_at: ago(0.01),
    })),
    templates: [
      {
        key: "rhel9", label: "RHEL 9.4", vcenter_template: "tpl-rhel9.4-2026.10", os_family: "linux", customization: "",
        description: "With the corporate baseline.", offers: [{ project_id: null, environment: null }], in_inventory: true,
      },
      {
        key: "win2025", label: "Windows Server 2025", vcenter_template: "tpl-win2025-std-2026.09", os_family: "windows",
        customization: "", description: "", offers: [{ project_id: "prj_web", environment: null }], in_inventory: true,
      },
    ], // prettier-ignore
    sizes: [
      {
        key: "S",
        label: "Small",
        vcpu: 2,
        memory_gb: 4,
        disk_gb: 60,
        offers: [{ project_id: null, environment: null }],
      },
      {
        key: "M",
        label: "Medium",
        vcpu: 4,
        memory_gb: 16,
        disk_gb: 80,
        offers: [{ project_id: null, environment: null }],
      },
      {
        key: "XL",
        label: "X-Large",
        vcpu: 8,
        memory_gb: 48,
        disk_gb: 120,
        offers: [{ project_id: null, environment: "prd" }],
      },
    ],
    naming: [],
    quotas: Object.fromEntries(
      db.projects.map((p) => [
        p.id,
        (["vcpu", "memory_gb", "storage_gb", "vm_count"] as const).map((r) => ({
          environment: "",
          resource: r,
          limit: db.quota[p.id]?.limits[r] ?? null,
          enforcement: "hard" as const,
        })),
      ]),
    ),
    leases: [
      { environment: "dev", project_id: null, default_days: 30, max_days: 90, max_extensions: 2, grace_days: 7, expiry_action: "power_off_then_delete" },
      { environment: "tst", project_id: null, default_days: 30, max_days: 90, max_extensions: 2, grace_days: 7, expiry_action: "power_off_then_delete" },
      { environment: "prd", project_id: null, default_days: null, max_days: null, max_extensions: 0, grace_days: 7, expiry_action: "notify_only" },
    ], // prettier-ignore
    approvals: [
      {
        id: "pol_prd_vm", name: "Production VMs", reason: "Production VMs need sign-off", condition: 'environment == "prd"',
        item_type: "vm_blueprint", project_id: null, levels: [level("Infrastructure leads", "rvav-infra-leads")], priority: 100, enabled: true,
      },
    ], // prettier-ignore
    day2: [
      {
        id: "d2p_admins", name: "Administrators", reason: "", priority: 10, actions: ["*"], roles: ["admin"], condition: "true",
        effect: "allow", project_id: null, enabled: true,
      },
      {
        id: "d2p_prd_delete", name: "Deleting in production", reason: "Deleting a production deployment needs sign-off", priority: 20,
        actions: ["delete"], roles: [], condition: "environment.production", effect: "approval", project_id: null, enabled: true,
        levels: [level("Infrastructure leads", "rvav-infra-leads")],
      },
      {
        id: "d2p_members", name: "Members operate their VMs", reason: "", priority: 50, actions: ["*"],
        roles: ["owner", "member", "project-admin"], condition: "true", effect: "allow", project_id: null, enabled: true,
      },
    ], // prettier-ignore
    integrations: {
      vcenter: { settings: { url: "https://vc01.corp.example/sdk", username: "svc-rvav@vsphere.local", insecure: false, name: "vc01.corp.example" }, secret: true },
      awx: { settings: { url: "https://awx.corp.example", organization: "rvav", catalog_labels: ["catalog"], action_labels: ["day2"], inventory_source_id: 12, insecure: false }, secret: true },
      efficientip: { settings: { url: "https://ipam.corp.example", auth: "native", username: "rvav", space: "", dns_server: "ns1.corp.example", dns_view: "", instance_id: "rvav", insecure: false }, secret: true },
    }, // prettier-ignore
  };
  return d.administration;
}

function problem(status: number, title: string, errors?: S["FieldError"][], detail?: string) {
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/x",
      title,
      status,
      request_id: "req_mock",
      ...(errors ? { errors } : {}),
      ...(detail ? { detail } : {}),
    },
    { status, headers: { "Content-Type": "application/problem+json" } },
  );
}
const invalid = (errors: S["FieldError"][]) => problem(422, "Some inputs need attention", errors);
const err = (field: string, message: string): S["FieldError"] => ({ field, code: "invalid", message });

const glob = (pattern: string, name: string) =>
  new RegExp(
    "^" +
      pattern
        .replace(/[.+^${}()|[\]\\]/g, "\\$&")
        .replace(/\*/g, ".*")
        .replace(/\?/g, ".") +
      "$",
  ).test(name);
const selects = (patterns: string[], want: string[], name: string) =>
  (patterns.length === 0 || patterns.some((p) => glob(p, name))) &&
  want.every((t) => (tags[name] ?? []).includes(t)) &&
  (patterns.length > 0 || want.length > 0);

/** Whether a project's placement rules in an environment allow a cluster, datastore or network, as the server's. */
export function placed(
  db: Db,
  projectId: string,
  env: string,
  kind: "cluster" | "datastore" | "network",
  name: string,
) {
  return state(db).rules.some(
    (r) =>
      r.project.id === projectId &&
      r.environment === env &&
      (kind === "cluster"
        ? selects(r.clusters, r.cluster_tags, name)
        : kind === "datastore"
          ? selects(r.datastores, r.datastore_tags, name)
          : r.networks.some((pat) => glob(pat, name))),
  );
}

/** The VM folder of a project's first placement rule in an environment. */
export function placementFolder(db: Db, projectId: string, env: string): string | undefined {
  return state(db)
    .rules.filter((r) => r.project.id === projectId && r.environment === env)
    .sort((a, b) => a.priority - b.priority)[0]?.folder;
}

/** The naming policy offered to a project in an environment, the most specific winning. */
export function namingTemplate(db: Db, projectId: string, env: string): string | undefined {
  let best: { template: string; score: number } | undefined;
  for (const n of state(db).naming) {
    for (const o of n.offers) {
      if ((o.project_id && o.project_id !== projectId) || (o.environment && o.environment !== env)) continue;
      const score = (o.project_id ? 2 : 0) + (o.environment ? 1 : 0);
      if (!best || score > best.score) best = { template: n.template, score };
    }
  }
  return best?.template;
}

export function administrationHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const st = () => state(db);
  const project = (id: string) => db.projects.find((p) => p.id === id || p.code === id);
  const user = (id: string) => db.users.find((u) => u.id === id);
  const adminProject = (p: Db["projects"][number]): S["AdminProject"] => {
    const x = st().extra[p.id] ?? { created_at: now() };
    return {
      ...p,
      archived: Boolean(x.archived_at),
      ...(x.archived_at ? { archived_at: x.archived_at } : {}),
      created_at: x.created_at,
      members: (st().members[p.id] ?? []).length,
      deployments: db.deployments.filter((d) => d.project.id === p.id && d.status !== "deleted").length,
    };
  };
  const members = (projectId: string): S["ProjectMember"][] =>
    (st().members[projectId] ?? []).map((m) => ({
      user: { id: m.userId, name: user(m.userId)?.name ?? m.userId },
      role: m.role,
      direct: true,
      direct_role: m.role,
      groups: [],
    }));
  const next = (prefix: string) => `${prefix}_${++st().seq}`;
  const checkCel = (expr: string | null | undefined, field: string, out: S["FieldError"][]) => {
    const p = expr ? celCheck(expr) : null;
    if (p) out.push(err(field, `This condition is not valid CEL: ${p}`));
  };

  return [
    // ── People, projects, members, groups, environments ──
    http.get(`${base}/users`, async ({ request }) => {
      await wait();
      const q = (new URL(request.url).searchParams.get("q") ?? "").toLowerCase();
      return HttpResponse.json({
        items: db.users.filter((u) => `${u.name} ${u.email ?? ""}`.toLowerCase().includes(q)),
      });
    }),
    http.get(`${base}/admin/projects`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const q = (url.searchParams.get("q") ?? "").toLowerCase();
      const archived = url.searchParams.get("archived") === "true";
      return HttpResponse.json({
        items: db.projects
          .map(adminProject)
          .filter((p) => (archived || !p.archived) && `${p.name} ${p.code}`.toLowerCase().includes(q)),
      });
    }),
    http.post(`${base}/admin/projects`, async ({ request }) => {
      await wait();
      const c = (await request.json()) as S["AdminProjectCreate"];
      const errors: S["FieldError"][] = [];
      if (!/^[a-z][a-z0-9]{1,7}$/.test(c.code))
        errors.push(err("/code", "2 to 8 lower-case letters and digits, starting with a letter."));
      if (!c.name.trim()) errors.push(err("/name", "Give the project a name."));
      if (errors.length) return invalid(errors);
      if (db.projects.some((p) => p.code === c.code))
        return problem(409, "Conflict", undefined, `A project has the code ${c.code}.`);
      const p = {
        id: `prj_${c.code}`,
        code: c.code,
        name: c.name.trim(),
        description: c.description ?? "",
        cost_center: c.cost_center ?? "",
        environments: c.environments,
        my_role: "project-admin" as const,
      };
      db.projects.push(p);
      db.quota[p.id] = {
        limits: { vcpu: null, memory_gb: null, storage_gb: null, vm_count: null },
        used: { vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 },
      };
      st().extra[p.id] = { created_at: now() };
      st().members[p.id] = (c.project_admins ?? []).map((u) => ({ userId: u, role: "project-admin" }));
      if (c.project_admins?.includes(db.me.user.id))
        db.me.projects.push({ project: { id: p.id, code: p.code, name: p.name }, role: "project-admin" });
      st().quotas[p.id] = [];
      audited(db, "project.create", "project", p.id, { code: p.code });
      return HttpResponse.json(adminProject(p), { status: 201 });
    }),
    http.get(`${base}/admin/projects/:id`, async ({ params }) => {
      await wait();
      const p = project(String(params.id));
      return p ? HttpResponse.json(adminProject(p)) : problem(404, "Not found");
    }),
    http.patch(`${base}/admin/projects/:id`, async ({ params, request }) => {
      await wait();
      const p = project(String(params.id));
      if (!p) return problem(404, "Not found");
      const u = (await request.json()) as S["AdminProjectUpdate"];
      if (u.name !== undefined) p.name = u.name;
      if (u.description !== undefined) p.description = u.description;
      if (u.cost_center !== undefined) p.cost_center = u.cost_center;
      if (u.environments) p.environments = u.environments;
      if (u.archived !== undefined) st().extra[p.id]!.archived_at = u.archived ? now() : undefined;
      audited(db, u.archived === true ? "project.archive" : "project.update", "project", p.id);
      return HttpResponse.json(adminProject(p));
    }),
    http.get(`${base}/projects/:id/members`, async ({ params }) => {
      await wait();
      return HttpResponse.json({ items: members(String(params.id)) });
    }),
    http.put(`${base}/projects/:id/members/:userId`, async ({ params, request }) => {
      await wait();
      const id = String(params.id);
      const { role } = (await request.json()) as S["ProjectMemberWrite"];
      const list = (st().members[id] ??= []);
      const m = list.find((x) => x.userId === params.userId);
      const admins = list.filter((x) => x.role === "project-admin" && x.userId !== params.userId).length;
      if (m?.role === "project-admin" && role !== "project-admin" && admins === 0)
        return problem(
          409,
          "Conflict",
          undefined,
          "The project would have no project admin: make someone else one first.",
        );
      if (m) m.role = role;
      else list.push({ userId: String(params.userId), role });
      if (params.userId === db.me.user.id) {
        const mine = db.me.projects.find((x) => x.project.id === id);
        const pr = project(id)!;
        if (mine) mine.role = role;
        else db.me.projects.push({ project: { id: pr.id, code: pr.code, name: pr.name }, role });
      }
      audited(db, "member.set", "project", id, { user_id: params.userId, role });
      return HttpResponse.json(members(id).find((x) => x.user.id === params.userId));
    }),
    http.delete(`${base}/projects/:id/members/:userId`, async ({ params }) => {
      await wait();
      const id = String(params.id);
      const list = st().members[id] ?? [];
      const m = list.find((x) => x.userId === params.userId);
      if (!m) return problem(404, "Not found");
      if (m.role === "project-admin" && list.filter((x) => x.role === "project-admin").length === 1)
        return problem(
          409,
          "Conflict",
          undefined,
          "The project would have no project admin: make someone else one first.",
        );
      list.splice(list.indexOf(m), 1);
      if (params.userId === db.me.user.id) db.me.projects = db.me.projects.filter((x) => x.project.id !== id);
      audited(db, "member.remove", "project", id, { user_id: params.userId });
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/group-mappings`, async () => {
      await wait();
      return HttpResponse.json({ items: st().groups });
    }),
    http.post(`${base}/admin/group-mappings`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["GroupMappingWrite"];
      const projectRole = ["project-admin", "member", "viewer"].includes(w.role);
      if (!w.oidc_group.trim()) return invalid([err("/oidc_group", "Name the OIDC group.")]);
      if (Boolean(w.project_id) !== projectRole)
        return invalid([
          err(
            "/role",
            w.project_id
              ? "In a project: project-admin, member or viewer."
              : "Without a project: admin, catalog-admin or auditor.",
          ),
        ]);
      const p = w.project_id ? project(w.project_id) : undefined;
      const m: S["GroupMapping"] = {
        id: next("gmp"),
        oidc_group: w.oidc_group.trim(),
        project: p ? { id: p.id, code: p.code, name: p.name } : null,
        role: w.role,
        users: 0,
      };
      st().groups.push(m);
      audited(db, "group_mapping.create", "group_mapping", m.id, { group: m.oidc_group });
      return HttpResponse.json(m, { status: 201 });
    }),
    http.delete(`${base}/admin/group-mappings/:id`, async ({ params }) => {
      await wait();
      const groups = st().groups;
      const i = groups.findIndex((g) => g.id === params.id);
      if (i < 0) return problem(404, "Not found");
      if (groups[i]!.role === "admin" && groups.filter((g) => g.role === "admin").length === 1)
        return problem(
          409,
          "Conflict",
          undefined,
          "Nobody would be an administrator any more: map another group to admin first.",
        );
      groups.splice(i, 1);
      audited(db, "group_mapping.delete", "group_mapping", String(params.id));
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/environments`, async () => {
      await wait();
      return HttpResponse.json({
        items: db.environments.map((e) => ({
          ...e,
          ...(st().envs[e.key] ?? { snapshot_max_count: 3, snapshot_max_age_days: 7 }),
          projects: db.projects.filter((p) => p.environments.includes(e.key)).length,
          deployments: db.deployments.filter((d) => d.environment === e.key && d.status !== "deleted").length,
        })),
      });
    }),
    http.post(`${base}/admin/environments`, async ({ request }) => {
      await wait();
      const e = (await request.json()) as S["AdminEnvironment"];
      if (!/^[a-z][a-z0-9]{1,9}$/.test(e.key))
        return invalid([err("/key", "2 to 10 lower-case letters and digits, starting with a letter.")]);
      if (db.environments.some((x) => x.key === e.key))
        return problem(409, "Conflict", undefined, `An environment has the key ${e.key}.`);
      db.environments.push({ key: e.key, name: e.name, production: e.production, order: e.order });
      st().envs[e.key] = {
        snapshot_max_count: e.snapshot_max_count,
        snapshot_max_age_days: e.snapshot_max_age_days ?? null,
      };
      audited(db, "environment.create", "environment", e.key);
      return HttpResponse.json({ ...e, projects: 0, deployments: 0 }, { status: 201 });
    }),
    http.patch(`${base}/admin/environments/:key`, async ({ params, request }) => {
      await wait();
      const e = db.environments.find((x) => x.key === params.key);
      if (!e) return problem(404, "Not found");
      const u = (await request.json()) as S["AdminEnvironmentUpdate"];
      if (u.name !== undefined) e.name = u.name;
      if (u.production !== undefined) e.production = u.production;
      if (u.order !== undefined) e.order = u.order;
      const x = (st().envs[e.key] ??= { snapshot_max_count: 3, snapshot_max_age_days: 7 });
      if (u.snapshot_max_count !== undefined) x.snapshot_max_count = u.snapshot_max_count;
      if (u.snapshot_max_age_days !== undefined) x.snapshot_max_age_days = u.snapshot_max_age_days || null;
      audited(db, "environment.update", "environment", e.key);
      return HttpResponse.json({ ...e, ...x });
    }),
    http.delete(`${base}/admin/environments/:key`, async ({ params }) => {
      await wait();
      const i = db.environments.findIndex((x) => x.key === params.key);
      if (i < 0) return problem(404, "Not found");
      if (db.projects.some((p) => p.environments.includes(String(params.key))))
        return problem(409, "Conflict", undefined, `Projects use ${String(params.key)}.`);
      db.environments.splice(i, 1);
      audited(db, "environment.delete", "environment", String(params.key));
      return new HttpResponse(null, { status: 204 });
    }),

    // ── Placement and network mappings ──
    http.get(`${base}/admin/placement-rules`, async ({ request }) => {
      await wait();
      const id = new URL(request.url).searchParams.get("project_id");
      return HttpResponse.json({ items: st().rules.filter((r) => !id || r.project.id === id) });
    }),
    ...(["post", "put"] as const).map((method) =>
      http[method](`${base}/admin/placement-rules${method === "put" ? "/:id" : ""}`, async ({ params, request }) => {
        await wait();
        const w = (await request.json()) as S["PlacementRuleWrite"];
        const p = project(w.project_id);
        const errors: S["FieldError"][] = [];
        if (!p) errors.push(err("/project_id", "There is no such project."));
        else if (!p.environments.includes(w.environment))
          errors.push(err("/environment", `${p.name} doesn't deploy to ${w.environment}.`));
        if (w.clusters.length === 0 && !w.cluster_tags?.length)
          errors.push(err("/clusters", "Name clusters, or tags they carry."));
        if (w.networks.length === 0) errors.push(err("/networks", "Name the networks VMs may connect to."));
        if (!w.folder.startsWith("/"))
          errors.push(err("/folder", "A folder's path, for example /GVA/vm/rvav/web/dev."));
        (w.cluster_tags ?? []).forEach((t, i) => {
          if (!/^[^/]+\/[^/]+$/.test(t)) errors.push(err(`/cluster_tags/${i}`, `"${t}" is not category/tag.`));
        });
        if (errors.length) return invalid(errors);
        const rule: S["PlacementRule"] = {
          id: method === "put" ? String(params.id) : next("plc"),
          project: { id: p!.id, code: p!.code, name: p!.name },
          environment: w.environment,
          priority: w.priority ?? 100,
          clusters: w.clusters,
          datastores: w.datastores,
          networks: w.networks,
          folder: w.folder,
          cluster_tags: w.cluster_tags ?? [],
          datastore_tags: w.datastore_tags ?? [],
        };
        const rules = st().rules;
        const i = rules.findIndex((r) => r.id === rule.id);
        if (method === "put" && i < 0) return problem(404, "Not found");
        if (i >= 0) rules[i] = rule;
        else rules.push(rule);
        audited(db, `placement.rule.${method === "put" ? "update" : "create"}`, "placement_rule", rule.id);
        return HttpResponse.json(rule, { status: method === "put" ? 200 : 201 });
      }),
    ),
    http.delete(`${base}/admin/placement-rules/:id`, async ({ params }) => {
      await wait();
      const rules = st().rules;
      const i = rules.findIndex((r) => r.id === params.id);
      if (i < 0) return problem(404, "Not found");
      rules.splice(i, 1);
      audited(db, "placement.rule.delete", "placement_rule", String(params.id));
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/placement-preview`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const p = project(url.searchParams.get("project_id") ?? "");
      const env = url.searchParams.get("environment") ?? "";
      if (!p) return problem(404, "Not found");
      const rules = st()
        .rules.filter((r) => r.project.id === p.id && r.environment === env)
        .sort((a, b) => a.priority - b.priority);
      const out: S["PlacementPreview"] = {
        clusters: [],
        datastores: [],
        networks: [],
        folder: rules[0]?.folder ?? "",
        problems: [],
      };
      if (rules.length === 0) {
        out.problems.push(`No placement rule for ${p.name} in ${env}: requests there fail.`);
        return HttpResponse.json(out);
      }
      const rule = (cluster: string) => rules.find((r) => selects(r.clusters, r.cluster_tags, cluster));
      for (const c of clusters) {
        const r = rule(c.name);
        if (!r) continue;
        out.clusters.push({ name: c.name, hosts: c.hosts, tags: tags[c.name] ?? [] });
        for (const d of datastores[c.name] ?? []) {
          if (selects(r.datastores, r.datastore_tags, d.name))
            out.datastores.push({
              name: d.name,
              free_gb: Math.round(d.freeTb * 1024),
              clusters: [c.name],
              tags: tags[d.name] ?? [],
            });
        }
        for (const n of networks.filter((x) => x.env === c.env && r.networks.some((pat) => glob(pat, x.name)))) {
          if (out.networks.some((x) => x.name === n.name)) continue;
          const m = st().mappings.find((x) => x.network === n.name);
          out.networks.push({
            name: n.name,
            clusters: [c.name],
            ...(m ? { subnet: m.subnet, dns_zone: m.dns_zone, source: m.source } : {}),
          });
          if (!m) out.problems.push(`Network ${n.name} has no subnet: map it by hand or in EfficientIP.`);
        }
      }
      if (out.clusters.length === 0) out.problems.push("No cluster in vCenter matches the rules.");
      return HttpResponse.json(out);
    }),
    http.get(`${base}/admin/network-mappings`, async () => {
      await wait();
      return HttpResponse.json({ items: st().mappings });
    }),
    http.put(`${base}/admin/network-mappings/:network`, async ({ params, request }) => {
      await wait();
      const w = (await request.json()) as S["NetworkMappingWrite"];
      if (!/^\d+\.\d+\.\d+\.\d+\/\d+$/.test(w.subnet))
        return invalid([err("/subnet", "A subnet such as 10.31.10.0/24.")]);
      if (!w.dns_zone.trim()) return invalid([err("/dns_zone", "The DNS zone VMs on this network are named in.")]);
      const m: S["NetworkMapping"] = {
        network: String(params.network),
        ...w,
        dns_servers: w.dns_servers ?? [],
        source: "manual",
      };
      const list = st().mappings;
      const i = list.findIndex((x) => x.network === m.network);
      if (i >= 0) list[i] = m;
      else list.push(m);
      audited(db, "network.mapping.set", "network", m.network);
      return HttpResponse.json(m);
    }),
    http.delete(`${base}/admin/network-mappings/:network`, async ({ params }) => {
      await wait();
      const list = st().mappings;
      const i = list.findIndex((x) => x.network === params.network && x.source === "manual");
      if (i < 0) return problem(404, "Not found");
      list.splice(i, 1);
      audited(db, "network.mapping.delete", "network", String(params.network));
      return new HttpResponse(null, { status: 204 });
    }),

    // ── Templates, sizes, naming ──
    ...(
      [
        ["templates", "template"],
        ["sizes", "size"],
      ] as const
    ).flatMap(([path, kind]) => {
      const list = () => (kind === "template" ? st().templates : st().sizes) as { key: string }[];
      return [
        http.get(`${base}/admin/${path}`, async () => {
          await wait();
          return HttpResponse.json({ items: list() });
        }),
        http.post(`${base}/admin/${path}`, async ({ request }) => {
          await wait();
          const w = (await request.json()) as { key: string; label: string; vcenter_template?: string };
          if (!w.label?.trim()) return invalid([err("/label", "Give it a label.")]);
          if (w.vcenter_template?.startsWith("library:"))
            return invalid([
              err(
                "/vcenter_template",
                "Content library items come with advanced provisioning (S12): name a vCenter template.",
              ),
            ]);
          if (list().some((x) => x.key === w.key))
            return problem(409, "Conflict", undefined, `The key ${w.key} is taken.`);
          const row = {
            offers: [],
            ...w,
            ...(kind === "template" ? { in_inventory: true, customization: "", description: "" } : {}),
          };
          list().push(row);
          audited(db, `${kind}.create`, kind, w.key);
          return HttpResponse.json(row, { status: 201 });
        }),
        http.put(`${base}/admin/${path}/:key`, async ({ params, request }) => {
          await wait();
          const w = (await request.json()) as { key: string };
          const i = list().findIndex((x) => x.key === params.key);
          if (i < 0) return problem(404, "Not found");
          list()[i] = { ...list()[i], ...w, key: String(params.key) };
          audited(db, `${kind}.update`, kind, String(params.key));
          return HttpResponse.json(list()[i]);
        }),
        http.delete(`${base}/admin/${path}/:key`, async ({ params }) => {
          await wait();
          const i = list().findIndex((x) => x.key === params.key);
          if (i < 0) return problem(404, "Not found");
          list().splice(i, 1);
          audited(db, `${kind}.delete`, kind, String(params.key));
          return new HttpResponse(null, { status: 204 });
        }),
      ];
    }),
    http.get(`${base}/admin/naming-policies`, async () => {
      await wait();
      return HttpResponse.json({ items: st().naming });
    }),
    ...(["post", "put"] as const).map((method) =>
      http[method](`${base}/admin/naming-policies${method === "put" ? "/:id" : ""}`, async ({ params, request }) => {
        await wait();
        const w = (await request.json()) as S["NamingPolicyWrite"];
        if (!/\{seq(:\d+)?\}$/.test(w.template))
          return invalid([
            err(
              "/template",
              "A template ends with {seq} or {seq:03}, for example {project}-{environment}-{role}{seq:03}.",
            ),
          ]);
        if (!w.name.trim()) return invalid([err("/name", "Give the policy a name.")]);
        const pol: S["NamingPolicy"] = {
          id: method === "put" ? String(params.id) : next("nmp"),
          name: w.name,
          template: w.template,
          counter: w.counter ?? "prefix",
          offers: w.offers ?? [],
        };
        const list = st().naming;
        const i = list.findIndex((x) => x.id === pol.id);
        if (i >= 0) list[i] = pol;
        else list.push(pol);
        audited(db, `naming.${method === "put" ? "update" : "create"}`, "naming_policy", pol.id);
        return HttpResponse.json(pol, { status: method === "put" ? 200 : 201 });
      }),
    ),
    http.delete(`${base}/admin/naming-policies/:id`, async ({ params }) => {
      await wait();
      const list = st().naming;
      const i = list.findIndex((x) => x.id === params.id);
      if (i < 0) return problem(404, "Not found");
      list.splice(i, 1);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/naming-preview`, async ({ request }) => {
      await wait();
      const q = (await request.json()) as S["NamingPreviewQuery"];
      if (!/\{seq(:\d+)?\}$/.test(q.template))
        return invalid([err("/template", "A template ends with {seq} or {seq:03}.")]);
      const p = project(q.project_id);
      const width = Number(/\{seq:(\d+)\}$/.exec(q.template)?.[1] ?? 3);
      const rawRole = (q.inputs ?? {}).role;
      const role = typeof rawRole === "string" ? rawRole : "";
      const prefix = q.template
        .replace(/\{seq(:\d+)?\}$/, "")
        .replaceAll("{project}", p?.code ?? "")
        .replaceAll("{environment}", q.environment)
        .replaceAll("{role}", role)
        .replaceAll("{inputs.role}", role);
      if (prefix.includes("{"))
        return HttpResponse.json({ names: [], skipped: [], problem: "Give the inputs the template uses." });
      const names = Array.from({ length: q.count ?? 3 }, (_, i) => `${prefix}${String(i + 2).padStart(width, "0")}`);
      return HttpResponse.json({
        names,
        skipped: [{ name: `${prefix}${"1".padStart(width, "0")}`, taken_in: "vcenter" }],
      });
    }),

    // ── Quotas and policies ──
    http.get(`${base}/admin/projects/:id/quotas`, async ({ params }) => {
      await wait();
      return HttpResponse.json({ items: st().quotas[String(params.id)] ?? [] });
    }),
    http.put(`${base}/admin/projects/:id/quotas`, async ({ params, request }) => {
      await wait();
      const { items } = (await request.json()) as S["QuotaLimitList"];
      const p = project(String(params.id));
      if (!p) return problem(404, "Not found");
      const errors: S["FieldError"][] = [];
      items.forEach((q, i) => {
        if (q.environment && !p.environments.includes(q.environment))
          errors.push(err(`/items/${i}/environment`, `${p.name} doesn't deploy to ${q.environment}.`));
        if (q.limit !== null && q.limit < 0) errors.push(err(`/items/${i}/limit`, "Zero or more, or no limit."));
      });
      if (errors.length) return invalid(errors);
      st().quotas[p.id] = items;
      // The project's own limits are what requests are checked against here.
      const limits = (db.quota[p.id] ??= {
        limits: { vcpu: null, memory_gb: null, storage_gb: null, vm_count: null },
        used: { vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 },
      }).limits;
      for (const r of ["vcpu", "memory_gb", "storage_gb", "vm_count"] as const)
        limits[r] = items.find((q) => !q.environment && q.resource === r)?.limit ?? null;
      audited(db, "policy.quota.set", "project", p.id);
      return HttpResponse.json({ items });
    }),
    http.get(`${base}/admin/lease-policies`, async () => {
      await wait();
      return HttpResponse.json({ items: st().leases });
    }),
    http.put(`${base}/admin/lease-policies`, async ({ request }) => {
      await wait();
      const l = (await request.json()) as S["LeasePolicy"];
      if (l.default_days != null && l.max_days != null && l.default_days > l.max_days)
        return invalid([err("/default_days", "The default can't be longer than the maximum.")]);
      const list = st().leases;
      const i = list.findIndex(
        (x) => x.environment === l.environment && (x.project_id ?? null) === (l.project_id ?? null),
      );
      if (i >= 0) list[i] = l;
      else list.push(l);
      audited(db, "policy.lease.set", "lease_policy", l.environment);
      return HttpResponse.json(l);
    }),
    http.delete(`${base}/admin/lease-policies`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const list = st().leases;
      const i = list.findIndex(
        (x) =>
          x.environment === url.searchParams.get("environment") && x.project_id === url.searchParams.get("project_id"),
      );
      if (i < 0) return problem(404, "Not found");
      list.splice(i, 1);
      return new HttpResponse(null, { status: 204 });
    }),
    ...(
      [
        ["approval-policies", "approval", "pol"],
        ["day2-policies", "day2", "d2p"],
      ] as const
    ).flatMap(([path, kind, prefix]) => {
      const list = () => (kind === "approval" ? st().approvals : st().day2) as { id: string; priority: number }[];
      const save = (id: string, w: Record<string, unknown>, status: number) => {
        const errors: S["FieldError"][] = [];
        if (typeof w.name !== "string" || !w.name.trim()) errors.push(err("/name", "Give the policy a name."));
        checkCel(w.condition as string, "/condition", errors);
        if (kind === "approval") checkCel(w.auto_approve as string, "/auto_approve", errors);
        const levels = (w.levels ?? []) as S["ApprovalLevel"][];
        if ((kind === "approval" || w.effect === "approval") && levels.length === 0)
          errors.push(err("/levels", "Give at least one level of approvers."));
        levels.forEach((l, i) => {
          if (!l.group && !l.approvers?.length) errors.push(err(`/levels/${i}`, "Give an OIDC group or approvers."));
        });
        if (errors.length) return invalid(errors);
        const pol = {
          reason: "",
          project_id: null,
          enabled: true,
          actions: ["*"],
          roles: [],
          condition: "true",
          ...w,
          id,
          priority: Number(w.priority ?? 100),
        };
        const i = list().findIndex((x) => x.id === id);
        if (i >= 0) list()[i] = pol;
        else list().push(pol);
        list().sort((a, b) => a.priority - b.priority);
        audited(db, `policy.${kind}.${status === 201 ? "create" : "update"}`, `${kind}_policy`, id);
        return HttpResponse.json(pol, { status });
      };
      return [
        http.get(`${base}/admin/${path}`, async () => {
          await wait();
          return HttpResponse.json({ items: list() });
        }),
        http.post(`${base}/admin/${path}`, async ({ request }) => {
          await wait();
          return save(next(prefix), (await request.json()) as Record<string, unknown>, 201);
        }),
        http.put(`${base}/admin/${path}/:id`, async ({ params, request }) => {
          await wait();
          if (!list().some((x) => x.id === params.id)) return problem(404, "Not found");
          return save(String(params.id), (await request.json()) as Record<string, unknown>, 200);
        }),
        http.delete(`${base}/admin/${path}/:id`, async ({ params }) => {
          await wait();
          const i = list().findIndex((x) => x.id === params.id);
          if (i < 0) return problem(404, "Not found");
          list().splice(i, 1);
          audited(db, `policy.${kind}.delete`, `${kind}_policy`, String(params.id));
          return new HttpResponse(null, { status: 204 });
        }),
      ];
    }),
    http.post(`${base}/admin/policies/explain`, async ({ request }) => {
      await wait();
      const q = (await request.json()) as S["PolicyExplainQuery"];
      const p = project(q.project_id);
      const env = db.environments.find((e) => e.key === q.environment);
      if (!p || !env) return problem(404, "Not found");
      const lease =
        st().leases.find((l) => l.environment === env.key && l.project_id === p.id) ??
        st().leases.find((l) => l.environment === env.key && !l.project_id);
      const naming = st().naming.find((n) =>
        n.offers.some((o) => (!o.project_id || o.project_id === p.id) && (!o.environment || o.environment === env.key)),
      );
      const approvals = st().approvals.map((a) => {
        const applies = a.enabled && (a.condition === "true" || a.condition === `environment == "${env.key}"`);
        return {
          id: a.id,
          name: a.name,
          applies,
          why: applies ? `Applies: ${a.condition}.` : `Its condition ${a.condition} is false here.`,
        };
      });
      const deciding = approvals.filter((a) => a.applies).map((a) => a.name);
      const action = q.action;
      const day2 = action
        ? st().day2.find(
            (d) =>
              d.enabled &&
              (d.actions ?? []).some((x) => glob(x, action)) &&
              (d.condition !== "environment.production" || env.production),
          )
        : undefined;
      const out: S["PolicyExplanation"] = {
        lease: {
          decided_by: lease ? (lease.project_id ? `${p.name} in ${env.name}` : env.name) : null,
          outcome: lease?.default_days ? `Deployments get ${lease.default_days} days, up to ${lease.max_days ?? "—"}.` : "No lease: deployments here don't expire.",
          considered: [],
        },
        naming: {
          decided_by: naming?.name ?? null,
          outcome: naming ? `VMs are named ${naming.template}.` : "No naming policy here: each blueprint's own template names the VMs.",
          considered: st().naming.map((n) => ({ id: n.id, name: n.name, applies: n.id === naming?.id, why: n.id === naming?.id ? "Offered here." : "Not offered here." })),
        },
        quota: (st().quotas[p.id] ?? []).map((l) => ({ resource: l.resource, scope: l.environment, limit: l.limit, enforcement: l.enforcement, used: 0, reserved: 0 })),
        ...(q.item_id
          ? { approval: { decided_by: deciding.length ? deciding.join(", ") : null, outcome: deciding.length ? `Needs approval: ${deciding.join(", ")}.` : "No approval needed.", considered: approvals } }
          : {}),
        ...(action
          ? { day2: { decided_by: day2?.name ?? null, outcome: day2 ? `${action}: ${day2.effect}.` : `No policy matches: ${action} is denied.`, considered: [] } }
          : {}),
      }; // prettier-ignore
      return HttpResponse.json(out);
    }),

    // ── Integrations ──
    http.get(`${base}/admin/integrations`, async () => {
      await wait();
      return HttpResponse.json({
        items: (["vcenter", "awx", "efficientip"] as const).map((kind) => {
          const it = st().integrations[kind]!;
          return {
            kind,
            configured: Boolean(it.settings.url),
            settings: it.settings,
            secret_set: it.secret,
            locked: kind === "vcenter" ? ["url"] : [],
            restart_needed: Boolean(it.updated_at),
            ...(it.updated_at
              ? { updated_at: it.updated_at, updated_by: { id: db.me.user.id, name: db.me.user.name } }
              : {}),
          };
        }),
      });
    }),
    http.put(`${base}/admin/integrations/:kind`, async ({ params, request }) => {
      await wait();
      const kind = String(params.kind);
      const w = (await request.json()) as S["IntegrationWrite"];
      const url = w.settings.url;
      if (typeof url === "string" && url && !/^https?:\/\//.test(url))
        return invalid([err("/settings/url", "An http or https URL.")]);
      const it = st().integrations[kind]!;
      const locked = kind === "vcenter" ? ["url"] : [];
      it.settings = {
        ...it.settings,
        ...Object.fromEntries(Object.entries(w.settings).filter(([k]) => !locked.includes(k))),
      };
      if (w.secret) it.secret = true;
      it.updated_at = now();
      audited(db, `settings.integration.${kind}`, "integration", kind);
      return HttpResponse.json({
        kind,
        configured: true,
        settings: it.settings,
        secret_set: it.secret,
        locked,
        restart_needed: true,
        updated_at: it.updated_at,
      });
    }),
    http.post(`${base}/admin/integrations/:kind/test`, async ({ params, request }) => {
      await wait();
      const kind = String(params.kind);
      const raw = await request.text();
      const body = raw ? (JSON.parse(raw) as S["IntegrationWrite"]) : undefined;
      const given = body?.settings?.url ?? st().integrations[kind]!.settings.url;
      const url = typeof given === "string" ? given : "";
      const reachable = !url.includes("unreachable");
      const checks: S["IntegrationCheck"][] = [
        {
          name: "Sign in",
          ok: reachable,
          message: reachable ? `Signed in to ${url}.` : `dial tcp: lookup ${url}: no such host`,
        },
      ];
      if (reachable && kind === "vcenter") {
        checks.push({ name: "Inventory", ok: true, message: "4 clusters, 6 datastores, 4 networks and 3 templates." });
        checks.push({ name: "Tagging service", ok: true, message: "Tags can be read, for placement by tag." });
      }
      return HttpResponse.json({ ok: checks.every((c) => c.ok), checks });
    }),
  ];
}
