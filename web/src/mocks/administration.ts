// Administration in the mock backend (S9, ADR-0020): projects, members,
// group mappings, environments, placement, network mappings, templates,
// sizes, naming, quotas, the lease, approval and day-2 policies, the
// explain query and integration settings. Checks follow the server's where
// the UI shows them.
import { http, HttpResponse } from "msw";
import type { components } from "@/lib/api/schema";
import { adminItemOf } from "./catalogAdmin";
import { celCheck } from "./cel";
import { audited } from "./governance";
import { clusters, datastoreClusters, datastores, hostsOf, networks, resourcePools, tags, VCENTER } from "./inventory";
import type { Db } from "./seed";
import { userGroups } from "./logic";
import {
  assignable,
  builtinRoles,
  customRoles,
  heldIn,
  type MockRole,
  meProject,
  permissionCatalogue,
  roleOf,
  roleRefs,
  rolesHandlers,
  strongest,
} from "./roles";
import { localAdminPasswordChanged } from "./signIn";

type S = components["schemas"];

/** A role given to a user in a project: one per role. */
interface Member {
  userId: string;
  role: string;
}

interface AdminState {
  extra: Record<string, { archived_at?: string; created_at: string; properties?: Record<string, string> }>;
  members: Record<string, Member[]>;
  groups: S["GroupMapping"][];
  /** Roles given for all of rvav: those with no id rvav set itself. */
  roleAssignments: S["RoleAssignment"][];
  envs: Record<string, { snapshot_max_count: number; snapshot_max_age_days: number | null; tag_categories: string[] }>;
  rules: S["PlacementRule"][];
  mappings: S["NetworkMapping"][];
  templates: S["VmTemplate"][];
  sizes: S["SizePreset"][];
  naming: S["NamingPolicy"][];
  quotas: Record<string, S["QuotaLimit"][]>;
  leases: S["LeasePolicy"][];
  approvals: S["ApprovalPolicy"][];
  day2: S["Day2Policy"][];
  integrations: Record<
    string,
    { settings: Record<string, unknown>; secret: boolean; signing?: boolean; updated_at?: string; revision?: number }
  >;
  /** vCenters added besides the integration's (ADR-0024). */
  vcenters: S["VCenter"][];
  /** rvav's processes (ADR-0030), and the settings revision. */
  processes: MockProcess[];
  revision: number;
  /** The last connection saved: processes drain, then start again with it. */
  applying?: { revision: number; at: number };
  /** The token AWX's inventory source reads rvav with, and the address it
   * reads rvav at (U1.4). */
  inventory: { token?: S["ApiToken"]; url?: string };
  /** The setup checklist hidden (U1.5). */
  setupHidden: boolean;
  seq: number;
}

interface MockProcess {
  role: S["Process"]["role"];
  host: string;
  revision: number;
  started_at: string;
  /** How long after a connection is saved it has drained and started again. */
  takes: number;
  pid: number;
}

/** Who holds one of these for all of rvav lists every project's name, as the server's projectPickers. */
const PROJECT_PICKERS = [
  "project.settings.read",
  "access.read",
  "audit.read",
  "infrastructure.read",
  "infrastructure.manage",
  "policies.read",
  "policies.manage",
  "extensibility.read",
  "extensibility.manage",
  "import.manage",
];

/** The integrations read on next use (ADR-0030). */
const LIVE_KINDS = ["oidc", "smtp", "teams", "slack", "hosts", "console", "vra"];

/** The integrations, in the order the server lists them. */
const KINDS = [
  "oidc",
  "vcenter",
  "console",
  "awx",
  "efficientip",
  "nsx",
  "git",
  "vra",
  "smtp",
  "teams",
  "slack",
  "hosts",
  "trust",
] as const;

/** A sync interval as the server takes it: 30s to 24h, as 2m or 1h30m. */
function intervalOK(v: string): boolean {
  const m = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(v);
  if (!m || v === "") return false;
  const secs = Number(m[1] ?? 0) * 3600 + Number(m[2] ?? 0) * 60 + Number(m[3] ?? 0);
  return secs >= 30 && secs <= 86400;
}

/** Whether an integration is set up, as the server says. */
function configuredOf(kind: string, it: { settings: Record<string, unknown>; secret: boolean }): boolean {
  const s = it.settings;
  if (kind === "slack") return it.secret;
  if (kind === "console") return Boolean(s.username);
  if (kind === "hosts") return [s.rest_source_hosts, s.webhook_hosts].some((l) => Array.isArray(l) && l.length > 0);
  if (kind === "trust") return typeof s.ca_bundle === "string" && s.ca_bundle.trim() !== "";
  return Boolean(s.url || s.host || s.app_id || s.issuer);
}

/** The processes as they are now: those behind drain, then start again. */
function processesNow(a: AdminState): S["Process"][] {
  const t = Date.now();
  return a.processes.map((p) => {
    let state: S["Process"]["state"] = "running";
    if (a.applying && p.revision < a.applying.revision) {
      // Draining from a third of the way, exited for a moment, then
      // started again with them.
      const gone = t - a.applying.at;
      if (gone >= p.takes * 1.1) {
        p.revision = a.applying.revision;
        p.started_at = new Date().toISOString();
        p.pid += 1;
      } else if (gone >= p.takes) state = "restarting";
      else if (gone >= p.takes / 3) state = "draining";
    }
    return {
      id: `${p.host}/${p.role}/${p.pid}`,
      role: p.role,
      host: p.host,
      version: "1.4.0",
      revision: p.revision,
      state,
      started_at: p.started_at,
      seen_at: new Date().toISOString(),
    };
  });
}

const now = () => new Date().toISOString();

/** An install's settings, in config.Install's order. */
const MOCK_INSTALL: S["InstallSetting"][] = (
  [
    ["RVAV_DATABASE_URL", "postgres://rvav:xxxxx@rvav-db.corp.example:5432/rvav?sslmode=require", true, true],
    ["RVAV_HTTP_ADDR", ":8080", false, false],
    ["RVAV_METRICS_ADDR", ":9090", false, false],
    ["RVAV_LOG_LEVEL", "info", false, true],
    ["RVAV_PUBLIC_URL", "https://rvav.corp.example", false, true],
    ["RVAV_CORS_ORIGINS", "", false, false],
    ["RVAV_DRAIN_TIMEOUT", "5m0s", false, false],
    ["RVAV_ADMIN_PASSWORD", undefined, true, false],
    ["RVAV_AUTH_MODE", "oidc", false, false],
    ["RVAV_ALLOW_DEV_AUTH", "off", false, false],
    ["RVAV_DEV_USER", "lmartin", false, false],
    ["RVAV_TEMPORAL_ADDRESS", "temporal-frontend.rvav.svc:7233", false, true],
    ["RVAV_TEMPORAL_NAMESPACE", "rvav", false, true],
    ["RVAV_TEMPORAL_TASK_QUEUE", "rvav-provision", false, false],
    ["RVAV_TEMPORAL_TLS", "off", false, false],
    ["RVAV_TEMPORAL_TLS_CA_FILE", "", false, false],
    ["RVAV_TEMPORAL_TLS_CERT_FILE", "", false, false],
    ["RVAV_TEMPORAL_TLS_KEY_FILE", "", false, false],
    ["RVAV_TEMPORAL_TLS_SERVER_NAME", "", false, false],
    ["RVAV_TEMPORAL_API_KEY", undefined, true, false],
    ["RVAV_FAKE_EFFICIENTIP", "off", false, false],
    ["RVAV_FAKE_EFFICIENTIP_LATENCY", "0s", false, false],
    ["RVAV_FAKE_AWX", "off", false, false],
    ["RVAV_FAKE_AWX_TASK_DELAY", "1.5s", false, false],
    ["RVAV_FAKE_AWX_FAIL_TASK", "", false, false],
    ["RVAV_GIT_WEBHOOK_SECRET", undefined, true, true],
    ["RVAV_GIT_CACHE", "/tmp/rvav-git", false, false],
    ["RVAV_SECRETS_KEY", undefined, true, true],
    ["RVAV_SECRETS_OLD_KEYS", undefined, true, false],
  ] as const
).map(([name, value, secret, set]) => ({ name, ...(value === undefined ? {} : { value }), secret, set }));

/** A setting's text, "" when it isn't a string. */
const text = (v: unknown): string => (typeof v === "string" ? v : "");

const seedProperties: Record<string, Record<string, string>> = { prj_web: { backupPolicy: "gold" } };

/** The administration's state, made on first use from the projects the mock holds then. */
export function adminState(db: Db): AdminState {
  const d = db as Db & { administration?: AdminState };
  if (d.administration) return d.administration;
  const ago = (days: number) => new Date(Date.now() - days * 86_400_000).toISOString();
  // A new install has no projects: it gets none of what refers to them.
  const ref = (id: string) => {
    const p = db.projects.find((x) => x.id === id) ?? { id, code: id, name: id };
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
        storage_policy: "",
        vcenter: "",
        cluster_tags: [],
        datastore_tags: [],
        resource_pool: "",
        hosts: [],
        datastore_cluster: "",
        strategy: "most_free_memory",
      });
    }
  }
  const level = (name: string, group: string): S["ApprovalLevel"] => ({ name, group, mode: "any" });
  d.administration = {
    seq: 1000,
    extra: Object.fromEntries(
      db.projects.map((p) => [p.id, { created_at: ago(400), properties: { ...seedProperties[p.id] } }]),
    ),
    // The mock user's roles in a project are /me's.
    members: Object.fromEntries(
      (
        [
          ["prj_web", [{ userId: "usr_tnguyen", role: "member" }]],
          ["prj_data", [{ userId: "usr_sbrunner", role: "project-admin" }]],
        ] as [string, Member[]][]
      ).map(([id, list]) => [
        id,
        [
          ...(db.me.projects.find((p) => p.project.id === id)?.roles ?? []).map((r) => ({
            userId: db.me.user.id,
            role: r.key,
          })),
          ...list,
          ...(db.seedMembers ?? []).filter((m) => m.projectId === id).map(({ userId, role }) => ({ userId, role })),
        ],
      ]),
    ),
    groups: [
      { id: "gmp_admins", oidc_group: "rvav-admins", project: null, role: "admin", role_name: "Administrator", users: 1 },
      { id: "gmp_catalog", oidc_group: "rvav-catalog-admins", project: null, role: "catalog-admin", role_name: "Catalog admin", users: 1 },
      { id: "gmp_web", oidc_group: "rvav-web", project: ref("prj_web"), role: "member", role_name: "Member", users: 1 },
      { id: "gmp_data", oidc_group: "rvav-data", project: ref("prj_data"), role: "member", role_name: "Member", users: 1 },
    ], // prettier-ignore
    // A persona's custom role was given by someone; built-in ones rvav set.
    roleAssignments: db.me.roles
      .filter((r) => r.key !== "user")
      .map((r) =>
        roleOf(null, r.key)
          ? { user: db.me.user, role: { key: r.key, name: r.name }, fixed: true }
          : {
              id: "ras_persona",
              user: db.me.user,
              role: { key: r.key, name: r.name },
              fixed: false,
              created_at: ago(30),
            },
      ),
    envs: Object.fromEntries(
      db.environments.map((e) => [
        e.key,
        { snapshot_max_count: 3, snapshot_max_age_days: 7, tag_categories: ["backup"] },
      ]),
    ),
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
      ...(n.name === "dvpg-app-dev-vl310" ? { subnet6: "2001:db8:310::/64", gateway6: "2001:db8:310::1" } : {}),
    })),
    templates: [
      {
        key: "rhel9", label: "RHEL 9.4", os_family: "linux", customization: "",
        sources: [{ vcenter: "vc01.corp.example", template: "tpl-rhel9.4-2026.10", content_library: "", in_inventory: true }],
        description: "With the corporate baseline.", offers: [{ project_id: null, environment: null }],
        user_data: "#cloud-config\npackages: [chrony, qualys-agent]", constraints: { compute: [], storage: [], network: [] },
        linked_clone: false, cloudbase_init: false,
      },
      {
        key: "win2025", label: "Windows Server 2025", os_family: "windows", customization: "", description: "",
        sources: [{ vcenter: "vc01.corp.example", template: "tpl-win2025-std-2026.09", content_library: "", in_inventory: true }],
        offers: [{ project_id: "prj_web", environment: null }],
        user_data: "", constraints: { compute: [], storage: [], network: [] }, linked_clone: false, cloudbase_init: false,
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
      ...(db.seedDay2 ?? []),
      {
        id: "d2p_members", name: "Members operate their VMs", reason: "", priority: 50, actions: ["*"],
        roles: ["owner", "member", "project-admin"], condition: "true", effect: "allow", project_id: null, enabled: true,
      },
    ], // prettier-ignore
    vcenters: [],
    inventory: {},
    revision: 1,
    processes: [
      { role: "api", host: "rvav-api-7d9f-2k4xq", revision: 1, started_at: ago(3), takes: 600, pid: 1 },
      { role: "worker", host: "rvav-worker-5c6b-8wq2n", revision: 1, started_at: ago(3), takes: 900, pid: 1 },
      { role: "worker", host: "rvav-worker-5c6b-tz7vd", revision: 1, started_at: ago(3), takes: 1200, pid: 1 },
      { role: "sync", host: "rvav-sync-69d4-m3b8r", revision: 1, started_at: ago(3), takes: 450, pid: 1 },
    ],
    integrations: {
      oidc: { settings: { issuer: "https://login.corp.example/realms/rvav", discovery_url: "", client_id: "rvav", scopes: "openid profile email", groups_claim: "groups", manager_claim: "manager" }, secret: false },
      vcenter: { settings: { url: "https://vc01.corp.example/sdk", username: "svc-rvav@vsphere.local", insecure: false, cert_sha256: "", name: "vc01.corp.example", sync_interval: "2m" }, secret: true,
        revision: 1, updated_at: new Date(Date.now() - 3 * 86_400_000).toISOString() },
      console: { settings: { username: "svc-rvav-console@vsphere.local", sdk_urls: [] }, secret: true },
      awx: { settings: { url: "https://awx.corp.example", organization: "rvav", catalog_labels: ["catalog"], action_labels: ["day2"], inventory_source_id: 12, insecure: false, cert_sha256: "", sync_interval: "5m" }, secret: true },
      efficientip: { settings: { url: "https://ipam.corp.example", auth: "native", username: "rvav", space: "", dns_server: "ns1.corp.example", dns_view: "", instance_id: "rvav", insecure: false, cert_sha256: "", sync_interval: "5m" }, secret: true },
      nsx: { settings: { url: "https://nsx.corp.example", username: "svc-rvav", cert_file: "", key_file: "", insecure: false, cert_sha256: "" }, secret: true },
      git: { settings: { url: "", branch: "main", dir: "catalog", username: "", known_hosts: "", insecure: false, committer: "rvav@corp.example", sync_interval: "5m" }, secret: false },
      vra: { settings: { url: "", username: "", domain: "", insecure: false, cert_sha256: "" }, secret: false },
      smtp: { settings: { host: "smtp.corp.example", port: 587, tls: "starttls", from: "rvav <rvav@corp.example>", username: "" }, secret: false },
      teams: { settings: { app_id: "", tenant_id: "", service_url: "https://smba.trafficmanager.net/emea", login_url: "https://login.microsoftonline.com", graph_url: "https://graph.microsoft.com", openid_url: "https://login.botframework.com/v1/.well-known/openidconfiguration" }, secret: false },
      slack: { settings: { api_url: "https://slack.com/api" }, secret: false, signing: false },
      hosts: { settings: { rest_source_hosts: ["cmdb.corp.example"], webhook_hosts: ["hooks.corp.example"] }, secret: false },
      trust: { settings: { ca_bundle: "" }, secret: false },
    }, // prettier-ignore
    setupHidden: false,
  };
  if (db.fresh) {
    // A new install: nothing set but what rvav starts with.
    const a = d.administration;
    Object.assign(a, { groups: [], mappings: [], templates: [], quotas: {}, vcenters: [], inventory: {} });
    for (const it of Object.values(a.integrations)) {
      for (const [k, v] of Object.entries(it.settings)) {
        const address =
          /^(url|issuer|discovery_url|client_id|host|username|app_id|tenant_id|organization|dns_server|name)$/;
        if (address.test(k) && typeof v === "string") it.settings[k] = "";
        if (Array.isArray(v)) it.settings[k] = [];
      }
      it.secret = false;
      delete it.revision;
      delete it.updated_at;
    }
  }
  return d.administration;
}

const plural = (n: number, what: string) => `${n} ${what}${n === 1 ? "" : "s"}`;

/** The setup checklist, from what the mock holds, as the server reads it. */
function setupOf(db: Db): S["Setup"] {
  const a = adminState(db);
  const connection = (key: S["SetupStep"]["key"], kind: string): S["SetupStep"] => {
    const it = a.integrations[kind]!;
    const url = typeof it.settings.url === "string" ? it.settings.url : "";
    return configuredOf(kind, it) ? { key, done: true, detail: url } : { key, done: false, detail: "Not set." };
  };
  const issuer = typeof a.integrations.oidc!.settings.issuer === "string" ? a.integrations.oidc!.settings.issuer : "";
  const managesAccess = (g: S["GroupMapping"]) =>
    !g.project && roleOf(db, g.role)?.permissions.includes("access.manage");
  const admins = [...new Set(a.groups.filter(managesAccess).map((g) => g.oidc_group))].sort();
  const published = db.items.filter((i) => adminItemOf(db, i).published_version).length;
  const changed = localAdminPasswordChanged();
  const placements = a.rules.filter((r) => db.projects.some((p) => p.id === r.project.id)).length;
  const steps: S["SetupStep"][] = [
    {
      key: "password",
      done: changed,
      detail: changed ? "Changed from the install's." : "The install's password is still the administrator's.",
    },
    issuer
      ? { key: "provider", done: true, detail: issuer }
      : { key: "provider", done: false, detail: "None: only the local administrator can sign in." },
    admins.length > 0
      ? { key: "admin_group", done: true, detail: admins.join(", ") }
      : {
          key: "admin_group",
          done: false,
          detail: "No group is mapped to admin, nor to another role holding access.manage.",
        },
    connection("vcenter", "vcenter"),
    {
      key: "projects",
      done: db.environments.length > 0 && db.projects.length > 0 && placements > 0,
      detail: `${plural(db.environments.length, "environment")}, ${plural(db.projects.length, "project")}, ${plural(placements, "placement rule")}`,
    },
    connection("ipam", "efficientip"),
    connection("awx", "awx"),
    { key: "catalog_item", done: published > 0, detail: `${plural(db.items.length, "item")}, ${published} published` },
  ];
  const complete = steps.every((s) => s.done);
  return { steps, complete, hidden: a.setupHidden, open: !complete && !a.setupHidden && db.fresh };
}

/** The lease policy that applies to a project's environment. */
export function leasePolicyOf(db: Db, projectId: string, env: string): S["LeasePolicy"] | undefined {
  const all = adminState(db).leases.filter((l) => l.environment === env);
  return all.find((l) => l.project_id === projectId) ?? all.find((l) => !l.project_id);
}

/** The roles a user holds in a project: given to them there, or through a group. */
export function rolesGiven(db: Db, projectId: string, userId: string): string[] {
  return [
    ...(adminState(db).members[projectId] ?? []).filter((m) => m.userId === userId).map((m) => m.role),
    ...groupGrants(db, userId)
      .filter((g) => g.project?.id === projectId)
      .map((g) => g.role),
  ];
}

/** The group mappings that give a user a role, by their groups at their last sign-in. */
export function groupGrants(db: Db, userId: string): S["GroupMapping"][] {
  const groups = userGroups[userId] ?? [];
  return adminState(db).groups.filter((g) => groups.includes(g.oidc_group));
}

/** Says whether the mock user holds a permission in a project, a day-2 action through its group too. */
function holdsIn(db: Db, projectId: string, permission: string): boolean {
  const held = new Set([
    ...db.me.permissions,
    ...(db.me.projects.find((p) => p.project.id === projectId)?.permissions ?? []),
  ]);
  const group = /^(day2\.[a-z]+):/.exec(permission)?.[1];
  return held.has(permission) || Boolean(group && held.has(group));
}

/** Says whether the mock user may give a role in a project, or take it away, as the server's mayGive. */
export function mayGive(db: Db, r: MockRole, projectId: string): boolean {
  if (db.me.permissions.includes("access.manage")) return true;
  return holdsIn(db, projectId, "project.members.manage") && r.permissions.every((p) => holdsIn(db, projectId, p));
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
  return adminState(db).rules.some(
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
  return adminState(db)
    .rules.filter((r) => r.project.id === projectId && r.environment === env)
    .sort((a, b) => a.priority - b.priority)[0]?.folder;
}

/** As the server: a custom property's key names a vSphere custom attribute. */
function propertyProblems(props: Record<string, string>): S["FieldError"][] {
  return Object.entries(props).flatMap(([k, v]) => {
    if (!/^[A-Za-z][A-Za-z0-9._-]{0,63}$/.test(k))
      return [err(`/properties/${k}`, "A key of up to 64 letters, digits, '.', '_' and '-', starting with a letter.")];
    if (v.length > 512) return [err(`/properties/${k}`, "A value of up to 512 characters.")];
    return [];
  });
}

/** As the server: a naming counter starts at 0 or more and moves by 1 to 1000. */
function counterProblems(start: number, increment: number): S["FieldError"][] {
  const out: S["FieldError"][] = [];
  if (!Number.isInteger(start) || start < 0 || start > 999_999_999)
    out.push(err("/start", "The counter starts between 0 and 999999999."));
  if (!Number.isInteger(increment) || increment < 1 || increment > 1000)
    out.push(err("/increment", "The counter moves by 1 to 1000."));
  return out;
}

/** The naming policy offered to a project in an environment, the most specific winning. */
export function namingTemplate(db: Db, projectId: string, env: string): string | undefined {
  let best: { template: string; score: number } | undefined;
  for (const n of adminState(db).naming) {
    for (const o of n.offers) {
      if ((o.project_id && o.project_id !== projectId) || (o.environment && o.environment !== env)) continue;
      const score = (o.project_id ? 2 : 0) + (o.environment ? 1 : 0);
      if (!best || score > best.score) best = { template: n.template, score };
    }
  }
  return best?.template;
}

/** As the server: a saved secret only goes to the address it was given for. */
function secretMoves(current: Record<string, unknown>, given: Record<string, unknown>, secret: string | undefined) {
  const url = given.url;
  const pin = typeof given.cert_sha256 === "string" ? normalFingerprint(given.cert_sha256) : "";
  const moved =
    (typeof url === "string" && url !== "" && url !== current.url) ||
    (given.insecure === true && current.insecure !== true) ||
    (pin !== "" && pin !== normalFingerprint(text(current.cert_sha256)));
  return moved && !secret
    ? invalid([
        err(
          "/secret",
          "A new address, a certificate pinned, or TLS checks turned off, needs the password or token again: the saved one only goes where it was given for.",
        ),
      ])
    : null;
}

// ── Certificates (U1.4, ADR-0030) ──
// A server's certificate as the mock has it: a lab host's is self-signed,
// another's issued by the corporate CA the system trusts.

/** A SHA-256 as AB:CD:…, made up from a name. */
function fingerprint(name: string): string {
  const out: string[] = [];
  let h = 2166136261;
  for (let i = 0; i < 32; i++) {
    for (const c of `${name}#${i}`) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
    out.push((h & 0xff).toString(16).padStart(2, "0").toUpperCase());
  }
  return out.join(":");
}

/** A fingerprint given with or without colons, as AB:CD:…; "" when it isn't one. */
function normalFingerprint(v: string): string {
  const hex = v.replace(/^sha256:?/i, "").replace(/[\s:-]/g, "");
  return /^[0-9a-f]{64}$/i.test(hex) ? (hex.toUpperCase().match(/../g) ?? []).join(":") : "";
}

const mockPEM = (subject: string) =>
  `-----BEGIN CERTIFICATE-----\n${btoa(`rvav-mock:${subject}`)}\n-----END CERTIFICATE-----\n`;

/** The PEM blocks of a bundle, or why it isn't one, as the server says. */
function pemBlocks(bundle: string): { subjects: string[]; problem?: string } {
  const text = bundle.trim();
  const subjects: string[] = [];
  const re = /-----BEGIN ([A-Z ]+)-----\s*([\s\S]*?)\s*-----END \1-----/g;
  let rest = text;
  for (const m of text.matchAll(re)) {
    if (m[1] !== "CERTIFICATE")
      return {
        subjects,
        problem: m[1]!.includes("PRIVATE KEY")
          ? "a private key: the bundle holds the CAs' certificates only"
          : `a ${m[1]}, not a certificate`,
      };
    let subject = "Corp Root CA";
    try {
      const raw = atob(m[2]!.replace(/\s/g, ""));
      if (raw.startsWith("rvav-mock:")) subject = raw.slice("rvav-mock:".length);
    } catch {
      return { subjects, problem: `certificate ${subjects.length + 1}: malformed certificate` };
    }
    subjects.push(subject);
    rest = rest.replace(m[0], "");
  }
  if (rest.trim() !== "")
    return {
      subjects,
      problem: "not PEM: certificates between -----BEGIN CERTIFICATE----- and -----END CERTIFICATE-----",
    };
  return { subjects };
}

const longDate = (iso: string) =>
  new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" });

/** The certificate check of a connection's test, and whether the test goes on. */
function certificateCheck(
  bundle: string,
  rawURL: string,
  settings: Record<string, unknown>,
  pinnable: boolean,
): { check?: S["IntegrationCheck"]; certificate?: S["ServerCertificate"]; goOn: boolean } {
  let u: URL;
  try {
    u = new URL(rawURL);
  } catch {
    return { goOn: true };
  }
  if (u.protocol !== "https:") return { goOn: true };
  const host = u.hostname;
  const address = `${host}:${u.port || "443"}`;
  if (host.includes("unreachable"))
    return {
      check: {
        name: "Certificate",
        ok: false,
        message: `${address} gave none: dial tcp: lookup ${host}: no such host`,
      },
      goOn: false,
    };
  const selfSigned = /lab|self-signed/.test(host);
  const anchor = selfSigned ? host : "Corp Issuing CA 2";
  const sha256 = fingerprint(host);
  const inBundle = pemBlocks(bundle).subjects.includes(anchor);
  const trusted = !selfSigned || inBundle;
  const pinned = pinnable && normalFingerprint(text(settings.cert_sha256)) === sha256;
  const checked = settings.insecure !== true;
  const certificate: S["ServerCertificate"] = {
    address,
    subject: host,
    issuer: selfSigned ? host : "Corp Issuing CA 2",
    names: [host],
    not_before: "2026-01-15T00:00:00Z",
    not_after: "2027-01-15T00:00:00Z",
    sha256,
    trusted,
    pinned,
    checked,
    pinnable,
    ...(trusted ? {} : { problem: "no CA rvav trusts signed it" }),
    anchor_pem: mockPEM(anchor),
    anchor_subject: anchor,
    anchor_is_leaf: selfSigned,
  };
  const about = `${host}, issued by ${certificate.issuer}, until ${longDate(certificate.not_after)}`;
  if (!checked)
    return {
      certificate,
      check: { name: "Certificate", ok: true, message: `Not checked: TLS checks are off. ${about}.` },
      goOn: true,
    };
  if (trusted)
    return {
      certificate,
      check: { name: "Certificate", ok: true, message: `${about}: a CA rvav trusts vouches for it.` },
      goOn: true,
    };
  if (pinned)
    return {
      certificate,
      check: { name: "Certificate", ok: true, message: `${about}: pinned for this connection.` },
      goOn: true,
    };
  const fix = pinnable
    ? "Trust this certificate, or add its CA to the CA bundle."
    : "Add its CA to the CA bundle (Trusted certificates).";
  return {
    certificate,
    check: { name: "Certificate", ok: false, message: `${about}: no CA rvav trusts signed it. ${fix}` },
    goOn: false,
  };
}

/** A pin or a bundle that can't be read, as the server refuses them. */
function trustErrors(kind: string, settings: Record<string, unknown>): S["FieldError"][] {
  const out: S["FieldError"][] = [];
  const pin = settings.cert_sha256;
  if (typeof pin === "string" && pin.trim() !== "" && !normalFingerprint(pin))
    out.push(err("/settings/cert_sha256", "A certificate's SHA-256: 32 bytes in hex, with or without colons."));
  const bundle = settings.ca_bundle;
  if (kind === "trust" && typeof bundle === "string") {
    const { problem: p } = pemBlocks(bundle);
    if (p) out.push(err("/settings/ca_bundle", `CA certificates in PEM: ${p}.`));
  }
  return out;
}

export function administrationHandlers(db: Db, base: string, wait: () => Promise<void>) {
  const st = () => adminState(db);
  /** A save's revision: a connection's drains the processes, one read on next use doesn't. */
  const saved = (kind: string) => {
    const a = st();
    a.revision += 1;
    if (LIVE_KINDS.includes(kind)) {
      for (const p of a.processes) if (!a.applying || p.revision >= a.applying.revision) p.revision = a.revision;
    } else a.applying = { revision: a.revision, at: Date.now() };
    return a.revision;
  };
  const applyingOf = (kind: string, revision: number) => ({
    revision,
    applying: !LIVE_KINDS.includes(kind) && processesNow(st()).some((p) => p.revision < revision),
  });
  const primaryVCenter = () => (st().integrations.vcenter?.settings.name as string | undefined) ?? "";
  const templateSourceErrors = (sources: S["VmTemplateSourceWrite"][]) => {
    const out: S["FieldError"][] = [];
    if (sources.length === 0) out.push(err("/sources", "Name the template in a vCenter at least."));
    const seen = new Set<string>();
    sources.forEach((src, i) => {
      const vc = src.vcenter === primaryVCenter() ? "" : src.vcenter;
      if (vc && !st().vcenters.some((v) => v.name === vc))
        out.push(err(`/sources/${i}/vcenter`, `rvav manages no vCenter ${vc}.`));
      else if (seen.has(vc))
        out.push(err(`/sources/${i}/vcenter`, "A template has one source per vCenter: this one has two."));
      seen.add(vc);
      if (!src.template.trim()) out.push(err(`/sources/${i}/template`, "Name the template in this vCenter."));
      else if (src.template.startsWith("library:"))
        out.push(err(`/sources/${i}/template`, "Name the content library item, and its library in Content library."));
    });
    return out;
  };
  const templateErrors = (w: S["VmTemplateWrite"]) => {
    const out = templateSourceErrors(w.sources ?? []);
    const ud = (w.user_data ?? "").trim();
    const windows = w.os_family === "windows";
    if (w.cloudbase_init && !windows)
      out.push(
        err("/cloudbase_init", "cloudbase-init is for Windows templates: a Linux template's VMs get cloud-init."),
      );
    else if (w.cloudbase_init && w.customization?.trim())
      out.push(
        err(
          "/cloudbase_init",
          "A template whose VMs cloudbase-init configures doesn't use a customization specification: clear one of them.",
        ),
      );
    if (ud && windows && !w.cloudbase_init)
      out.push(
        err(
          "/user_data",
          "A Windows template's VMs read user data with cloudbase-init: mark the template as using it.",
        ),
      );
    else if (ud && windows) {
      if (!["#cloud-config", "#ps1", "rem cmd", "#!"].some((p) => ud.startsWith(p)))
        out.push(err("/user_data", "Start the user data with #cloud-config, #ps1_sysnative, #ps1, rem cmd or #!."));
    } else if (ud && w.customization?.trim())
      out.push(
        err(
          "/user_data",
          "A template whose VMs cloud-init configures doesn't use a customization specification: clear one of them.",
        ),
      );
    else if (ud && !ud.startsWith("#cloud-config") && !ud.startsWith("#!"))
      out.push(err("/user_data", "Start the user data with #cloud-config, or with #! for a script."));
    for (const kind of ["compute", "storage", "network"] as const)
      (w.constraints?.[kind] ?? []).forEach((tag, i) => {
        if (tag.includes("${"))
          out.push(
            err(
              `/constraints/${kind}/${i}`,
              "A template's constraints are tags as they are: ${...} is for blueprints.",
            ),
          );
        else if (!/^!?[^/:]+\/[^/:]+(:hard|:soft)?$/.test(tag.trim()))
          out.push(err(`/constraints/${kind}/${i}`, `"${tag}" is not category/tag, as rvav-tier/gold.`));
      });
    if (w.linked_clone && (w.sources ?? []).some((src) => src.content_library?.trim()))
      out.push(
        err(
          "/linked_clone",
          "A content library item is deployed, not cloned: a linked clone needs a VM template in each vCenter.",
        ),
      );
    return out;
  };
  const storedTemplate = (w: S["VmTemplateWrite"]) => ({
    user_data: (w.user_data ?? "").trim(),
    linked_clone: w.linked_clone ?? false,
    cloudbase_init: w.cloudbase_init ?? false,
    constraints: {
      compute: w.constraints?.compute ?? [],
      storage: w.constraints?.storage ?? [],
      network: w.constraints?.network ?? [],
    },
    sources: (w.sources ?? []).map((src): S["VmTemplateSource"] => ({
      vcenter: src.vcenter || primaryVCenter(),
      template: src.template.trim(),
      content_library: src.content_library ?? "",
      in_inventory: true,
    })),
  });
  const project = (id: string) => db.projects.find((p) => p.id === id || p.code === id);
  const user = (id: string) => db.users.find((u) => u.id === id);
  const adminProject = (p: Db["projects"][number]): S["AdminProject"] => {
    const x = st().extra[p.id] ?? { created_at: now() };
    return {
      ...p,
      deployment_sharing: p.deployment_sharing ?? "all_members",
      archived: Boolean(x.archived_at),
      ...(x.archived_at ? { archived_at: x.archived_at } : {}),
      created_at: x.created_at,
      properties: x.properties ?? {},
      members: (st().members[p.id] ?? []).length,
      deployments: db.deployments.filter((d) => d.project.id === p.id && d.status !== "deleted").length,
    };
  };
  const rolesOfUser = (projectId: string, userId: string) =>
    (st().members[projectId] ?? []).filter((m) => m.userId === userId).map((m) => m.role);
  const viaGroups = (projectId: string, userId: string) =>
    groupGrants(db, userId).filter((g) => g.project?.id === projectId);
  const members = (projectId: string): S["ProjectMember"][] => {
    const ids = [
      ...new Set([
        ...(st().members[projectId] ?? []).map((m) => m.userId),
        ...db.users.filter((u) => viaGroups(projectId, u.id).length > 0).map((u) => u.id),
      ]),
    ];
    return ids.map((userId) => {
      const direct = rolesOfUser(projectId, userId).sort();
      const groups = viaGroups(projectId, userId);
      const keys = [...new Set([...direct, ...groups.map((g) => g.role)])];
      const directRole = strongest(direct);
      return {
        user: user(userId) ?? { id: userId, name: userId },
        role: strongest(keys),
        roles: roleRefs(db, keys)
          .map((r) => ({
            ...r,
            direct: direct.includes(r.key),
            groups: groups.filter((g) => g.role === r.key).map((g) => g.oidc_group),
          }))
          .sort((a, b) => a.name.localeCompare(b.name)),
        direct: direct.length > 0,
        ...(directRole ? { direct_role: directRole } : {}),
        direct_roles: direct,
        groups: [...new Set(groups.map((g) => g.oidc_group))],
      };
    });
  };
  const forbidden = (detail: string) => problem(403, "Not allowed", undefined, detail);
  const holdsAnywhere = (perm: string) =>
    db.me.permissions.includes(perm) || db.me.projects.some((p) => p.permissions.includes(perm));
  // As the server's readsPolicies, showsPolicy and writesPolicy (ADR-0045).
  const policies = {
    reads: () => db.me.permissions.includes("policies.read") || holdsAnywhere("project.policies.manage"),
    shows: (projectId?: string | null) =>
      db.me.permissions.includes("policies.read") || !projectId || holdsIn(db, projectId, "project.policies.manage"),
    global: () => db.me.permissions.includes("policies.manage"),
    writes: (projectId?: string | null) =>
      db.me.permissions.includes("policies.manage") ||
      (Boolean(projectId) && holdsIn(db, projectId!, "project.policies.manage")),
  };
  const refusedPolicy = (projectId?: string | null) =>
    projectId
      ? forbidden(`you may not write the policies of ${project(projectId)?.name ?? projectId}`)
      : forbidden("you may not write the policies of every project");
  /** As the server's ExplainAccess: the grants that count, and what each permission is held through. */
  const explain = (u: S["UserRef"], p: Db["projects"][number] | null): S["AccessExplanation"] => {
    // The mock user's roles for all of rvav are its persona's: none of its groups gives it another.
    const personaHolds = (name: string) =>
      st().groups.every((g) => g.oidc_group !== name || g.project || db.me.roles.some((r) => r.key === g.role));
    const groups = (userGroups[u.id] ?? []).filter((name) => u.id !== db.me.user.id || personaHolds(name));
    const grants: { grant: S["AccessGrant"]; permissions: string[] }[] = [];
    const add = (
      key: string,
      given: S["AccessGrant"]["given"],
      through: S["AccessGrant"]["through"],
      group?: string,
    ) => {
      const r = roleOf(db, key);
      if (!r || !assignable(r, given === "project")) return;
      const same = ({ grant: g }: (typeof grants)[number]) =>
        g.role.key === key && g.given === given && g.through === through && g.group === group;
      if (grants.some(same)) return;
      const grant = { role: { key: r.key, name: r.name }, given, through, ...(group ? { group } : {}) };
      grants.push({ grant, permissions: r.permissions });
    };
    add("user", "rvav", "rvav");
    for (const a of st().roleAssignments.filter((a) => a.user.id === u.id))
      add(a.role.key, "rvav", a.fixed ? "rvav" : "user");
    if (p) for (const m of st().members[p.id] ?? []) if (m.userId === u.id) add(m.role, "project", "user");
    for (const g of st().groups.filter((g) => groups.includes(g.oidc_group))) {
      if (!g.project) add(g.role, "rvav", "group", g.oidc_group);
      else if (g.project.id === p?.id) add(g.role, "project", "group", g.oidc_group);
    }
    const by = new Map<string, S["AccessGrant"][]>();
    for (const { grant, permissions } of grants)
      for (const perm of permissions)
        if (perm !== "approval.project" || grant.given === "project") by.set(perm, [...(by.get(perm) ?? []), grant]);
    const day2 = [...by.keys()].filter((k) => k.startsWith("day2.")).sort();
    return {
      user: u,
      project: p ? { id: p.id, code: p.code, name: p.name } : null,
      groups,
      roles: grants.map((g) => g.grant),
      permissions: [...permissionCatalogue.map((c) => c.id), ...day2].map((id) => ({
        id,
        held: by.has(id),
        granted_by: by.get(id) ?? [],
      })),
    };
  };

  /** What the server refuses in a change of someone's roles in a project: roles the mock user may not give or take away. */
  const refusal = (projectId: string, current: string[], keys: string[]) => {
    const p = project(projectId);
    if (!p) return problem(404, "Not found");
    if (!holdsIn(db, p.id, "project.members.manage")) return forbidden("you may not manage its members");
    for (const k of keys.filter((k) => !current.includes(k))) {
      const r = roleOf(db, k)!;
      if (!mayGive(db, r, p.id))
        return forbidden(`you may not give ${r.name}: it allows what you may not do in ${p.name}`);
    }
    for (const k of current.filter((k) => !keys.includes(k))) {
      const r = roleOf(db, k);
      if (r && !mayGive(db, r, p.id))
        return forbidden(`you may not take ${r.name} away: it allows what you may not do in ${p.name}`);
    }
    return null;
  };
  /** Keeps /me's project current when the mock user's own roles there change. */
  const refreshMe = (projectId: string) => {
    const pr = project(projectId)!;
    const given = rolesOfUser(projectId, db.me.user.id);
    const others = db.me.projects.filter((x) => x.project.id !== projectId);
    const rvav = db.me.roles.map((r) => r.key);
    const seen = given.length > 0 || heldIn(db, rvav, []).includes("project.see");
    db.me.projects = seen
      ? [...others, meProject(db, { id: pr.id, code: pr.code, name: pr.name }, rvav, given)].sort((a, b) =>
          a.project.name.localeCompare(b.project.name),
        )
      : others;
  };
  const manages = (key: string) => roleOf(db, key)?.permissions.includes("project.members.manage") ?? false;
  const managesAccess = (key: string) => roleOf(db, key)?.permissions.includes("access.manage") ?? false;
  /** As the server's ProjectHolders: the ways a project has project.members.manage given in it, but these. */
  const membersManagers = (projectId: string, except: { user?: string; mapping?: string; role?: string }) => {
    const counts = (key: string) => key !== except.role && manages(key);
    return (
      (st().members[projectId] ?? []).filter((m) => m.userId !== except.user && counts(m.role)).length +
      st().groups.filter((g) => g.project?.id === projectId && g.id !== except.mapping && counts(g.role)).length
    );
  };
  const noManager = (projectId: string) =>
    problem(
      409,
      "Conflict",
      undefined,
      `${project(projectId)?.name ?? projectId} would have no project admin: make someone else one first.`,
    );
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
        items: db.users.filter((u) => u.id === q || `${u.name} ${u.email ?? ""}`.toLowerCase().includes(q)),
      });
    }),
    http.get(`${base}/admin/projects`, async ({ request }) => {
      await wait();
      if (!db.me.permissions.includes("project.settings.read"))
        return forbidden("you may not read every project's settings");
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
      errors.push(...propertyProblems(c.properties ?? {}));
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
      };
      db.projects.push(p);
      db.quota[p.id] = {
        limits: { vcpu: null, memory_gb: null, storage_gb: null, vm_count: null },
        used: { vcpu: 0, memory_gb: 0, storage_gb: 0, vm_count: 0 },
      };
      st().extra[p.id] = { created_at: now(), properties: c.properties ?? {} };
      st().members[p.id] = (c.project_admins ?? []).map((u) => ({ userId: u, role: "project-admin" }));
      refreshMe(p.id);
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
      const errors = propertyProblems(u.properties ?? {});
      if (errors.length) return invalid(errors);
      if (u.properties) st().extra[p.id]!.properties = u.properties;
      if (u.name !== undefined) p.name = u.name;
      if (u.description !== undefined) p.description = u.description;
      if (u.cost_center !== undefined) p.cost_center = u.cost_center;
      if (u.environments) p.environments = u.environments;
      if (u.archived !== undefined) st().extra[p.id]!.archived_at = u.archived ? now() : undefined;
      audited(db, u.archived === true ? "project.archive" : "project.update", "project", p.id);
      return HttpResponse.json(adminProject(p));
    }),
    http.get(`${base}/projects/:id/assignable-roles`, async ({ params }) => {
      await wait();
      const p = project(String(params.id));
      if (!p) return problem(404, "Not found");
      if (!holdsIn(db, p.id, "project.members.manage")) return forbidden("you may not manage its members");
      return HttpResponse.json({
        items: [...builtinRoles, ...customRoles(db)]
          .filter((r) => r.key !== "user" && assignable(r, true) && mayGive(db, r, p.id))
          .map(({ key, name, description, permissions, built_in }) => ({
            key,
            name,
            description,
            permissions,
            built_in,
          })),
      });
    }),
    http.get(`${base}/admin/project-names`, async ({ request }) => {
      await wait();
      if (!PROJECT_PICKERS.some((p) => db.me.permissions.includes(p)))
        return forbidden("you may not see every project's name");
      const archived = new URL(request.url).searchParams.get("archived") === "true";
      return HttpResponse.json({
        items: db.projects
          .map(adminProject)
          .filter((p) => archived || !p.archived)
          .map(({ id, code, name, archived }) => ({ id, code, name, archived })),
      } satisfies S["ProjectNameList"]);
    }),
    http.get(`${base}/admin/access/explain`, async ({ request }) => {
      await wait();
      const url = new URL(request.url);
      const anyone = db.me.permissions.includes("access.read");
      const u = user(url.searchParams.get("user_id") ?? "");
      if (!anyone && u?.id !== db.me.user.id) return forbidden("you may not see what someone else may do");
      if (!u) return problem(404, "Not found");
      const projectId = url.searchParams.get("project_id");
      const p = projectId ? project(projectId) : null;
      if (projectId && (!p || (!anyone && !db.me.projects.some((m) => m.project.id === p.id))))
        return problem(404, "Not found");
      return HttpResponse.json(explain(u, p ?? null));
    }),
    http.get(`${base}/projects/:id/members`, async ({ params }) => {
      await wait();
      return HttpResponse.json({ items: members(String(params.id)) });
    }),
    http.put(`${base}/projects/:id/members/:userId`, async ({ params, request }) => {
      await wait();
      const id = String(params.id);
      const userId = String(params.userId);
      const w = (await request.json()) as S["ProjectMemberWrite"];
      const keys = w.roles ?? (w.role ? [w.role] : []);
      const field = w.roles ? "/roles" : "/role";
      if (keys.length === 0) return invalid([err("/roles", "Give the roles, or one role.")]);
      const errors = keys.flatMap((k, i) => {
        const r = roleOf(db, k);
        const at = w.roles ? `${field}/${i}` : field;
        if (!r) return [err(at, `"${k}" is no role.`)];
        return assignable(r, true) ? [] : [err(at, `${r.name} is given for all of rvav, not in a project.`)];
      });
      if (errors.length) return invalid(errors);
      const refused = refusal(id, rolesOfUser(id, userId), keys);
      if (refused) return refused;
      const list = (st().members[id] ??= []);
      const current = rolesOfUser(id, userId);
      if (current.some(manages) && !keys.some(manages) && membersManagers(id, { user: userId }) === 0)
        return noManager(id);
      st().members[id] = [
        ...list.filter((x) => x.userId !== userId),
        ...[...new Set(keys)].map((role) => ({ userId, role })),
      ];
      if (userId === db.me.user.id) refreshMe(id);
      audited(db, "member.set", "project", id, {
        user_id: userId,
        added: keys.filter((k) => !current.includes(k)),
        removed: current.filter((k) => !keys.includes(k)),
        ...(keys.length === 1 ? { role: keys[0] } : {}),
      });
      return HttpResponse.json(members(id).find((x) => x.user.id === userId));
    }),
    http.delete(`${base}/projects/:id/members/:userId`, async ({ params }) => {
      await wait();
      const id = String(params.id);
      const userId = String(params.userId);
      const list = st().members[id] ?? [];
      const current = rolesOfUser(id, userId);
      if (current.length === 0 && viaGroups(id, userId).length > 0)
        return problem(409, "Conflict", undefined, "They are a member through a group only: change the group mapping.");
      if (current.length === 0) return problem(404, "Not found");
      const refused = refusal(id, current, []);
      if (refused) return refused;
      if (current.some(manages) && membersManagers(id, { user: userId }) === 0) return noManager(id);
      st().members[id] = list.filter((x) => x.userId !== userId);
      if (userId === db.me.user.id) refreshMe(id);
      audited(db, "member.remove", "project", id, { user_id: userId, removed: current });
      return new HttpResponse(null, { status: 204 });
    }),
    http.get(`${base}/admin/group-mappings`, async () => {
      await wait();
      return HttpResponse.json({ items: st().groups });
    }),
    http.post(`${base}/admin/group-mappings`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["GroupMappingWrite"];
      if (!w.oidc_group.trim()) return invalid([err("/oidc_group", "Name the OIDC group.")]);
      const r = roleOf(db, w.role);
      if (!r) return invalid([err("/role", "There is no such role.")]);
      if (r.key === "user")
        return invalid([err("/role", "Everyone signed in holds User: map the group to another role.")]);
      if (!assignable(r, Boolean(w.project_id)))
        return invalid([
          err(
            "/role",
            w.project_id
              ? `In a project: ${r.name} is given for all of rvav only.`
              : `Without a project: ${r.name} is given in a project only.`,
          ),
        ]);
      const p = w.project_id ? project(w.project_id) : undefined;
      const m: S["GroupMapping"] = {
        id: next("gmp"),
        oidc_group: w.oidc_group.trim(),
        project: p ? { id: p.id, code: p.code, name: p.name } : null,
        role: r.key,
        role_name: r.name,
        users: db.users.filter((u) => (userGroups[u.id] ?? []).includes(w.oidc_group.trim())).length,
      };
      st().groups.push(m);
      audited(db, "group_mapping.create", "group_mapping", m.id, { group: m.oidc_group, role: m.role });
      return HttpResponse.json(m, { status: 201 });
    }),
    http.delete(`${base}/admin/group-mappings/:id`, async ({ params }) => {
      await wait();
      const groups = st().groups;
      const i = groups.findIndex((g) => g.id === params.id);
      if (i < 0) return problem(404, "Not found");
      const g = groups[i]!;
      // As the server's RvavHolders: rvav's own roles ("Set by rvav") count too.
      const rvavHolders =
        groups.filter((x) => x !== g && !x.project && managesAccess(x.role)).length +
        st().roleAssignments.filter((a) => managesAccess(a.role.key)).length;
      if (!g.project && managesAccess(g.role) && rvavHolders === 0)
        return problem(
          409,
          "Conflict",
          undefined,
          "Nobody would be an administrator any more: map another group to admin first.",
        );
      if (g.project && manages(g.role) && membersManagers(g.project.id, { mapping: g.id }) === 0)
        return noManager(g.project.id);
      groups.splice(i, 1);
      audited(db, "group_mapping.delete", "group_mapping", String(params.id), { group: g.oidc_group, role: g.role });
      return new HttpResponse(null, { status: 204 });
    }),
    ...rolesHandlers(db, base, wait, {
      given: (key) => [
        ...Object.entries(st().members).flatMap(([projectId, list]) =>
          list.filter((m) => m.role === key).map(() => projectId),
        ),
        ...st()
          .groups.filter((g) => g.role === key && g.project)
          .map((g) => g.project!.id),
      ],
      holders: (key) =>
        Object.values(st().members).flatMap((l) => l.filter((m) => m.role === key)).length +
        st().groups.filter((g) => g.role === key).length +
        st().roleAssignments.filter((a) => a.role.key === key).length,
      policies: (key) => {
        const levels = (ls: S["ApprovalLevel"][] | undefined) => (ls ?? []).some((l) => l.roles?.includes(key));
        return [
          ...st().day2.filter((p) => (p.roles ?? []).includes(key) || levels(p.levels)),
          ...st().approvals.filter((p) => levels(p.levels)),
        ].map((p) => p.name);
      },
      assignments: () => st().roleAssignments,
      rvavMappings: () =>
        st()
          .groups.filter((g) => !g.project)
          .map((g) => g.role),
      membersManagers: (projectId, exceptRole) => membersManagers(projectId, { role: exceptRole }),
      projectName: (id) => project(id)?.name ?? id,
    }),
    http.get(`${base}/admin/environments`, async () => {
      await wait();
      return HttpResponse.json({
        items: db.environments.map((e) => ({
          ...e,
          ...(st().envs[e.key] ?? { snapshot_max_count: 3, snapshot_max_age_days: 7, tag_categories: [] }),
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
        tag_categories: e.tag_categories ?? [],
      };
      audited(db, "environment.create", "environment", e.key);
      return HttpResponse.json({ ...e, ...st().envs[e.key], projects: 0, deployments: 0 }, { status: 201 });
    }),
    http.patch(`${base}/admin/environments/:key`, async ({ params, request }) => {
      await wait();
      const e = db.environments.find((x) => x.key === params.key);
      if (!e) return problem(404, "Not found");
      const u = (await request.json()) as S["AdminEnvironmentUpdate"];
      if (u.name !== undefined) e.name = u.name;
      if (u.production !== undefined) e.production = u.production;
      if (u.order !== undefined) e.order = u.order;
      const x = (st().envs[e.key] ??= { snapshot_max_count: 3, snapshot_max_age_days: 7, tag_categories: [] });
      if (u.snapshot_max_count !== undefined) x.snapshot_max_count = u.snapshot_max_count;
      if (u.snapshot_max_age_days !== undefined) x.snapshot_max_age_days = u.snapshot_max_age_days || null;
      if (u.tag_categories !== undefined) x.tag_categories = [...new Set(u.tag_categories)].sort();
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
        if (w.datastores.length === 0 && !w.datastore_tags?.length && !w.datastore_cluster)
          errors.push(err("/datastores", "Name datastores, tags they carry, or a datastore cluster."));
        if (w.networks.length === 0) errors.push(err("/networks", "Name the networks VMs may connect to."));
        if (!w.folder.startsWith("/"))
          errors.push(err("/folder", "A folder's path, for example /GVA/vm/rvav/web/dev."));
        const known = [String(st().integrations.vcenter!.settings.name), ...st().vcenters.map((v) => v.name)];
        if (w.vcenter && !known.includes(w.vcenter))
          errors.push(err("/vcenter", `rvav manages no vCenter ${w.vcenter}.`));
        (w.cluster_tags ?? []).forEach((t, i) => {
          if (!/^[^/]+\/[^/]+$/.test(t)) errors.push(err(`/cluster_tags/${i}`, `"${t}" is not category/tag.`));
        });
        // NSX segments made for deployments (ADR-0028), against the fake NSX's Tier-1s.
        if (w.segments) {
          if (!["t1-gva-dev", "t1-gva-prd", "t1-zrh-prd"].includes(w.segments.tier1))
            errors.push(err("/segments/tier1", `NSX has no Tier-1 ${w.segments.tier1}.`));
          if (w.segments.prefix_length < 16 || w.segments.prefix_length > 29)
            errors.push(err("/segments/prefix_length", "From /16 to /29."));
        }
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
          storage_policy: w.storage_policy ?? "",
          vcenter: w.vcenter ?? "",
          resource_pool: (w.resource_pool ?? "").replace(/^\/+|\/+$/g, ""),
          hosts: w.hosts ?? [],
          datastore_cluster: w.datastore_cluster ?? "",
          strategy: w.strategy ?? "most_free_memory",
          ...(w.segments ? { segments: w.segments } : {}),
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
            tags: tags[n.name] ?? [],
            ...(m ? { subnet: m.subnet, dns_zone: m.dns_zone, source: m.source } : {}),
          });
          if (!m) out.problems.push(`Network ${n.name} has no subnet: map it by hand or in EfficientIP.`);
        }
      }
      if (out.clusters.length === 0) out.problems.push("No cluster in vCenter matches the rules.");
      return HttpResponse.json(out);
    }),
    http.get(`${base}/admin/inventory`, async () => {
      await wait();
      const out: S["VsphereInventory"] = {
        clusters: clusters.map((c) => ({
          vcenter: VCENTER,
          name: c.name,
          hosts: c.hosts,
          memory_free_gb: Math.round(c.hosts * 512 * (c.memFreePct / 100)),
          tags: tags[c.name] ?? [],
        })),
        datastores: [
          ...Object.entries(datastores).flatMap(([cluster, list]) =>
            list.map((d) => ({
              vcenter: VCENTER,
              name: d.name,
              free_gb: Math.round(d.freeTb * 1024),
              capacity_gb: Math.round(d.freeTb * 1024 * 2.5),
              clusters: [cluster],
              tags: tags[d.name] ?? [],
            })),
          ),
          ...datastoreClusters.flatMap((p) =>
            p.datastores.map((name) => ({
              vcenter: VCENTER,
              name,
              free_gb: Math.round((p.freeTb * 1024) / p.datastores.length),
              capacity_gb: Math.round((p.freeTb * 1024 * 2) / p.datastores.length),
              clusters: [p.cluster],
              tags: tags[name] ?? [],
            })),
          ),
        ],
        hosts: clusters.flatMap((c) =>
          hostsOf(c).map((name, i) => ({
            vcenter: VCENTER,
            name,
            cluster: c.name,
            state: i === 0 && c.env === "tst" ? ("maintenance" as const) : ("connected" as const),
            memory_free_gb: Math.round(512 * (c.memFreePct / 100)),
            memory_total_gb: 512,
            tags: tags[name] ?? [],
          })),
        ),
        resource_pools: resourcePools.map((p) => ({
          vcenter: VCENTER,
          cluster: p.cluster,
          path: p.path,
          cpu_limit_mhz: null,
          memory_limit_gb: p.memoryLimitGb ?? null,
          tags: tags[`${p.cluster}/${p.path}`] ?? [],
        })),
        tag_categories: [
          { vcenter: VCENTER, name: "app", single: false, tags: ["billing", "shop"] },
          { vcenter: VCENTER, name: "backup", single: false, tags: ["daily", "weekly"] },
          { vcenter: VCENTER, name: "tier", single: true, tags: ["gold", "silver"] },
        ],
        datastore_clusters: datastoreClusters.map((p) => ({
          vcenter: VCENTER,
          name: p.name,
          datastores: p.datastores,
          free_gb: Math.round(p.freeTb * 1024),
          capacity_gb: Math.round(p.freeTb * 1024 * 2),
          sdrs: p.sdrs,
          tags: tags[p.name] ?? [],
        })),
        networks: networks.map((n) => ({
          vcenter: VCENTER,
          name: n.name,
          clusters: clusters.filter((c) => c.env === n.env).map((c) => c.name),
          tags: tags[n.name] ?? [],
        })),
      };
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
      const subnet6 = w.subnet6?.trim() ?? "";
      const gateway6 = w.gateway6?.trim() ?? "";
      if ((subnet6 || gateway6) && !/^[0-9a-f:]+\/\d+$/i.test(subnet6))
        return invalid([err("/subnet6", "An IPv6 subnet such as 2001:db8:310::/64.")]);
      if (subnet6 && !gateway6.includes(":")) return invalid([err("/gateway6", "An IPv6 address.")]);
      const m: S["NetworkMapping"] = {
        network: String(params.network),
        subnet: w.subnet,
        gateway: w.gateway,
        dns_zone: w.dns_zone,
        dns_servers: w.dns_servers ?? [],
        source: "manual",
        ...(subnet6 ? { subnet6, gateway6 } : {}),
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
          const w = (await request.json()) as S["VmTemplateWrite"];
          if (!w.label?.trim()) return invalid([err("/label", "Give it a label.")]);
          if (kind === "template") {
            const bad = templateErrors(w);
            if (bad.length) return invalid(bad);
          }
          if (list().some((x) => x.key === w.key))
            return problem(409, "Conflict", undefined, `The key ${w.key} is taken.`);
          const row = {
            offers: [],
            ...(kind === "template" ? { customization: "", description: "" } : {}),
            ...w,
            ...(kind === "template" ? storedTemplate(w) : {}),
          };
          list().push(row);
          audited(db, `${kind}.create`, kind, w.key);
          return HttpResponse.json(row, { status: 201 });
        }),
        http.put(`${base}/admin/${path}/:key`, async ({ params, request }) => {
          await wait();
          const w = (await request.json()) as S["VmTemplateWrite"];
          const i = list().findIndex((x) => x.key === params.key);
          if (i < 0) return problem(404, "Not found");
          if (kind === "template") {
            const bad = templateErrors(w);
            if (bad.length) return invalid(bad);
          }
          list()[i] = {
            ...list()[i],
            ...w,
            ...(kind === "template" ? storedTemplate(w) : {}),
            key: String(params.key),
          };
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
        const counterErrors = counterProblems(w.start ?? 1, w.increment ?? 1);
        if (counterErrors.length) return invalid(counterErrors);
        const pol: S["NamingPolicy"] = {
          id: method === "put" ? String(params.id) : next("nmp"),
          name: w.name,
          template: w.template,
          counter: w.counter ?? "prefix",
          start: w.start ?? 1,
          increment: w.increment ?? 1,
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
      const start = q.start ?? 1;
      const step = q.increment ?? 1;
      const counterErrors = counterProblems(start, step);
      if (counterErrors.length) return invalid(counterErrors);
      // The first number is taken in vCenter, as the demo's first VM is.
      const names = Array.from(
        { length: q.count ?? 3 },
        (_, i) => `${prefix}${String(start + (i + 1) * step).padStart(width, "0")}`,
      );
      return HttpResponse.json({
        names,
        skipped: [{ name: `${prefix}${String(start).padStart(width, "0")}`, taken_in: "vcenter" }],
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
    http.put(`${base}/projects/:id/deployment-sharing`, async ({ params, request }) => {
      await wait();
      const p = project(String(params.id));
      if (!p) return problem(404, "Not found");
      if (!holdsIn(db, p.id, "project.members.manage")) return forbidden("you may not manage its members");
      const { deployment_sharing: sharing } = (await request.json()) as S["DeploymentSharingWrite"];
      if (sharing !== "all_members" && sharing !== "owner_only")
        return invalid([err("/deployment_sharing", "all_members or owner_only.")]);
      p.deployment_sharing = sharing;
      audited(db, "project.sharing.set", "project", p.id);
      return HttpResponse.json({
        ...p,
        my_permissions: db.me.projects.find((x) => x.project.id === p.id)?.permissions ?? db.me.permissions,
      });
    }),
    http.get(`${base}/admin/lease-policies`, async () => {
      await wait();
      if (!policies.reads()) return forbidden("you may not read policies");
      return HttpResponse.json({ items: st().leases.filter((l) => policies.shows(l.project_id)) });
    }),
    http.put(`${base}/admin/lease-policies`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["LeasePolicy"];
      const l = { ...w, enforcement: w.enforcement ?? "hard" };
      if (!policies.writes(l.project_id)) return refusedPolicy(l.project_id);
      const env = st().leases.find((x) => x.environment === l.environment && !x.project_id);
      const name = db.environments.find((e) => e.key === l.environment)?.name ?? l.environment;
      const looser: S["FieldError"][] = [];
      if (!policies.global() && env) {
        if (env.max_days != null && (l.max_days == null || l.max_days > env.max_days))
          looser.push(err("/max_days", `${name} allows ${env.max_days} days at most.`));
        if (l.enforcement === "soft" && env.enforcement !== "soft")
          looser.push(err("/enforcement", `${name}'s lease is hard: its overrides are too.`));
      }
      if (looser.length) return invalid(looser);
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
      if (!policies.writes(list[i]!.project_id)) return refusedPolicy(list[i]!.project_id);
      list.splice(i, 1);
      audited(db, "policy.lease.delete", "lease_policy", url.searchParams.get("environment") ?? "");
      return new HttpResponse(null, { status: 204 });
    }),
    ...(
      [
        ["approval-policies", "approval", "pol"],
        ["day2-policies", "day2", "d2p"],
      ] as const
    ).flatMap(([path, kind, prefix]) => {
      const list = () =>
        (kind === "approval" ? st().approvals : st().day2) as {
          id: string;
          priority: number;
          project_id?: string | null;
        }[];
      const save = (id: string, w: Record<string, unknown>, status: number) => {
        const was = list().find((x) => x.id === id);
        for (const pid of [was?.project_id, w.project_id as string | null | undefined])
          if (status === 200 || pid === w.project_id) if (!policies.writes(pid)) return refusedPolicy(pid);
        const errors: S["FieldError"][] = [];
        if (kind === "day2" && w.enforcement === "soft" && w.effect === "allow")
          errors.push(err("/enforcement", "Only a deny or an approval policy can be soft: it warns instead."));
        if (typeof w.name !== "string" || !w.name.trim()) errors.push(err("/name", "Give the policy a name."));
        checkCel(w.condition as string, "/condition", errors);
        if (kind === "approval") checkCel(w.auto_approve as string, "/auto_approve", errors);
        const levels = (w.levels ?? []) as S["ApprovalLevel"][];
        if ((kind === "approval" || w.effect === "approval") && levels.length === 0)
          errors.push(err("/levels", "Give at least one level of approvers."));
        levels.forEach((l, i) => {
          if (!l.group && !l.approvers?.length && !l.roles?.length && !l.manager)
            errors.push(
              err(`/levels/${i}`, "Give an OIDC group, approvers, project roles or the requester's manager."),
            );
          l.roles?.forEach((k, j) => {
            const r = roleOf(db, k);
            if (!r || !assignable(r, true))
              errors.push(err(`/levels/${i}/roles/${j}`, `There is no role ${k} given in projects.`));
          });
          if (l.timeout && !/^\d+(h|m|s)$/.test(l.timeout))
            errors.push(err(`/levels/${i}/timeout`, "A duration such as 48h."));
          if (l.on_timeout && !l.timeout)
            errors.push(err(`/levels/${i}/on_timeout`, "What happens on time-out needs a timeout."));
          if (l.on_timeout === "escalate" && !l.escalate_to?.group && !l.escalate_to?.approvers?.length)
            errors.push(err(`/levels/${i}/escalate_to`, "Say whom to escalate to: an OIDC group or approvers."));
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
          ...(kind === "day2" ? { enforcement: w.enforcement ?? "hard" } : {}),
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
          if (!policies.reads()) return forbidden("you may not read policies");
          const levelRoles = [...builtinRoles, ...customRoles(db)].filter((r) => assignable(r, true));
          return HttpResponse.json({
            items: list().filter((x) => policies.shows(x.project_id)),
            level_roles: levelRoles.map((r) => ({ key: r.key, name: r.name })),
          });
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
          if (!policies.writes(list()[i]!.project_id)) return refusedPolicy(list()[i]!.project_id);
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
      const here = (x: { project_id?: string | null }) => !x.project_id || x.project_id === p.id;
      const approvals = st()
        .approvals.filter(here)
        .map((a) => {
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
              here(d) &&
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

    // ── vCenters (ADR-0024) ──
    http.get(`${base}/admin/vcenters`, async () => {
      await wait();
      const primary = st().integrations.vcenter!;
      return HttpResponse.json({
        items: [
          {
            name: String(primary.settings.name),
            url: String(primary.settings.url),
            username: String(primary.settings.username),
            insecure: primary.settings.insecure === true,
            password_set: primary.secret,
            primary: true,
            inventory: { clusters: 4, vms: db.deployments.length, synced_at: now() },
          },
          ...st().vcenters,
        ],
      });
    }),
    ...(["post", "put"] as const).map((method) =>
      http[method](`${base}/admin/vcenters${method === "put" ? "/:name" : ""}`, async ({ params, request }) => {
        await wait();
        const w = (await request.json()) as S["VCenterWrite"];
        const name = method === "put" ? String(params.name) : w.name;
        const current = st().vcenters.find((v) => v.name === name);
        if (method === "put" && !current) return problem(404, "Not found");
        // Checked first, as the server does; then a name taken.
        const errors: S["FieldError"][] = [];
        if (!/^[a-z0-9][a-z0-9.-]{1,62}$/.test(name))
          errors.push(err("/name", "Its host name in lower case, for example vc02.corp.example."));
        else if (name === st().integrations.vcenter!.settings.name)
          errors.push(err("/name", `${name} is the vCenter integration's: change it in Integrations.`));
        if (!/^https:\/\/[^/]+/.test(w.url))
          errors.push(err("/url", "Its SDK address over HTTPS, for example https://vc02.corp.example/sdk."));
        if (!w.username.trim()) errors.push(err("/username", "Give the account rvav signs in with."));
        if (!current && !w.password) errors.push(err("/password", "Give the account's password."));
        const pin = normalFingerprint(w.cert_sha256 ?? "");
        if ((w.cert_sha256 ?? "").trim() && !pin)
          errors.push(err("/cert_sha256", "A certificate's SHA-256: 32 bytes in hex, with or without colons."));
        if (
          current &&
          !w.password &&
          (w.url !== current.url || (w.insecure && !current.insecure) || (pin && pin !== (current.cert_sha256 ?? "")))
        )
          errors.push(
            err(
              "/password",
              "A new address, a certificate pinned, or the certificate not checked, needs the password again: the saved one only goes where it was given for.",
            ),
          );
        if (errors.length) return invalid(errors);
        if (method === "post" && current) return problem(409, "Conflict", undefined, `rvav already manages ${name}.`);
        // Not synced yet: no inventory until the sync reads it.
        const vc: S["VCenter"] = {
          name,
          url: w.url,
          username: w.username,
          insecure: w.insecure ?? false,
          ...(pin ? { cert_sha256: pin } : {}),
          password_set: true,
          primary: false,
          ...(current?.inventory ? { inventory: current.inventory } : {}),
        };
        st().vcenters = [...st().vcenters.filter((v) => v.name !== name), vc];
        audited(db, `vcenter.${method === "put" ? "update" : "create"}`, "vcenter", name);
        return HttpResponse.json(vc, { status: method === "post" ? 201 : 200 });
      }),
    ),
    http.delete(`${base}/admin/vcenters/:name`, async ({ params }) => {
      await wait();
      const i = st().vcenters.findIndex((v) => v.name === params.name);
      if (i < 0) return problem(404, "Not found");
      st().vcenters.splice(i, 1);
      audited(db, "vcenter.delete", "vcenter", String(params.name));
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(`${base}/admin/vcenters/:name/test`, async ({ request }) => {
      await wait();
      const raw = await request.text();
      const w = raw ? (JSON.parse(raw) as S["VCenterWrite"]) : undefined;
      const ok = !w || /^https:\/\/[^/]+/.test(w.url);
      const tls = w
        ? certificateCheck(text(st().integrations.trust!.settings.ca_bundle), w.url, { ...w }, true)
        : { goOn: true };
      const cert = tls.certificate ? { certificate: tls.certificate } : {};
      if (ok && !tls.goOn) return HttpResponse.json({ ok: false, checks: [tls.check!], ...cert });
      return HttpResponse.json({
        ok,
        checks: ok
          ? [
              ...(tls.check ? [tls.check] : []),
              { name: "Sign in", ok: true, message: `Signed in to ${w?.url ?? "it"} as ${w?.username ?? "rvav"}.` },
              { name: "Inventory", ok: true, message: "2 clusters, 2 datastores, 3 networks and 1 templates." },
            ]
          : [{ name: "Settings", ok: false, message: "Its SDK address over HTTPS." }],
        ...cert,
      });
    }),

    // ── The setup checklist (U1.5, ADR-0030) ──
    http.get(`${base}/admin/setup`, async () => {
      await wait();
      return HttpResponse.json(setupOf(db));
    }),
    http.put(`${base}/admin/setup`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["SetupWrite"];
      st().setupHidden = w.hidden;
      audited(db, w.hidden ? "setup.hidden" : "setup.shown", "setup", "checklist");
      return HttpResponse.json(setupOf(db));
    }),

    // ── rvav's processes (ADR-0030) ──
    // The System page (U1.5): as the server's, with an install's
    // settings as config.Install lists them.
    http.get(`${base}/admin/system`, async () => {
      await wait();
      const a = st();
      const connections = Object.entries(a.integrations)
        .filter(([k]) => !LIVE_KINDS.includes(k))
        .map(([, it]) => it.revision ?? 0);
      const processes = processesNow(a);
      const settings = KINDS.flatMap((kind): S["SettingsRevision"][] => {
        const it = a.integrations[kind];
        if (!it?.revision) return [];
        const revision = it.revision;
        return [
          {
            kind,
            revision,
            updated_at: it.updated_at ?? now(),
            updated_by: { id: db.me.user.id, name: db.me.user.name },
            applies: LIVE_KINDS.includes(kind)
              ? "next_use"
              : processes.some((p) => p.revision < revision)
                ? "applying"
                : "applied",
          },
        ];
      }).sort((x, y) => y.revision - x.revision);
      return HttpResponse.json({
        version: "1.4.0",
        schema: 49,
        revision: Math.max(0, ...connections),
        processes,
        settings,
        install: MOCK_INSTALL,
      } satisfies S["System"]);
    }),

    http.get(`${base}/admin/processes`, async () => {
      await wait();
      const a = st();
      const connections = Object.entries(a.integrations)
        .filter(([k]) => !LIVE_KINDS.includes(k))
        .map(([, it]) => it.revision ?? 0);
      return HttpResponse.json({ items: processesNow(a), revision: Math.max(0, ...connections) });
    }),

    // ── Integrations ──
    http.get(`${base}/admin/integrations`, async () => {
      await wait();
      return HttpResponse.json({
        items: KINDS.map((kind) => {
          const it = st().integrations[kind]!;
          return {
            kind,
            configured: configuredOf(kind, it),
            settings: it.settings,
            secret_set: it.secret,
            ...(kind === "slack" ? { signing_secret_set: it.signing === true } : {}),
            locked: kind === "vcenter" && !db.fresh ? ["url"] : [],
            ...applyingOf(kind, it.revision ?? 0),
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
      if (kind === "oidc") {
        const bad = providerErrors(w.settings);
        if (bad.length) return invalid(bad);
        const it = st().integrations.oidc!;
        it.settings = { ...it.settings, ...w.settings };
        it.updated_at = now();
        it.revision = saved(kind);
        audited(db, "settings.integration.oidc", "integration", "oidc");
        return HttpResponse.json({
          kind,
          configured: Boolean(it.settings.issuer),
          settings: it.settings,
          secret_set: false,
          locked: [],
          ...applyingOf(kind, it.revision),
          updated_at: it.updated_at,
        });
      }
      const url = w.settings.url;
      if (kind === "git" && typeof url === "string" && /^[a-z+]+:\/\/[^/@]*:[^/@]*@/.test(url))
        return invalid([
          err("/settings/url", "The URL holds a password or token: give it as the token, not in the URL."),
        ]);
      if (
        typeof url === "string" &&
        url &&
        !(kind === "git" ? /^(https?:\/\/|ssh:\/\/|[\w.-]+@[\w.-]+:)/ : /^https?:\/\//).test(url)
      )
        return invalid([
          err(
            "/settings/url",
            kind === "git" ? "An https:// or ssh:// URL, or git@host:path." : "An http or https URL.",
          ),
        ]);
      const every = w.settings.sync_interval;
      if (typeof every === "string" && every && !intervalOK(every))
        return invalid([err("/settings/sync_interval", "A duration from 30s to 24h, for example 5m or 1h30m.")]);
      const untrusted = trustErrors(kind, w.settings);
      if (untrusted.length) return invalid(untrusted);
      if (typeof w.settings.cert_sha256 === "string")
        w.settings.cert_sha256 = normalFingerprint(w.settings.cert_sha256);
      const it = st().integrations[kind]!;
      // RVAV_VCENTER_URL in the mock's environment, not a new install's.
      const locked = kind === "vcenter" && !db.fresh ? ["url"] : [];
      const given = Object.fromEntries(Object.entries(w.settings).filter(([k]) => !locked.includes(k)));
      const moved = it.secret ? secretMoves(it.settings, given, w.secret) : null;
      if (moved) return moved;
      it.settings = {
        ...it.settings,
        ...Object.fromEntries(Object.entries(w.settings).filter(([k]) => !locked.includes(k))),
      };
      if (w.secret) it.secret = true;
      if (w.signing_secret) it.signing = true;
      it.updated_at = now();
      it.revision = saved(kind);
      audited(db, `settings.integration.${kind}`, "integration", kind);
      return HttpResponse.json({
        kind,
        configured: configuredOf(kind, it),
        settings: it.settings,
        secret_set: it.secret,
        ...(kind === "slack" ? { signing_secret_set: it.signing === true } : {}),
        locked,
        ...applyingOf(kind, it.revision),
        updated_at: it.updated_at,
      });
    }),
    http.post(`${base}/admin/integrations/:kind/test`, async ({ params, request }) => {
      await wait();
      const kind = String(params.kind);
      const raw = await request.text();
      const body = raw ? (JSON.parse(raw) as S["IntegrationWrite"]) : undefined;
      const saved = st().integrations[kind]!;
      if (kind === "oidc") {
        const bad = providerErrors(body?.settings ?? {});
        if (bad.length) return invalid(bad);
        return HttpResponse.json(
          providerTest({ ...saved.settings, ...body?.settings }, text(st().integrations.trust!.settings.ca_bundle)),
        );
      }
      const editable = Object.fromEntries(
        Object.entries(body?.settings ?? {}).filter(([k]) => !(kind === "vcenter" && k === "url")),
      );
      const untrusted = trustErrors(kind, editable);
      if (untrusted.length) return invalid(untrusted);
      const moved = body && saved.secret ? secretMoves(saved.settings, editable, body.secret) : null;
      if (moved) return moved;
      const ps = { ...saved.settings, ...editable };
      const bundle = text((kind === "trust" ? ps : st().integrations.trust!.settings).ca_bundle);
      if (kind === "trust") {
        const { subjects } = pemBlocks(bundle);
        return HttpResponse.json({
          ok: true,
          checks: subjects.length
            ? subjects.map((name) => ({
                name,
                ok: true,
                message: `A CA, until 15 January 2031; SHA-256 ${fingerprint(name)}.`,
              }))
            : [{ name: "CA bundle", ok: true, message: "Empty: only the system's CAs are trusted." }],
        });
      }
      const tls = ["vcenter", "awx", "efficientip", "nsx", "vra", "git"].includes(kind)
        ? certificateCheck(bundle, text(ps.url), ps, kind !== "git")
        : { goOn: true };
      if (!tls.goOn)
        return HttpResponse.json({
          ok: false,
          checks: [tls.check!],
          ...(tls.certificate ? { certificate: tls.certificate } : {}),
        });
      const list = (v: unknown) => (Array.isArray(v) && v.length ? v.join(", ") : "no host");
      if (kind === "hosts")
        return HttpResponse.json({
          ok: true,
          checks: [
            {
              name: "Hosts",
              ok: true,
              message: `REST value sources and calls may reach ${list(ps.rest_source_hosts)}; webhooks ${list(ps.webhook_hosts)}.`,
            },
          ],
        });
      if (kind === "console") {
        const sdk = Array.isArray(ps.sdk_urls) ? ps.sdk_urls.length : 0;
        const account = typeof ps.username === "string" ? ps.username : "";
        return HttpResponse.json({
          ok: account !== "",
          checks: [
            account
              ? { name: "Sign in", ok: true, message: `Signed in to vc01.corp.example as ${account}.` }
              : {
                  name: "Console account",
                  ok: false,
                  message: "none: VMRC asks each user to sign in to vCenter",
                },
            {
              name: "Console in the browser",
              ok: true,
              message: sdk
                ? `${sdk} WebMKS SDK scripts, loaded by the browser.`
                : "No WebMKS SDK: consoles open in VMRC only.",
            },
          ],
        });
      }
      const given = editable.url ?? saved.settings.url;
      const url = typeof given === "string" ? given : "";
      const reachable = !url.includes("unreachable");
      const checks: S["IntegrationCheck"][] = [
        ...(tls.check ? [tls.check] : []),
        {
          name: "Sign in",
          ok: reachable,
          message: reachable ? `Signed in to ${url}.` : `dial tcp: lookup ${url}: no such host`,
        },
      ];
      if (reachable && kind === "git")
        checks.push({ name: "Branch main", ok: true, message: "At 3f9c2a1b7d40; items are files in catalog/." });
      if (reachable && kind === "vcenter") {
        checks.push({ name: "Inventory", ok: true, message: "4 clusters, 6 datastores, 4 networks and 3 templates." });
        checks.push({ name: "Tagging service", ok: true, message: "Tags can be read, for placement by tag." });
      }
      return HttpResponse.json({
        ok: checks.every((c) => c.ok),
        checks,
        ...(tls.certificate ? { certificate: tls.certificate } : {}),
      });
    }),
    // The token AWX's inventory source reads rvav with (U1.4): made for
    // rvav's inventory user and put in AWX, the earlier one revoked.
    http.get(`${base}/admin/integrations/awx/inventory-token`, async () => {
      await wait();
      const a = st();
      return HttpResponse.json({
        locked: false,
        source_set: Number(a.integrations.awx!.settings.inventory_source_id) > 0,
        public_url: "https://rvav.corp.example",
        ...(a.inventory.token ? { token: a.inventory.token } : {}),
      } satisfies S["InventoryToken"]);
    }),
    http.post(`${base}/admin/integrations/awx/inventory-token`, async ({ request }) => {
      await wait();
      const a = st();
      const raw = await request.text();
      const w = (raw ? JSON.parse(raw) : {}) as S["InventoryTokenWrite"];
      const given = w.url?.trim() ?? "";
      if (given && !/^https?:\/\/[^/\s]+/.test(given))
        return invalid([err("/url", "rvav's address as AWX reaches it, such as https://rvav.corp.example.")]);
      if (!(Number(a.integrations.awx!.settings.inventory_source_id) > 0))
        return problem(
          409,
          "Conflict",
          undefined,
          "Set AWX's inventory source ID first: the source that reads rvav's inventory.",
        );
      const attached = !a.inventory.url;
      a.inventory.url = given || a.inventory.url || "https://rvav.corp.example";
      const revoked = a.inventory.token ? 1 : 0;
      const id = `tok_inv${++a.seq}`;
      a.inventory.token = {
        id,
        name: "AWX inventory",
        scope: "inventory",
        user: { id: "usr_rvav-inventory", name: "AWX inventory (rvav)" },
        created_at: now(),
        expires_at: new Date(Date.now() + 365 * 86_400_000).toISOString(),
      };
      audited(db, "awx.inventory_token", "api_token", id, { url: a.inventory.url, revoked });
      return HttpResponse.json(
        {
          token: a.inventory.token,
          credential: "rvav API",
          url: a.inventory.url,
          attached,
          revoked,
          update: `AWX is syncing the inventory source with it (update ${a.seq}).`,
        } satisfies S["InventoryTokenMade"],
        { status: 201 },
      );
    }),
    // A trial sign-in's token (ADR-0030): the mock's trial window sends
    // "mock-trial-token", for a user in rvav-web and rvav-catalog-admins.
    http.post(`${base}/admin/integrations/oidc/trial`, async ({ request }) => {
      await wait();
      const w = (await request.json()) as S["OidcTrialRequest"];
      if (!w.access_token) return invalid([err("/access_token", "The trial sign-in's access token.")]);
      const bad = providerErrors(w.settings);
      if (bad.length) return invalid(bad);
      const ps = { ...st().integrations.oidc!.settings, ...w.settings };
      const claim = typeof ps.groups_claim === "string" && ps.groups_claim ? ps.groups_claim : "groups";
      const managerClaim = typeof ps.manager_claim === "string" && ps.manager_claim ? ps.manager_claim : "manager";
      const groups = ["rvav-web", "rvav-catalog-admins"];
      const who = { sub: "a3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d", name: "Ada Lovelace", email: "ada@corp.example" };
      const claims: Record<string, unknown> = {
        iss: ps.issuer,
        aud: "rvav",
        ...who,
        groups,
        manager: "grace.hopper@corp.example",
        exp: Math.floor(Date.now() / 1000) + 300,
      };
      const accepted = ps.client_id === "rvav";
      const mine = st().groups.filter((g) => claim === "groups" && groups.includes(g.oidc_group));
      return HttpResponse.json({
        accepted,
        ...(accepted
          ? claim in claims
            ? {}
            : { message: `The token has no ${claim} claim, so no group mapping gives a role.` }
          : {
              message: `oidc: expected audience "${String(ps.client_id)}" got ["rvav"]. rvav expects the client ID as the audience: set the provider to put it in aud (Keycloak: an audience mapper).`,
            }),
        subject: who.sub,
        name: who.name,
        email: who.email,
        claims,
        groups_claim: claim,
        groups: claim in claims ? groups : [],
        manager_claim: managerClaim,
        ...(managerClaim in claims ? { manager: String(claims[managerClaim]) } : {}),
        global_roles: accepted ? mine.filter((g) => !g.project).map((g) => g.role) : [],
        project_roles: accepted
          ? mine.filter((g) => g.project).map((g) => ({ project: g.project!, role: g.role }))
          : [],
      } satisfies S["OidcTrial"]);
    }),
    http.get(`${base}/auth/options`, async () => {
      await wait();
      const ps = st().integrations.oidc!.settings;
      return HttpResponse.json(
        ps.issuer
          ? {
              provider: {
                authority: ps.issuer,
                client_id: ps.client_id || "rvav",
                scope: ps.scopes || "openid profile email",
              },
            }
          : {},
      );
    }),
  ];
}

// The provider's settings as the server checks them (config.CheckSettings).
function providerErrors(given: Record<string, unknown>): S["FieldError"][] {
  const out: S["FieldError"][] = [];
  for (const f of ["issuer", "discovery_url"]) {
    const v = given[f];
    if (typeof v === "string" && v && !/^https?:\/\//.test(v)) out.push(err(`/settings/${f}`, "An http or https URL."));
  }
  const scopes = typeof given.scopes === "string" ? given.scopes.trim() : "";
  if (scopes && !scopes.split(/\s+/).includes("openid"))
    out.push(err("/settings/scopes", "The scopes must include openid."));
  const claim = given.groups_claim;
  if (typeof claim === "string" && /[\s"']/.test(claim))
    out.push(err("/settings/groups_claim", "A claim name, without spaces or quotes."));
  const manager = given.manager_claim;
  if (typeof manager === "string" && /[\s"']/.test(manager))
    out.push(err("/settings/manager_claim", "A claim name, without spaces or quotes."));
  return out;
}

// A provider's test: unreachable when its issuer says so.
function providerTest(ps: Record<string, unknown>, bundle: string): S["IntegrationTest"] {
  const issuer = typeof ps.issuer === "string" ? ps.issuer : "";
  if (!issuer) return { ok: false, checks: [{ name: "Issuer", ok: false, message: "no issuer is set" }] };
  if (!ps.client_id) return { ok: false, checks: [{ name: "Client", ok: false, message: "no client ID is set" }] };
  const discovery = typeof ps.discovery_url === "string" && ps.discovery_url ? ps.discovery_url : issuer;
  const tls = issuer.includes("unreachable") ? { goOn: true } : certificateCheck(bundle, discovery, {}, false);
  const cert = tls.certificate ? { certificate: tls.certificate } : {};
  if (!tls.goOn) return { ok: false, checks: [tls.check!], ...cert };
  if (issuer.includes("unreachable"))
    return {
      ok: false,
      checks: [
        {
          name: "Discovery",
          ok: false,
          message: `Get "${issuer}/.well-known/openid-configuration": dial tcp: lookup: no such host`,
        },
      ],
    };
  return {
    ok: true,
    checks: [
      ...(tls.check ? [tls.check] : []),
      { name: "Discovery", ok: true, message: `${issuer} answered: sign-in at ${issuer}/protocol/openid-connect/auth` },
      { name: "Signing keys", ok: true, message: "1 published: k1 (RS256)" },
    ],
    ...cert,
  };
}
