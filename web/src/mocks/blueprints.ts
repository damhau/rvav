// The seeded items' blueprints in the mock, as the server's seed writes them
// (internal/seed/catalog.go): what catalog administration's Definition tab
// shows and the canvas draws (ADR-0032). The three-tier item is N1's
// acceptance item (ADR-0028): its tiers on a segment made for the
// deployment, each in a security group of its own, with the rules between
// them.
import { sizes as inventorySizes, templates } from "./inventory";

type Blueprint = Record<string, unknown>;

const sizes = Object.fromEntries(
  Object.entries(inventorySizes).map(([k, s]) => [
    k,
    { label: s.label, vcpu: s.vcpu, memory_gb: s.memoryGb, disk_gb: s.diskGb },
  ]),
);

const bySize = {
  vcpu: "sizes[inputs.size].vcpu",
  memory_gb: "sizes[inputs.size].memory_gb",
  disk_gb: "sizes[inputs.size].disk_gb",
};

const guest = (os: "linux" | "windows") => ({ os_family: os, domain: "corp.example", time_zone: "Europe/Zurich" });

const awxTemplates = (list: [string, string][]) =>
  Object.fromEntries(list.map(([key, title]) => [key, { title, job_template: key }]));

const linuxPost: [string, string][] = [
  ["linux-baseline", "Corporate baseline"],
  ["idm-join", "Join IdM"],
  ["monitoring-agent", "Monitoring agent"],
  ["role-nginx", "nginx"],
];

/** One VM, its AWX jobs chosen in the form. */
function singleVM(template: string, os: "linux" | "windows", post: [string, string][]): Blueprint {
  return {
    kind: "vm",
    naming: "{project}-{environment}-{inputs.role}{seq:03}",
    template,
    guest: guest(os),
    sizes,
    bindings: {
      ...bySize,
      cluster: "inputs.cluster",
      datastore: "inputs.datastore",
      network: "inputs.network",
      post_config: "inputs.post_config",
    },
    awx_templates: awxTemplates(post),
  };
}

const seeded: Record<string, Blueprint> = {
  "rhel9-server": singleVM(templates.rhel9.name, "linux", linuxPost),
  "ubuntu-server": singleVM(templates.ubuntu.name, "linux", linuxPost),
  "windows-server": singleVM(templates.windows.name, "windows", [
    ["windows-baseline", "Corporate baseline"],
    ["ad-join", "Join corp.example"],
    ["monitoring-agent", "Monitoring agent"],
  ]),
  // Web servers kept apart and a database server, on the request's network (ADR-0024).
  "web-app": {
    kind: "vm",
    naming: "{project}-{environment}-{inputs.app}{component}{seq:02}",
    deployment_name: "{project}-{environment}-{inputs.app}",
    template: templates.rhel9.name,
    guest: guest("linux"),
    sizes,
    bindings: { ...bySize, cluster: "inputs.cluster", datastore: "inputs.datastore", network: "inputs.network" },
    awx_templates: awxTemplates([["linux-baseline", "Corporate baseline"]]),
    components: [
      {
        key: "web",
        title: "Web servers",
        count: "inputs.web_nodes",
        anti_affinity: true,
        bindings: { post_config: '["linux-baseline"]' },
      },
      {
        key: "db",
        title: "Database",
        count: 1,
        bindings: { vcpu: 'sizes["L"].vcpu', memory_gb: 'sizes["L"].memory_gb', disk_gb: 'sizes["L"].disk_gb' },
      },
    ],
  },
  // N1's three tiers on NSX (ADR-0028).
  "three-tier": {
    kind: "vm",
    naming: "{project}-{environment}-tt{component}{seq:02}",
    deployment_name: "{project}-{environment}-threetier",
    rollback_on_failure: true,
    template: templates.rhel9.name,
    guest: guest("linux"),
    bindings: {
      vcpu: "4",
      memory_gb: "16",
      disk_gb: "80",
      cluster: "inputs.cluster",
      datastore: "inputs.datastore",
      network: "segments.app",
    },
    awx_templates: awxTemplates([["linux-baseline", "Corporate baseline"]]),
    segments: [{ key: "app", title: "Application" }],
    components: [
      {
        key: "web",
        title: "Web",
        count: "inputs.web_nodes",
        anti_affinity: true,
        groups: ["web"],
        bindings: { post_config: '["linux-baseline"]' },
      },
      { key: "app", title: "Application", count: 1, groups: ["app"] },
      {
        key: "db",
        title: "Database",
        count: 1,
        groups: ["db", "nsx:grp-backup-clients"],
        bindings: { vcpu: "8", memory_gb: "32", disk_gb: "120" },
        disks: [{ size_gb: 200, storage_policy: "gold" }],
      },
    ],
    security_groups: [
      { key: "web", title: "Web", rules: [{ from: ["any"], services: ["HTTPS"] }] },
      { key: "app", title: "Application", rules: [{ from: ["web"], services: ["HTTP"] }] },
      {
        key: "db",
        title: "Database",
        rules: [
          { from: ["app"], services: ["PostgreSQL"] },
          { from: ["any"], services: ["any"], action: "drop" },
        ],
      },
    ],
  },
  // Its nodes, then the database's owner and its backups at the same time (ADR-0022).
  "postgres-cluster": {
    kind: "composite",
    rollback_on_failure: true,
    steps: [
      {
        key: "nodes",
        title: "Database nodes",
        item: "pg-node",
        with: {
          name: "inputs.role",
          nodes: 3,
          pg_version: "inputs.pg_version",
          size: "inputs.size",
          network: "inputs.network",
        },
      },
      {
        parallel: [
          {
            key: "owner",
            title: "The database's owner",
            item: "db-user",
            with: { database: "inputs.role", role_name: 'inputs.role + "_owner"', privileges: '"owner"' },
          },
          {
            key: "backups",
            title: "Backups",
            awx: { job_template: "pgbackrest-stanza", title: "pgBackRest stanza" },
            when: "inputs.backup_retention_days > 0",
            with: { hosts: "steps.nodes.vms", retention_days: "inputs.backup_retention_days" },
            undo: { job_template: "pgbackrest-stanza-delete", with: { hosts: "steps.nodes.vms" } },
          },
        ],
      },
    ],
  },
};

/** A seeded item's blueprint, if the mock has one written out. */
export function seededBlueprint(slug: string): Blueprint | undefined {
  const bp = seeded[slug];
  return bp ? structuredClone(bp) : undefined;
}
