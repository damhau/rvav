// Multi-VM blueprints in the mock (ADR-0024): the web application item
// builds web servers kept on separate hosts and a database server, as one
// deployment; the three-tier item (ADR-0028) its web, application and
// database tiers on an NSX segment made for it. Other VM items are one
// component, "vm".
import type { EnvironmentKey, PreviewComponent, Project } from "@/lib/api/types";
import { clusters, networks, sizes, templates, type SizeKey } from "./inventory";
import { nextFreeIp, vmNames } from "./logic";
import type { Db, StoredItem } from "./seed";

type Inputs = Record<string, unknown>;

export interface Part {
  key: string;
  title: string;
  count: number;
  /** What goes into its VM names, as the role does for a single VM. */
  role: string;
  size: SizeKey;
  os: "linux" | "windows";
  template: string;
  antiAffinity: boolean;
  post: string[];
  /** Its NSX security groups, as the preview labels them (ADR-0028). */
  nsxGroups?: string[];
  /** The key of the NSX segment made for the deployment its VMs are on. */
  segment?: string;
}

const text = (v: unknown, fallback = "") => (typeof v === "string" && v ? v : fallback);

/** The web application's components for these inputs. */
export function webAppParts(inputs: Inputs): Part[] {
  const app = text(inputs.app, "app")
    .toLowerCase()
    .replace(/[^a-z]/g, "");
  const nodes = typeof inputs.web_nodes === "number" ? inputs.web_nodes : 2;
  return [
    {
      key: "web",
      title: "Web servers",
      count: nodes,
      role: `${app}web`,
      size: (inputs.size as SizeKey) ?? "M",
      os: "linux",
      template: templates.rhel9.name,
      antiAffinity: nodes > 1,
      post: ["linux-baseline"],
    },
    {
      key: "db",
      title: "Database",
      count: 1,
      role: `${app}db`,
      size: "L",
      os: "linux",
      template: templates.rhel9.name,
      antiAffinity: false,
      post: [],
    },
  ];
}

/** The three-tier item's tiers: each in its own group, on the segment made for it. */
export function threeTierParts(inputs: Inputs): Part[] {
  const nodes = typeof inputs.web_nodes === "number" ? inputs.web_nodes : 2;
  const tier = (key: string, title: string, count: number, size: SizeKey, groups: string[]): Part => ({
    key,
    title,
    count,
    role: `tt${key}`,
    size,
    os: "linux",
    template: templates.rhel9.name,
    antiAffinity: key === "web" && count > 1,
    post: key === "web" ? ["linux-baseline"] : [],
    nsxGroups: groups,
    segment: "app",
  });
  return [
    tier("web", "Web", nodes, "M", ["Web (new)"]),
    tier("app", "Application", 1, "M", ["Application (new)"]),
    tier("db", "Database", 1, "L", ["Database (new)", "grp-backup-clients"]),
  ];
}

/** A multi-VM item's components for these inputs; undefined for any other item. */
export function multiParts(slug: string, inputs: Inputs): Part[] | undefined {
  if (slug === "web-app") return webAppParts(inputs);
  if (slug === "three-tier") return threeTierParts(inputs);
  return undefined;
}

/** The deployment name of a multi-VM item. */
export function multiName(slug: string, project: Project, env: EnvironmentKey, inputs: Inputs) {
  if (slug === "three-tier") return `${project.code}-${env}-threetier`;
  const app = text(inputs.app, "app")
    .toLowerCase()
    .replace(/[^a-z]/g, "");
  return `${project.code}-${env}-${app}`;
}

/** What each component builds, as the server's preview says. */
export function previewComponents(
  db: Db,
  item: StoredItem,
  project: Project,
  env: EnvironmentKey,
  inputs: Inputs,
  count: number,
): PreviewComponent[] {
  const network = text(inputs.network);
  const next = network ? nextFreeIp(db, network) : null;
  const cluster = text(inputs.cluster) || clusters.find((c) => c.env === env)?.name;
  const datastore = text(inputs.datastore) || undefined;
  const parts: Part[] =
    multiParts(item.slug, inputs) ??
    (item.slug === "postgres-cluster"
      ? // A composite: its VM step's components, keyed and titled under it.
        [
          {
            key: "nodes/node",
            title: "Database nodes · PostgreSQL nodes",
            count: 3 * count,
            role: `pg${text(inputs.name)
              .toLowerCase()
              .replace(/[^a-z]/g, "")}`,
            size: (inputs.size as SizeKey) ?? "M",
            os: "linux",
            template: templates.rhel9.name,
            antiAffinity: true,
            post: [],
            nsxGroups: ["Database servers (new)", "grp-backup-clients"],
          },
        ]
      : [
          {
            key: "vm",
            title: item.name,
            count,
            role: text(inputs.role, "app"),
            size: (inputs.size as SizeKey) ?? "M",
            os: item.slug === "windows-server" ? "windows" : "linux",
            template:
              item.slug === "ubuntu-server"
                ? templates.ubuntu.name
                : item.slug === "windows-server"
                  ? templates.windows.name
                  : templates.rhel9.name,
            antiAffinity: false,
            post: [],
          },
        ]);
  return parts.map((p) => {
    const size = sizes[p.size] ?? sizes.M;
    return {
      key: p.key,
      title: p.title,
      count: p.count,
      names: p.count > 0 ? vmNames(db, project, env, p.role, p.count) : [],
      vcpu: size.vcpu,
      memory_gb: size.memoryGb,
      storage_gb: size.diskGb,
      os_family: p.os,
      template: p.template,
      placement: { cluster, datastore },
      disks: [{ size_gb: size.diskGb, datastore }],
      nics: [
        p.segment
          ? { key: "nic1", network: `A new NSX segment (${p.segment})`, ipam: "efficientip" as const }
          : {
              key: "nic1",
              network,
              ipam: "efficientip" as const,
              subnet: networks.find((n) => n.name === network)?.subnet,
              address: next?.ip,
            },
      ],
      tags: [],
      anti_affinity: p.antiAffinity,
      ...(p.nsxGroups ? { nsx_groups: p.nsxGroups } : {}),
    };
  });
}

/** How many VMs a multi-VM item builds. */
export const partVMs = (parts: Part[]) => parts.reduce((n, p) => n + p.count, 0);
