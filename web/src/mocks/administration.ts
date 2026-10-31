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
import { clusters, datastores, networks } from "./inventory";
import type { Db } from "./seed";
import { localAdminPasswordChanged } from "./signIn";

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
      const gone = t - a.applying.at;
      if (gone >= p.takes) {
        p.revision = a.applying.revision;
        p.started_at = new Date().toISOString();
        p.pid += 1;
      } else if (gone >= p.takes * 0.9) state = "restarting";
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
        key: "rhel9", label: "RHEL 9.4", vcenter_template: "tpl-rhel9.4-2026.10", content_library: "", os_family: "linux", customization: "",
        description: "With the corporate baseline.", offers: [{ project_id: null, environment: null }], in_inventory: true,
      },
      {
        key: "win2025", label: "Windows Server 2025", vcenter_template: "tpl-win2025-std-2026.09", content_library: "", os_family: "windows",
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
      oidc: { settings: { issuer: "https://login.corp.example/realms/rvav", discovery_url: "", client_id: "rvav", scopes: "openid profile email", groups_claim: "groups" }, secret: false },
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
  const a = state(db);
  const connection = (key: S["SetupStep"]["key"], kind: string): S["SetupStep"] => {
    const it = a.integrations[kind]!;
    const url = typeof it.settings.url === "string" ? it.settings.url : "";
    return configuredOf(kind, it) ? { key, done: true, detail: url } : { key, done: false, detail: "Not set." };
  };
  const issuer = typeof a.integrations.oidc!.settings.issuer === "string" ? a.integrations.oidc!.settings.issuer : "";
  const admins = [...new Set(a.groups.filter((g) => g.role === "admin" && !g.project).map((g) => g.oidc_group))].sort();
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
      : { key: "admin_group", done: false, detail: "No group is mapped to admin." },
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
  const all = state(db).leases.filter((l) => l.environment === env);
  return all.find((l) => l.project_id === projectId) ?? all.find((l) => !l.project_id);
}

/** A user's role in a project ("" when none), for other mock handlers. */
export function memberRole(db: Db, projectId: string, userId: string): string {
  return state(db).members[projectId]?.find((m) => m.userId === userId)?.role ?? "";
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
  const st = () => state(db);
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
              err("/vcenter_template", "Name the content library item, and its library in Content library."),
            ]);
          if (list().some((x) => x.key === w.key))
            return problem(409, "Conflict", undefined, `The key ${w.key} is taken.`);
          const row = {
            offers: [],
            ...(kind === "template" ? { customization: "", description: "", content_library: "" } : {}),
            ...w,
            ...(kind === "template" ? { in_inventory: true } : {}),
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
        schema: 48,
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
              message: `REST value sources may call ${list(ps.rest_source_hosts)}; webhooks ${list(ps.webhook_hosts)}.`,
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
      const groups = ["rvav-web", "rvav-catalog-admins"];
      const who = { sub: "a3f1c2d4-5e6f-4a7b-8c9d-0e1f2a3b4c5d", name: "Ada Lovelace", email: "ada@corp.example" };
      const claims: Record<string, unknown> = {
        iss: ps.issuer,
        aud: "rvav",
        ...who,
        groups,
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
