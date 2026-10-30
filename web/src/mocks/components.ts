// Multi-VM blueprints in the mock (ADR-0024): the web application item
// builds web servers kept on separate hosts and a database server, as one
// deployment. Other VM items are one component, "vm".
import type { EnvironmentKey, PreviewComponent, Project } from "@/lib/api/types";
import { clusters, networks, sizes, templates, type SizeKey } from "./inventory";
import { nextFreeIp, vmNames } from "./logic";
import type { Db, StoredItem } from "./seed";

/** The slug of the mock's multi-VM item. */
export const MULTI_SLUG = "web-app";

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

/** The deployment name of a web application. */
export function webAppName(project: Project, env: EnvironmentKey, inputs: Inputs) {
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
    item.slug === MULTI_SLUG
      ? webAppParts(inputs)
      : item.slug === "postgres-cluster"
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
          ];
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
        {
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
export const webAppVMs = (inputs: Inputs) => webAppParts(inputs).reduce((n, p) => n + p.count, 0);
