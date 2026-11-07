// The seeded items' blueprints in the mock, as the server's seed writes them
// (internal/seed/catalog.go): what catalog administration's Definition tab
// shows and the canvas draws (ADR-0032). The three-tier item is N1's
// acceptance item (ADR-0028): its tiers on a segment made for the
// deployment, each in a security group of its own, with the rules between
// them.
import { parseUrl } from "@/lib/composite";
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
  // The cluster's nodes, kept on separate hosts: an item only the cluster requests.
  "pg-node": {
    kind: "vm",
    naming: "{project}-{environment}-pg{inputs.name}{seq:02}",
    deployment_name: "{project}-{environment}-pg-{inputs.name}",
    template: templates.rhel9.name,
    rollback_on_failure: true,
    guest: guest("linux"),
    sizes,
    bindings: { ...bySize, network: "inputs.network" },
    awx_templates: awxTemplates([
      ["linux-baseline", "Corporate baseline"],
      ["postgresql-install", "PostgreSQL"],
      ["patroni-cluster", "Patroni cluster"],
    ]),
    components: [
      {
        key: "db",
        title: "Database nodes",
        count: "inputs.nodes",
        anti_affinity: true,
        bindings: { post_config: '["linux-baseline", "postgresql-install", "patroni-cluster"]' },
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

// ---- What a requester sees of a blueprint -----------------------------------

type Keep = (v: unknown) => unknown;
const isObject = (v: unknown): v is Blueprint => typeof v === "object" && v !== null && !Array.isArray(v);
const asIs: Keep = (v) => v;
const fields =
  (keys: Record<string, Keep>): Keep =>
  (v) => {
    if (!isObject(v)) return null;
    const out: Blueprint = {};
    for (const [k, keep] of Object.entries(keys)) {
      const kept = k in v ? keep(v[k]) : null;
      if (kept !== null && kept !== undefined) out[k] = kept;
    }
    return out;
  };
const each =
  (keep: Keep): Keep =>
  (v) =>
    Array.isArray(v) ? v.map(keep) : null;
const values =
  (keep: Keep): Keep =>
  (v) =>
    isObject(v) ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, keep(x)])) : null;
/** What a string literal reads as once masked, and a whole expression that isn't CEL. */
const masked = "…";

/** The string or bytes literal written at src[i] (policy.literalAt): its text from open to end, where it stops. */
function literalAt(src: string[], i: number): { open: number; end: number; stop: number; bytes: boolean } | null {
  let j = i;
  let raw = false;
  let bytes = false;
  while (j < src.length && j - i < 2 && /[rRbB]/.test(src[j]!)) {
    raw ||= /[rR]/.test(src[j]!);
    bytes ||= /[bB]/.test(src[j]!);
    j++;
  }
  if (src[j] !== "'" && src[j] !== '"') return null;
  let q = src[j]!;
  if (src.slice(j, j + 3).join("") === q.repeat(3)) q = q.repeat(3);
  const open = j + q.length;
  for (let k = open; k < src.length; k++) {
    if (!raw && src[k] === "\\") {
      k++;
      continue;
    }
    if (src.slice(k, k + q.length).join("") === q) return { open, end: k, stop: k + q.length, bytes };
  }
  return null;
}

/**
 * A CEL expression as a requester reads it, as the server's
 * policy.MaskLiterals gives it (ADR-0032): each string literal's text
 * replaced by "…" in its own quotes, comments removed, the rest as written;
 * an empty literal kept. With lone, an expression that is only a string
 * literal, or a list of them, is kept: the value the canvas draws by name.
 * The server finds the literals with cel-go's parser; this scanner finds
 * the same in CEL that parses, and says "…" for a literal left open.
 */
export function maskLiterals(expr: string, lone: boolean): string {
  const src = [...expr];
  const literals: { from: number; open: number; end: number; stop: number; bytes: boolean }[] = [];
  const kept: string[] = []; // the text without comments, a literal as one placeholder
  for (let i = 0; i < src.length;) {
    const c = src[i]!;
    const word = /[A-Za-z_]/.test(c) && !/[A-Za-z0-9_]/.test(src[i - 1] ?? " ");
    const lit = c === "'" || c === '"' || (word && /[rRbB]/.test(c)) ? literalAt(src, i) : null;
    if (lit) {
      literals.push({ from: i, ...lit });
      kept.push("\uE000");
      i = lit.stop;
    } else if (c === "'" || c === '"') return masked;
    else if (c === "/" && src[i + 1] === "/") {
      kept.push("\n");
      while (i < src.length && src[i] !== "\n") i++;
    } else if (word) {
      const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(src.slice(i).join(""))!;
      kept.push(m[0]);
      i += [...m[0]].length;
    } else {
      kept.push(c);
      i++;
    }
  }
  const shape = kept.join("").replace(/\s+/g, "");
  const only = (shape === "\uE000" && !literals[0]?.bytes) || /^\[\uE000(,\uE000)*,?\]$/.test(shape) || shape === "[]";
  const mask = !(lone && only);
  let out = "";
  let next = 0;
  for (let i = 0; i < src.length;) {
    const lit = literals[next];
    if (lit && lit.from === i) {
      next++;
      out +=
        mask && lit.end > lit.open
          ? src.slice(i, lit.open).join("") + masked + src.slice(lit.end, lit.stop).join("")
          : src.slice(i, lit.stop).join("");
      i = lit.stop;
    } else if (src[i] === "/" && src[i + 1] === "/") {
      out = out.replace(/[ \t]+$/, "");
      while (i < src.length && src[i] !== "\n") i++;
    } else out += src[i++];
  }
  return out;
}

/** A CEL expression, its string literals masked; a number or a boolean as it is, a list of strings where lone. */
const cel =
  (lone = false): Keep =>
  (v) => {
    if (typeof v === "string") return maskLiterals(v, lone);
    if (typeof v === "number" || typeof v === "boolean") return v;
    if (Array.isArray(v) && lone && v.every((x) => typeof x === "string")) return v;
    return null;
  };

const stepRefs: Keep = (v) =>
  isObject(v)
    ? Object.fromEntries(
        Object.entries(v).flatMap(([k, x]) => {
          const refs = typeof x === "string" ? (x.match(/\bsteps\.[a-z][a-z0-9_]*/g) ?? []) : [];
          return refs.length > 0 ? [[k, refs.join(" ")]] : [];
        }),
      )
    : null;

const bindings = fields({ network: cel(true), groups: cel(true), template: cel(true), post_config: cel(true) });
const guestOS = fields({ os_family: asIs });
const disks = each(fields({ size_gb: cel(), storage_policy: asIs }));
const nics = each(fields({ key: asIs, network: cel(true), ipam: asIs }));
const vmTopology = fields({
  kind: asIs,
  template: asIs,
  library: asIs,
  guest: guestOS,
  bindings,
  awx_templates: values(fields({ title: asIs })),
  disks,
  nics,
  groups: asIs,
  segments: each(fields({ key: asIs, title: asIs })),
  security_groups: each(
    fields({ key: asIs, title: asIs, rules: each(fields({ from: asIs, services: asIs, action: asIs })) }),
  ),
  components: each(
    fields({
      key: asIs,
      title: asIs,
      count: cel(),
      template: asIs,
      library: asIs,
      guest: guestOS,
      bindings,
      disks,
      nics,
      anti_affinity: asIs,
      groups: asIs,
    }),
  ),
});
/** A REST call's URL: its text goes (the host, the path), each ${expr} stays with its literals masked. */
const restUrl: Keep = (v) => {
  if (typeof v !== "string") return null;
  const { parts, error } = parseUrl(v);
  if (error) return masked;
  return parts.map((p) => ("expr" in p ? `\${${maskLiterals(p.expr, false)}}` : masked)).join("");
};
// A REST call's method and what it reads: not its secret's name, the statuses it accepts or what it keeps.
const restCall = fields({ method: asIs, url: restUrl, body: cel() });
// A wait for a person: that it asks the requester or the owner, not which group
// or people, nor what it says or offers; how long, and what happens then.
const askTopology = fields({
  requester: asIs,
  owner: asIs,
  timeout: asIs,
  on_timeout: asIs,
  form: each(fields({ key: asIs })),
});
const stepKeys: Record<string, Keep> = {
  key: asIs,
  title: asIs,
  item: asIs,
  awx: fields({ job_template: asIs, title: asIs }),
  workflow: asIs,
  rest: restCall,
  ask: askTopology,
  wait: fields({ for: asIs, until: cel() }),
  poll: fields({ rest: restCall, until: cel(), every: asIs, timeout: asIs, on_timeout: asIs }),
  when: cel(),
  with: stepRefs,
  undo: fields({ job_template: asIs, workflow: asIs, rest: restCall, with: stepRefs }),
};
const step = fields(stepKeys);
stepKeys.parallel = each(step);
const compositeTopology = fields({ kind: asIs, rollback_on_failure: asIs, steps: each(step) });

/**
 * What a request form gets of a blueprint to draw what the item builds, as
 * the server's blueprint.Topology gives it (ADR-0032): the part the canvas
 * draws, without guest settings, sizes, naming or a step's binding literals,
 * the string literals of its CEL masked. Checked against the server's on the
 * same blueprints (internal/seed/testdata/topology.json, topology.test.ts).
 * Undefined for the kinds the canvas doesn't draw.
 */
export function topologyOf(bp: Blueprint | undefined): Blueprint | undefined {
  const keep = bp?.kind === "vm" ? vmTopology : bp?.kind === "composite" ? compositeTopology : undefined;
  return keep ? (keep(bp) as Blueprint) : undefined;
}
