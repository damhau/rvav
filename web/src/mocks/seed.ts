// The mock backend's in-memory store, seeded with realistic example data.
// Every name, address and person here is fictional.
import type {
  CatalogItem,
  DeploymentDetail,
  DnsRecordResource,
  Environment,
  EnvironmentKey,
  IpAddressResource,
  LogLine,
  Me,
  Project,
  QuotaResource,
  RequestForm,
  ServiceRequest,
  Step,
  UserRef,
  VmResource,
} from "@/lib/api/types";
import type { StoredApproval } from "./approvals";
import { factsFor } from "./approvals";
import { formFor } from "./forms";
import { DOMAIN, networks, templates, VCENTER } from "./inventory";

export interface StoredRequest {
  request: ServiceRequest;
  steps: Step[];
  logs: Record<string, LogLine[]>;
}

export interface StoredItem extends CatalogItem {
  projectIds: string[];
}

export interface Db {
  me: Me;
  users: UserRef[];
  environments: Environment[];
  projects: Project[];
  quota: Record<string, { limits: Record<QuotaResource, number | null>; used: Record<QuotaResource, number> }>;
  categories: { key: string; name: string }[];
  items: StoredItem[];
  favourites: Set<string>;
  forms: Record<string, RequestForm>;
  requests: StoredRequest[];
  approvals: StoredApproval[];
  deployments: DeploymentDetail[];
  idempotency: Map<string, { body: string; status: number; response: unknown }>;
  ipNext: Record<string, number>;
  seq: { request: number; id: number; moref: number; event: number };
}

const iso = (ms: number) => new Date(ms).toISOString();

export function createDb(now = Date.now()): Db {
  const ago = (min: number) => iso(now - min * 60_000);
  const inDays = (d: number) => iso(now + d * 86_400_000);
  let idSeq = 1000;
  const id = (prefix: string) => `${prefix}_${(idSeq++).toString(36)}`;
  let moref = 48200;

  const users: UserRef[] = [
    { id: "usr_lmartin", name: "Léa Martin", email: "lea.martin@corp.example" },
    { id: "usr_mkeller", name: "Marc Keller", email: "marc.keller@corp.example" },
    { id: "usr_sbrunner", name: "Sophie Brunner", email: "sophie.brunner@corp.example" },
    { id: "usr_tnguyen", name: "Thomas Nguyen", email: "thomas.nguyen@corp.example" },
    { id: "usr_afavre", name: "Alain Favre", email: "alain.favre@corp.example" },
    { id: "usr_cdubois", name: "Claire Dubois", email: "claire.dubois@corp.example" },
  ];
  const [lea, marc, sophie, thomas, alain, claire] = users as [UserRef, UserRef, UserRef, UserRef, UserRef, UserRef];

  const environments: Environment[] = [
    { key: "dev", name: "Development", production: false, order: 1 },
    { key: "tst", name: "Test", production: false, order: 2 },
    { key: "prd", name: "Production", production: true, order: 3 },
  ];

  const projects: Project[] = [
    {
      id: "prj_web",
      code: "web",
      name: "Web Platform",
      description: "Public web front ends, checkout and search.",
      cost_center: "CC-4120",
      environments: ["dev", "tst", "prd"],
      my_role: "project-admin",
    },
    {
      id: "prj_data",
      code: "data",
      name: "Data Engineering",
      description: "Ledger, reporting and the analytics pipeline.",
      cost_center: "CC-4310",
      environments: ["dev", "prd"],
      my_role: "member",
    },
  ];
  const web = projects[0]!;
  const data = projects[1]!;
  const ref = (p: Project) => ({ id: p.id, code: p.code, name: p.name });

  const me: Me = {
    user: lea,
    title: "Platform engineer",
    // Mock mode shows every page: the mock user is an administrator too.
    global_roles: ["user", "admin"],
    projects: [
      { project: ref(web), role: "project-admin" },
      { project: ref(data), role: "member" },
    ],
  };

  const categories = [
    { key: "compute", name: "Compute" },
    { key: "databases", name: "Databases" },
    { key: "network", name: "Network" },
    { key: "security", name: "Security" },
    { key: "identity", name: "Identity" },
  ];

  const both = [web.id, data.id];
  const item = (
    o: Omit<StoredItem, "id" | "version" | "favourite"> & { key: string; version: string },
  ): StoredItem => ({
    ...o,
    id: `itm_${o.key}`,
    version: {
      id: `ver_${o.key}_${o.version.replace(/\W+/g, "")}`,
      number: o.version,
      published_at: ago(60 * 24 * 20),
    },
    favourite: false,
  });
  const items: StoredItem[] = [
    item({
      key: "rhel9",
      slug: "rhel9-server",
      name: "RHEL 9 server",
      summary: "Red Hat Enterprise Linux 9.4 with the corporate baseline, IdM join and node_exporter.",
      category: "compute",
      type: "vm_blueprint",
      icon: "server",
      version: "3.2",
      typical_duration_seconds: 420,
      approval: "conditional",
      featured: true,
      requests_last_30_days: 64,
      source: { kind: "native" },
      projectIds: both,
    }),
    item({
      key: "ubuntu",
      slug: "ubuntu-server",
      name: "Ubuntu 24.04 server",
      summary: "Ubuntu LTS with cloud-init, the corporate baseline and the monitoring agent.",
      category: "compute",
      type: "vm_blueprint",
      icon: "server",
      version: "2.4",
      typical_duration_seconds: 360,
      approval: "conditional",
      requests_last_30_days: 41,
      source: { kind: "native" },
      projectIds: both,
    }),
    item({
      key: "windows",
      slug: "windows-server",
      name: "Windows Server 2025",
      summary: "Domain-joined Windows Server 2025 Standard with the CIS level 1 GPO set.",
      category: "compute",
      type: "vm_blueprint",
      icon: "monitor",
      version: "1.8",
      typical_duration_seconds: 1080,
      approval: "conditional",
      requests_last_30_days: 17,
      source: { kind: "native" },
      projectIds: both,
    }),
    item({
      key: "pgcluster",
      slug: "postgres-cluster",
      name: "PostgreSQL 16 cluster",
      summary: "Three-node Patroni cluster with etcd, an HAProxy VIP and pgBackRest backups.",
      category: "databases",
      type: "composite",
      icon: "database",
      version: "1.3",
      typical_duration_seconds: 1560,
      approval: "always",
      requests_last_30_days: 5,
      source: { kind: "native" },
      projectIds: both,
    }),
    item({
      key: "dbuser",
      slug: "db-user",
      name: "Create database user",
      summary: "Creates a PostgreSQL role with a generated password stored in Vault.",
      category: "databases",
      type: "awx_job_template",
      icon: "key-round",
      version: "AWX #47",
      typical_duration_seconds: 60,
      approval: "never",
      requests_last_30_days: 88,
      source: { kind: "awx", awx_template_id: 47, awx_template_name: "pg-create-role" },
      projectIds: both,
    }),
    item({
      key: "fwflow",
      slug: "firewall-flow",
      name: "Open firewall flow",
      summary: "Requests a flow on the perimeter and internal firewalls and opens the change ticket.",
      category: "network",
      type: "awx_workflow",
      icon: "shield",
      version: "AWX #12",
      typical_duration_seconds: 240,
      approval: "always",
      requests_last_30_days: 29,
      source: { kind: "awx", awx_template_id: 12, awx_template_name: "fw-flow-request" },
      projectIds: both,
    }),
    item({
      key: "dnsrec",
      slug: "dns-record",
      name: "DNS record",
      summary: "Adds an A, CNAME or TXT record in an EfficientIP zone your project owns.",
      category: "network",
      type: "awx_job_template",
      icon: "globe",
      version: "AWX #31",
      typical_duration_seconds: 60,
      approval: "never",
      requests_last_30_days: 52,
      source: { kind: "awx", awx_template_id: 31, awx_template_name: "ddi-dns-record" },
      projectIds: both,
    }),
    item({
      key: "tlscert",
      slug: "tls-cert",
      name: "Renew TLS certificate",
      summary: "Issues a certificate from the internal CA and deploys it to the hosts you pick.",
      category: "security",
      type: "awx_job_template",
      icon: "lock",
      version: "AWX #22",
      typical_duration_seconds: 180,
      approval: "never",
      requests_last_30_days: 23,
      source: { kind: "awx", awx_template_id: 22, awx_template_name: "pki-renew-deploy" },
      projectIds: both,
    }),
    item({
      key: "idmgroup",
      slug: "idm-group",
      name: "IdM access group",
      summary: "Creates an IdM group with sudo and HBAC rules for a set of hosts.",
      category: "identity",
      type: "awx_job_template",
      icon: "users",
      version: "AWX #9",
      typical_duration_seconds: 120,
      approval: "never",
      requests_last_30_days: 11,
      source: { kind: "awx", awx_template_id: 9, awx_template_name: "idm-hbac-group" },
      projectIds: both,
    }),
  ];
  const forms: Record<string, RequestForm> = {};
  for (const i of items) forms[i.id] = formFor(i.slug, i.id, i.version.id);

  // ── Deployments ───────────────────────────────────────────────────────
  const netFor = (env: EnvironmentKey) => networks.find((n) => n.env === env)!;
  const clusterFor = (env: EnvironmentKey) =>
    env === "prd" ? "GVA-PRD-CL01" : env === "tst" ? "GVA-TST-CL01" : "GVA-DEV-CL01";
  const dsFor = (env: EnvironmentKey) =>
    env === "prd" ? "vsan-gva-prd-cl01" : env === "tst" ? "vsan-gva-tst-cl01" : "vsan-gva-dev-cl01";

  interface VmSpec {
    name: string;
    env: EnvironmentKey;
    ip: string;
    cpu?: number;
    memGb?: number;
    diskGb?: number;
    on?: boolean;
    network?: string;
    template?: { name: string; os: string };
    cpuPct?: number;
    memPct?: number;
    snapshots?: number;
  }
  function vmResources(s: VmSpec): [VmResource, IpAddressResource, DnsRecordResource] {
    moref += 17;
    const net = s.network ?? netFor(s.env).name;
    const subnet = networks.find((n) => n.name === net)!.subnet;
    const tpl = s.template ?? templates.rhel9;
    const on = s.on ?? true;
    const fqdn = `${s.name}.${DOMAIN}`;
    return [
      {
        id: id("res"),
        type: "vsphere.vm",
        name: s.name,
        sync_state: "in_sync",
        vm: {
          moref: `vm-${moref}`,
          instance_uuid: `5029${moref.toString(16)}-1c2d-4e5f-8a9b-0c${String(moref).padStart(10, "0")}`,
          vcenter: VCENTER,
          power_state: on ? "on" : "off",
          guest_state: on ? "running" : "not_running",
          guest_os: tpl.os,
          cpu_count: s.cpu ?? 4,
          memory_mb: (s.memGb ?? 16) * 1024,
          disks: [{ label: "Hard disk 1", capacity_gb: s.diskGb ?? 80, datastore: dsFor(s.env) }],
          nics: [
            {
              network: net,
              mac: `00:50:56:a1:${(moref % 256).toString(16).padStart(2, "0")}:3c`,
              ip_addresses: [s.ip],
            },
          ],
          fqdn,
          cluster: clusterFor(s.env),
          host: `esx-gva-${s.env}-0${(moref % 4) + 1}.${DOMAIN}`,
          folder: `/GVA/vm/rvav/${s.name.split("-")[0]}/${s.env}`,
          template: tpl.name,
          snapshot_count: s.snapshots ?? 0,
          snapshots: Array.from({ length: s.snapshots ?? 0 }, (_, i) => ({
            name: `Before change ${i + 1}`,
            created_at: ago(60 * 24 * (i + 1)),
          })),
          usage: on
            ? { cpu_pct: s.cpuPct ?? 8 + (moref % 40), memory_pct: s.memPct ?? 30 + (moref % 40), sampled_at: ago(0) }
            : { cpu_pct: 0, memory_pct: 0, sampled_at: ago(0) },
        },
      },
      {
        id: id("res"),
        type: "ipam.address",
        name: s.ip,
        sync_state: "in_sync",
        address: s.ip,
        subnet,
        efficientip_id: String(880000 + moref),
      },
      {
        id: id("res"),
        type: "dns.record",
        name: fqdn,
        sync_state: "in_sync",
        fqdn,
        record_type: "A",
        value: s.ip,
        zone: DOMAIN,
      },
    ];
  }

  interface DepSpec {
    name: string;
    item: StoredItem;
    project: Project;
    env: EnvironmentKey;
    owner: UserRef;
    createdMin: number;
    leaseDays: number | null;
    status?: DeploymentDetail["status"];
    vms: VmSpec[];
    inputs: Record<string, unknown>;
    requestNumber?: string;
  }
  const deployment = (s: DepSpec): DeploymentDetail => {
    const resources = s.vms.flatMap((v) => vmResources(v));
    const vms = resources.filter((r): r is VmResource => r.type === "vsphere.vm");
    const first = vms[0];
    return {
      id: id("dep"),
      name: s.name,
      status: s.status ?? "ready",
      project: ref(s.project),
      environment: s.env,
      catalog_item: { id: s.item.id, slug: s.item.slug, name: s.item.name },
      version: s.item.version,
      owner: s.owner,
      created_at: ago(s.createdMin),
      created_by_request_id: s.requestNumber ?? `REQ-${10000 + (idSeq % 400)}`,
      lease: {
        expires_at: s.leaseDays === null ? null : inDays(s.leaseDays),
        extensions_used: s.leaseDays !== null && s.leaseDays < 7 ? 1 : 0,
        extensions_max: s.leaseDays === null ? null : 2,
        max_days: s.leaseDays === null ? null : 90,
        grace_days: 7,
        expiry_action: "power_off_then_delete",
      },
      vm_count: vms.length,
      vms_powered_on: vms.filter((v) => v.vm.power_state === "on").length,
      primary_address: first ? { ip: first.vm.nics[0]?.ip_addresses?.[0] ?? "", fqdn: first.vm.fqdn ?? "" } : null,
      sync_state: "in_sync",
      inputs: s.inputs,
      resources,
    };
  };
  const [rhel, ubuntu, windows, pg] = items as [StoredItem, StoredItem, StoredItem, StoredItem];
  const deployments: DeploymentDetail[] = [
    deployment({
      name: "web-prd-front",
      item: rhel,
      project: web,
      env: "prd",
      owner: lea,
      createdMin: 60 * 24 * 41,
      leaseDays: null,
      requestNumber: "REQ-10233",
      vms: [
        { name: "web-prd-front001", env: "prd", ip: "10.21.10.37", memGb: 8, cpuPct: 37, memPct: 61 },
        { name: "web-prd-front002", env: "prd", ip: "10.21.10.54", memGb: 8, cpuPct: 33, memPct: 58 },
        { name: "web-prd-front003", env: "prd", ip: "10.21.10.71", memGb: 8, cpuPct: 41, memPct: 64 },
      ],
      inputs: { role: "front", size: "M", count: 3, post_config: ["linux-baseline", "idm-join", "role-nginx"] },
    }),
    deployment({
      name: "web-prd-cache",
      item: rhel,
      project: web,
      env: "prd",
      owner: lea,
      createdMin: 60 * 24 * 88,
      leaseDays: null,
      vms: [
        { name: "web-prd-cache001", env: "prd", ip: "10.21.10.80", cpu: 2 },
        { name: "web-prd-cache002", env: "prd", ip: "10.21.10.81", cpu: 2 },
      ],
      inputs: { role: "cache", size: "S", count: 2, post_config: ["linux-baseline"] },
    }),
    deployment({
      name: "web-prd-edge",
      item: ubuntu,
      project: web,
      env: "prd",
      owner: thomas,
      createdMin: 60 * 24 * 30,
      leaseDays: null,
      vms: [
        { name: "web-prd-edge001", env: "prd", ip: "10.21.10.12", template: templates.ubuntu },
        { name: "web-prd-edge002", env: "prd", ip: "10.21.10.13", template: templates.ubuntu },
      ],
      inputs: { role: "edge", size: "M", count: 2 },
    }),
    deployment({
      name: "web-dev-api",
      item: rhel,
      project: web,
      env: "dev",
      owner: thomas,
      createdMin: 60 * 24 * 23,
      leaseDays: 2,
      vms: [{ name: "web-dev-api004", env: "dev", ip: "10.31.10.84", snapshots: 1 }],
      inputs: { role: "api", size: "M", post_config: ["linux-baseline"] },
    }),
    deployment({
      name: "web-dev-worker",
      item: rhel,
      project: web,
      env: "dev",
      owner: lea,
      createdMin: 95,
      leaseDays: 14,
      status: "failed",
      requestNumber: "REQ-10482",
      vms: [{ name: "web-dev-worker002", env: "dev", ip: "10.31.10.112", cpu: 2, memGb: 4, cpuPct: 3, memPct: 12 }],
      inputs: { role: "worker", size: "S", post_config: ["linux-baseline", "idm-join"] },
    }),
    deployment({
      name: "web-dev-legacy",
      item: windows,
      project: web,
      env: "dev",
      owner: thomas,
      createdMin: 60 * 24 * 76,
      leaseDays: 1,
      vms: [
        {
          name: "web-dev-legacy001",
          env: "dev",
          ip: "10.31.10.140",
          template: templates.windows,
          diskGb: 120,
          on: false,
        },
      ],
      inputs: { role: "legacy", size: "M" },
    }),
    deployment({
      name: "web-tst-search",
      item: ubuntu,
      project: web,
      env: "tst",
      owner: lea,
      createdMin: 60 * 24 * 12,
      leaseDays: 5,
      requestNumber: "REQ-10461",
      vms: [
        {
          name: "web-tst-search001",
          env: "tst",
          ip: "10.41.10.23",
          cpu: 8,
          memGb: 32,
          diskGb: 250,
          template: templates.ubuntu,
        },
        {
          name: "web-tst-search002",
          env: "tst",
          ip: "10.41.10.22",
          cpu: 8,
          memGb: 32,
          diskGb: 250,
          template: templates.ubuntu,
          on: false,
        },
      ],
      inputs: { role: "search", size: "L", count: 2 },
    }),
    deployment({
      name: "data-prd-pg-ledger",
      item: pg,
      project: data,
      env: "prd",
      owner: sophie,
      createdMin: 60 * 24 * 130,
      leaseDays: null,
      vms: [
        {
          name: "data-prd-pgledger001",
          env: "prd",
          ip: "10.22.30.11",
          cpu: 8,
          memGb: 64,
          diskGb: 500,
          network: "dvpg-db-prd-vl230",
        },
        {
          name: "data-prd-pgledger002",
          env: "prd",
          ip: "10.22.30.12",
          cpu: 8,
          memGb: 64,
          diskGb: 500,
          network: "dvpg-db-prd-vl230",
        },
        {
          name: "data-prd-pgledger003",
          env: "prd",
          ip: "10.22.30.13",
          cpu: 8,
          memGb: 64,
          diskGb: 500,
          network: "dvpg-db-prd-vl230",
        },
      ],
      inputs: { role: "ledger", size: "XL", pg_version: "16.6", backup_retention_days: 35 },
    }),
  ];

  // ── Requests ──────────────────────────────────────────────────────────
  const step = (o: Partial<Step> & Pick<Step, "key" | "title" | "status">): Step => ({
    id: id("stp"),
    children: [],
    has_log: false,
    started_at: null,
    finished_at: null,
    ...o,
  });
  const reqBase = (
    number: string,
    it: StoredItem,
    project: Project,
    env: EnvironmentKey,
    requester: UserRef,
    submittedMin: number,
    title: string,
  ) => ({
    id: `req_${number.slice(4)}`,
    number,
    kind: "provision" as const,
    title,
    catalog_item: { id: it.id, slug: it.slug, name: it.name, type: it.type },
    version: it.version,
    project: ref(project),
    environment: env,
    requester,
    submitted_at: ago(submittedMin),
    count: 1,
    deployment_ids: [] as string[],
    current_step: null,
    error: null,
    scheduled_at: null,
    started_at: null,
    finished_at: null,
  });

  const workerDep = deployments.find((d) => d.name === "web-dev-worker")!;
  const failedJob = step({
    key: "awx.job",
    title: "AWX job 18229 · linux-baseline",
    detail: "limit=web-dev-worker002",
    system: "awx",
    status: "failed",
    started_at: ago(90),
    finished_at: ago(88),
    has_log: true,
    error: "idm : join realm failed on web-dev-worker002",
    external_ref: { kind: "awx_job", id: "18229", url: "https://awx.corp.example/#/jobs/playbook/18229/output" },
    children: [
      step({ key: "awx.task", title: "Gathering Facts", status: "done", system: "awx" }),
      step({ key: "awx.task", title: "baseline : configure chrony", status: "done", system: "awx" }),
      step({ key: "awx.task", title: "baseline : harden sshd", status: "done", system: "awx" }),
      step({
        key: "awx.task",
        title: "idm : join realm",
        status: "failed",
        system: "awx",
        error: "ipa-client-install: DNS discovery failed for idm.corp.example",
      }),
    ],
  });
  const failedLog: LogLine[] = (
    [
      ["header", "PLAY [Linux baseline] *******************************************************"],
      ["plain", ""],
      ["header", "TASK [Gathering Facts] ******************************************************"],
      ["ok", "ok: [web-dev-worker002]"],
      ["plain", ""],
      ["header", "TASK [baseline : configure chrony] ******************************************"],
      ["changed", "changed: [web-dev-worker002]"],
      ["plain", ""],
      ["header", "TASK [baseline : harden sshd] ***********************************************"],
      ["changed", "changed: [web-dev-worker002]"],
      ["plain", ""],
      ["header", "TASK [idm : join realm] *****************************************************"],
      [
        "failed",
        'fatal: [web-dev-worker002]: FAILED! => {"changed": false, "msg": "ipa-client-install failed: Unable to discover domain, not provisioning DNS records for idm.corp.example"}',
      ],
      ["plain", ""],
      ["recap", "PLAY RECAP ******************************************************************"],
      ["failed", "web-dev-worker002          : ok=3    changed=2    unreachable=0    failed=1    skipped=0"],
    ] as const
  ).map(([kind, text], n) => ({ n, text, kind }));

  const requests: StoredRequest[] = [
    {
      request: {
        ...reqBase("REQ-10482", rhel, web, "dev", lea, 95, "RHEL 9 server · web-dev-worker002"),
        version: { id: "ver_rhel9_31", number: "3.1" },
        status: "failed",
        inputs: workerDep.inputs,
        justification: "Background worker for the image resizer.",
        started_at: ago(95),
        finished_at: ago(88),
        deployment_ids: [workerDep.id],
        progress: { done: 7, total: 9 },
        error: {
          step_id: failedJob.id,
          message: "The VM could not join IdM: DNS discovery failed for idm.corp.example.",
        },
      },
      steps: [
        step({
          key: "request.submit",
          title: "Submitted",
          status: "done",
          system: "rvav",
          started_at: ago(95),
          finished_at: ago(95),
        }),
        step({
          key: "policy.approval",
          title: "Approval not required",
          detail: "environment = dev",
          system: "policy",
          status: "done",
          started_at: ago(95),
          finished_at: ago(95),
        }),
        step({
          key: "ipam.reserve",
          title: "Reserved 10.31.10.112",
          detail: "Subnet 10.31.10.0/24",
          system: "efficientip",
          status: "done",
          started_at: ago(95),
          finished_at: ago(95),
        }),
        step({
          key: "dns.create",
          title: "Created A and PTR records",
          detail: `web-dev-worker002.${DOMAIN}`,
          system: "efficientip",
          status: "done",
          started_at: ago(95),
          finished_at: ago(94),
        }),
        step({
          key: "vsphere.clone",
          title: "Cloned from tpl-rhel9.4-2026.10",
          detail: "GVA-DEV-CL01 · vsan-gva-dev-cl01",
          system: "vcenter",
          status: "done",
          started_at: ago(94),
          finished_at: ago(91),
        }),
        step({
          key: "vsphere.power_on",
          title: "Powered on · guest IP 10.31.10.112",
          system: "vcenter",
          status: "done",
          started_at: ago(91),
          finished_at: ago(90),
        }),
        step({
          key: "awx.inventory_update",
          title: "Refreshed AWX inventory “rvav”",
          system: "awx",
          status: "done",
          started_at: ago(90),
          finished_at: ago(90),
        }),
        failedJob,
        step({ key: "request.ready", title: "Ready", status: "skipped", system: "rvav" }),
      ],
      logs: { [failedJob.id]: failedLog },
    },
    pendingApproval(
      "REQ-10477",
      rhel,
      web,
      "prd",
      thomas,
      240,
      "RHEL 9 server · web-prd-front004",
      "Production VM above 32 GB RAM",
      {
        role: "front",
        size: "XL",
        cluster: "GVA-PRD-CL01",
        datastore: "vsan-gva-prd-cl01",
        network: "dvpg-web-prd-vl210",
        post_config: ["linux-baseline", "idm-join", "role-nginx"],
      },
    ),
    pendingApproval(
      "REQ-10479",
      ubuntu,
      data,
      "prd",
      sophie,
      180,
      "Ubuntu 24.04 server · data-prd-spark003",
      "Production VM above 32 GB RAM",
      {
        role: "spark",
        size: "XL",
        cluster: "GVA-PRD-CL02",
        datastore: "vsan-gva-prd-cl02",
        network: "dvpg-db-prd-vl230",
        post_config: ["linux-baseline"],
      },
    ),
    pendingApproval(
      "REQ-10471",
      items.find((i) => i.slug === "firewall-flow")!,
      web,
      "prd",
      lea,
      60 * 26,
      "Open firewall flow · web-prd-front → data-prd-pg-ledger :5432",
      "Flows into the production database zone",
      { source: "web-prd-front", destination: "data-prd-pg-ledger", protocol: "TCP", port: 5432 },
    ),
    finished(
      "REQ-10466",
      items.find((i) => i.slug === "db-user")!,
      data,
      "prd",
      sophie,
      60 * 30,
      "Create database user · app_reporting",
      "succeeded",
      {
        database: "ledger",
        role_name: "app_reporting",
        privileges: ["SELECT"],
      },
    ),
    finished(
      "REQ-10461",
      ubuntu,
      web,
      "tst",
      lea,
      60 * 24 * 12,
      "Ubuntu 24.04 server · web-tst-search002",
      "succeeded",
      {
        role: "search",
        size: "L",
      },
    ),
    finished(
      "REQ-10455",
      items.find((i) => i.slug === "dns-record")!,
      web,
      "prd",
      lea,
      60 * 24 * 13,
      "DNS record · status.corp.example CNAME",
      "rejected",
      {
        zone: "corp.example",
        name: "status",
        record_type: "CNAME",
        value: "statuspage.example.net",
      },
    ),
  ];
  requests.find((r) => r.request.number === "REQ-10461")!.request.deployment_ids = [
    deployments.find((d) => d.name === "web-tst-search")!.id,
  ];

  function pendingApproval(
    number: string,
    it: StoredItem,
    project: Project,
    env: EnvironmentKey,
    requester: UserRef,
    submittedMin: number,
    title: string,
    reason: string,
    inputs: Record<string, unknown>,
  ): StoredRequest {
    const waiting = step({
      key: "policy.approval",
      title: "Waiting for approval",
      detail: `${reason} · Infrastructure leads`,
      system: "policy",
      status: "running",
      started_at: ago(submittedMin),
    });
    return {
      request: {
        ...reqBase(number, it, project, env, requester, submittedMin, title),
        status: "pending_approval",
        inputs,
        current_step: { id: waiting.id, title: waiting.title },
        progress: { done: 1, total: 2 },
      },
      steps: [
        step({
          key: "request.submit",
          title: "Submitted",
          status: "done",
          system: "rvav",
          started_at: ago(submittedMin),
          finished_at: ago(submittedMin),
        }),
        waiting,
      ],
      logs: {},
    };
  }

  function finished(
    number: string,
    it: StoredItem,
    project: Project,
    env: EnvironmentKey,
    requester: UserRef,
    submittedMin: number,
    title: string,
    status: "succeeded" | "rejected",
    inputs: Record<string, unknown>,
  ): StoredRequest {
    const steps =
      status === "rejected"
        ? [
            step({
              key: "request.submit",
              title: "Submitted",
              status: "done",
              system: "rvav",
              started_at: ago(submittedMin),
              finished_at: ago(submittedMin),
            }),
            step({
              key: "policy.approval",
              title: `Rejected by ${marc.name}`,
              detail: "“Use the shared status page record instead.”",
              system: "policy",
              status: "failed",
              started_at: ago(submittedMin),
              finished_at: ago(submittedMin - 40),
            }),
          ]
        : [
            step({
              key: "request.submit",
              title: "Submitted",
              status: "done",
              system: "rvav",
              started_at: ago(submittedMin),
              finished_at: ago(submittedMin),
            }),
            step({
              key: "request.ready",
              title: "Ready",
              status: "done",
              system: "rvav",
              started_at: ago(submittedMin - 6),
              finished_at: ago(submittedMin - 6),
            }),
          ];
    return {
      request: {
        ...reqBase(number, it, project, env, requester, submittedMin, title),
        status,
        inputs,
        started_at: status === "succeeded" ? ago(submittedMin) : null,
        finished_at: ago(submittedMin - 6),
        progress: { done: steps.length, total: steps.length },
      },
      steps,
      logs: {},
    };
  }

  // Approval levels of the waiting requests. Marc Keller is away and has
  // delegated his to Léa Martin, so the mock user has an inbox.
  const byNumber = (n: string) => requests.find((r) => r.request.number === n)!.request;
  const level = (
    number: string,
    o: Partial<StoredApproval> & Pick<StoredApproval, "level" | "reasons" | "approvers" | "status">,
  ): StoredApproval => {
    const r = byNumber(number);
    return {
      id: `apr_${number.slice(4)}_${o.level.index}`,
      request: { id: r.id, number: r.number, title: r.title, status: r.status },
      project: r.project,
      environment: r.environment,
      requester: r.requester,
      submitted_at: r.submitted_at,
      rules: [],
      due_at: null,
      escalated: false,
      justification: r.justification,
      facts: factsFor(r.inputs ?? {}, r.title.split(" · ")[1] ?? null),
      group: null,
      mode: "any",
      votes: [],
      delegates: [],
      on_timeout: null,
      escalate_to: null,
      decided_at: null,
      outcome: null,
      ...o,
    };
  };
  const leads = {
    approvers: [marc],
    group: "rvav-infra-leads",
    delegates: [lea],
    on_timeout: "escalate" as const,
    escalate_to: [claire],
  };
  byNumber("REQ-10477").justification =
    "Fourth front node for Black Friday. The JVM heap is sized at 32 GB, the rest is page cache for static assets.";
  byNumber("REQ-10479").justification = "Spark executors for the year-end batch; the jobs spill to disk below 48 GB.";
  const approvals: StoredApproval[] = [
    level("REQ-10477", {
      ...leads,
      level: { index: 1, count: 1, name: "Infrastructure leads" },
      reasons: ["Production VM above 32 GB RAM", "Production VMs need sign-off"],
      rules: ['environment == "prd" && vm.memory_gb > 32', 'environment == "prd"'],
      status: "pending",
      due_at: inDays(20 / 24),
    }),
    level("REQ-10479", {
      ...leads,
      level: { index: 1, count: 1, name: "Infrastructure leads" },
      reasons: ["Production VM above 32 GB RAM", "Production VMs need sign-off"],
      rules: ['environment == "prd" && vm.memory_gb > 32', 'environment == "prd"'],
      status: "pending",
      due_at: inDays(1.5),
    }),
    level("REQ-10471", {
      level: { index: 1, count: 2, name: "Network engineers" },
      reasons: ["Firewall changes are reviewed by network security"],
      approvers: [alain],
      status: "approved",
      votes: [
        {
          user: alain,
          decision: "approve",
          comment: "Ports and zones match the flow matrix.",
          changes: [],
          decided_at: ago(21 * 60),
        },
      ],
      decided_at: ago(21 * 60),
      outcome: "Approved by Alain Favre",
    }),
    level("REQ-10471", {
      level: { index: 2, count: 2, name: "Network security" },
      reasons: ["Firewall changes are reviewed by network security"],
      approvers: [marc],
      status: "pending",
      due_at: ago(60),
    }),
  ];

  const ipNext: Record<string, number> = {};
  for (const n of networks) ipNext[n.name] = n.nextHost;

  return {
    me,
    users,
    environments,
    projects,
    quota: {
      [web.id]: {
        limits: { vcpu: 160, memory_gb: 640, storage_gb: 12000, vm_count: 60 },
        used: { vcpu: 112, memory_gb: 448, storage_gb: 7200, vm_count: 38 },
      },
      [data.id]: {
        limits: { vcpu: 256, memory_gb: 2048, storage_gb: 60000, vm_count: 40 },
        used: { vcpu: 184, memory_gb: 1472, storage_gb: 41500, vm_count: 27 },
      },
    },
    categories,
    items,
    favourites: new Set(["itm_rhel9", "itm_ubuntu", "itm_dbuser"]),
    forms,
    requests,
    approvals,
    deployments,
    idempotency: new Map(),
    ipNext,
    seq: { request: 10491, id: idSeq + 1000, moref: moref + 500, event: 0 },
  };
}

export let db: Db = createDb();

export function resetDb(now?: number) {
  db = createDb(now);
}
