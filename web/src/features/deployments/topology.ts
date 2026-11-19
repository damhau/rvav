// A deployment as the canvas draws it (ADR-0032), from what it holds rather
// than from its blueprint: its VMs, the networks and NSX segments their
// adapters are on, the security groups they are in and the firewall rules
// between those, and its custom resources (ADR-0056), left to right. A
// component's VMs are drawn one by one up to ONE_BY_ONE, in a frame named
// after it when the deployment has several components; more are one node
// with their count. The creating request's topology, when it can be read,
// gives the components their titles and the order they and the custom
// resources were made in.
import { words } from "@/components/canvas/blueprint";
import type { CanvasEdge, CanvasGraph, CanvasGroup, CanvasLine, CanvasNode } from "@/components/canvas/graph";
import type {
  CustomResource,
  DeploymentDetail,
  FirewallRulesResource,
  GroupMembershipResource,
  SecurityGroupResource,
  SegmentResource,
  VmResource,
} from "@/lib/api/types";
import { celCode, dependsOn } from "@/lib/dependsOn";
import { labelledProperties } from "./properties";

/**
 * Up to this many VMs of a component are drawn one by one: four cards
 * stack within the canvas's default height and stay readable without
 * zooming out. More are one node with their count, whose details list them.
 */
export const ONE_BY_ONE = 4;

type Raw = Record<string, unknown>;
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const str = (v: unknown) => (typeof v === "string" ? v : "");

export const vmNodeId = (resourceId: string) => `vm:${resourceId}`;
export const resourceNodeId = (resourceId: string) => `resource:${resourceId}`;

/** A custom resource's card shows this many of its properties; the Overview lists them all. */
const SHOWN_PROPERTIES = 3;

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;
const gb = (mb: number) => Math.round(mb / 1024);
const power = { on: "On", off: "Off", suspended: "Suspended" } as const;

function vmLines(v: VmResource): CanvasLine[] {
  const { vm } = v;
  const ip = vm.nics.flatMap((n) => n.ip_addresses ?? []).find((a) => !a.includes(":"));
  return [
    { label: "Power", text: power[vm.power_state] },
    { label: "Address", text: ip ?? "None reported", mono: Boolean(ip) },
    { label: "Size", text: `${vm.cpu_count} vCPU · ${gb(vm.memory_mb)} GB` },
    { label: "Disks", text: vm.disks.map((d) => `${d.capacity_gb} GB`).join(", ") || "None" },
  ];
}

const syncProblem = (v: VmResource) =>
  v.sync_state === "drifted" ? "Changed outside rvav." : v.sync_state === "missing" ? "Missing in vCenter." : undefined;

/** An adapter's number, as vCenter labels it: "Network adapter 2". */
const adapterNumber = (label: string | undefined, index: number) =>
  /(\d+)$/.exec(label ?? "")?.[1] ?? String(index + 1);

const resourceLines = (r: CustomResource): CanvasLine[] => labelledProperties(r).slice(0, SHOWN_PROPERTIES);

const readsOf = (text: string, root: "components" | "resources") =>
  [...celCode(text).matchAll(new RegExp(`(?<![\\w.])${root}\\.([a-z][a-z0-9-]*)`, "g"))].map((m) => m[1]!);

/**
 * The components and resources each custom resource of the request's
 * topology is made after: those its depends_on names, and those its
 * inputs read as components.<key> or resources.<key>.
 */
function resourcesAfter(topology: unknown): Map<string, string[]> {
  const out = new Map<string, string[]>();
  const resources = obj(topology).resources;
  if (!Array.isArray(resources)) return out;
  for (const r of resources.map(obj)) {
    const key = str(r.key);
    if (!key) continue;
    const named = Array.isArray(r.depends_on) ? r.depends_on.map(str) : [];
    const texts = Object.values(obj(r.inputs)).filter((v): v is string => typeof v === "string");
    const read = texts.flatMap((t) => [...readsOf(t, "components"), ...readsOf(t, "resources")]);
    out.set(
      key,
      [...new Set([...named, ...read])].filter((k) => k && k !== key),
    );
  }
  return out;
}

/** Each component's title in the request's topology. */
function titles(topology: unknown): Map<string, string> {
  const out = new Map<string, string>();
  const components = obj(topology).components;
  if (Array.isArray(components))
    for (const c of components.map(obj)) if (str(c.key)) out.set(str(c.key), str(c.title) || str(c.key));
  return out;
}

/** The deployment's VMs by component, in the order they first appear; one "vm" component for a single-VM item. */
export function vmsByComponent(d: DeploymentDetail): Map<string, VmResource[]> {
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const multi = vms.some((v) => v.vm.component && v.vm.component !== "vm");
  const out = new Map<string, VmResource[]>();
  for (const v of vms) {
    const key = multi ? (v.vm.component ?? "vm") : "vm";
    out.set(key, [...(out.get(key) ?? []), v]);
  }
  return out;
}

/**
 * The deployment's topology: `vm:<resource ID>` for a VM drawn alone,
 * `component:<key>` for a component's VMs drawn as one, `frame:<key>`
 * around a component's VMs, `network:<name>`, `segment:<name>`,
 * `sg:<NSX ID>` for its groups, `nsx:<NSX ID>` for existing ones, `any`,
 * `resource:<resource ID>` for a custom resource.
 */
export function deploymentGraph(d: DeploymentDetail, topology?: unknown): CanvasGraph {
  const segments = d.resources.filter((r): r is SegmentResource => r.type === "nsx.segment");
  const groups = d.resources.filter((r): r is SecurityGroupResource => r.type === "nsx.group");
  const policies = d.resources.filter((r): r is FirewallRulesResource => r.type === "nsx.policy");
  const members = d.resources.filter((r): r is GroupMembershipResource => r.type === "nsx.membership");

  const computes: CanvasNode[] = [];
  const nets = new Map<string, CanvasNode>();
  const sgs = new Map<string, CanvasNode>();
  const others = new Map<string, CanvasNode>();
  const frames: CanvasGroup[] = [];
  const edges: CanvasEdge[] = [];
  const node = (n: Omit<CanvasNode, "relations" | "problems"> & { problems?: string[] }): CanvasNode => ({
    ...n,
    relations: [],
    problems: n.problems ?? [],
  });
  const relate = (n: CanvasNode, text: string) => {
    if (!n.relations.includes(text)) n.relations.push(text);
  };
  const edge = (e: CanvasEdge) => {
    if (!edges.some((x) => x.id === e.id)) edges.push(e);
  };

  for (const s of segments)
    nets.set(
      s.name,
      node({
        id: `segment:${s.name}`,
        kind: "segment",
        title: s.name,
        badge: "Segment",
        lines: [
          { label: "Subnet", text: s.segment.subnet, mono: true },
          { label: "Tier-1", text: s.segment.tier1, mono: true },
        ],
      }),
    );
  const network = (name: string) => {
    let n = nets.get(name);
    if (!n) {
      n = node({ id: `network:${name}`, kind: "network", title: name, badge: "Network", lines: [] });
      nets.set(name, n);
    }
    return n;
  };
  for (const g of groups)
    sgs.set(
      g.group.id,
      node({
        id: `sg:${g.group.id}`,
        kind: "security_group",
        title: g.group.title ?? g.group.key ?? g.group.id,
        subtitle: g.group.id,
        badge: "Group",
        lines: [{ text: "Made for the deployment" }],
      }),
    );
  /** A group by its NSX ID: one made for the deployment, or an existing one. */
  const groupNode = (id: string, name = id) => {
    const own = sgs.get(id);
    if (own) return own;
    let n = others.get(id);
    if (!n) {
      n = node({
        id: `nsx:${id}`,
        kind: "nsx_group",
        title: name,
        badge: "Group",
        lines: [{ text: "Existing, in NSX" }],
      });
      others.set(id, n);
    }
    return n;
  };

  const byComponent = vmsByComponent(d);
  const framed = byComponent.size > 1;
  const titled = titles(topology);
  /** What each component is drawn as (its node, or the frame around its VMs), and each VM's node. */
  const drawnAs = new Map<string, { id: string; title: string }>();
  const nodeOfVm = new Map<string, CanvasNode>();

  for (const [key, list] of byComponent) {
    const title = titled.get(key) ?? (key === "vm" ? d.name : key);
    if (list.length > ONE_BY_ONE) {
      const on = list.filter((v) => v.vm.power_state === "on").length;
      const sizes = new Set(list.map((v) => `${v.vm.cpu_count} vCPU · ${gb(v.vm.memory_mb)} GB`));
      const n = node({
        id: `component:${key}`,
        kind: "component",
        title,
        ...(key !== title ? { subtitle: key } : {}),
        badge: plural(list.length, "VM"),
        lines: [
          { label: "Names", text: `${list[0]!.name} to ${list[list.length - 1]!.name}`, mono: true },
          { label: "Power", text: `${on} of ${list.length} on` },
          { label: "Size", text: sizes.size === 1 ? [...sizes][0]! : "Not all the same" },
        ],
        problems: list.flatMap((v) => (syncProblem(v) ? [`${v.name}: ${syncProblem(v)}`] : [])),
      });
      computes.push(n);
      drawnAs.set(key, { id: n.id, title });
      for (const v of list) nodeOfVm.set(v.id, n);
      continue;
    }
    for (const v of list) {
      const problem = syncProblem(v);
      const n = node({
        id: vmNodeId(v.id),
        kind: "vm",
        title: v.name,
        lines: vmLines(v),
        problems: problem ? [problem] : [],
        ...(framed ? { group: `frame:${key}` } : {}),
      });
      if (framed) relate(n, `In ${title}`);
      computes.push(n);
      nodeOfVm.set(v.id, n);
    }
    if (framed) {
      frames.push({
        id: `frame:${key}`,
        label: `${title} · ${plural(list.length, "VM")}`,
        nodes: list.map((v) => vmNodeId(v.id)),
        problems: [],
      });
      drawnAs.set(key, { id: `frame:${key}`, title });
    }
  }

  for (const list of byComponent.values())
    for (const v of list) {
      const at = nodeOfVm.get(v.id)!;
      v.vm.nics.forEach((nic, i) => {
        const net = network(nic.network);
        const n = adapterNumber(nic.label, i);
        const on = `${net.kind === "segment" ? "segment" : "network"} ${net.title}${nic.dhcp ? ", by DHCP" : ""}`;
        edge({
          id: `nic:${at.id}:${n}:${nic.network}`,
          source: net.id,
          target: at.id,
          kind: "network",
          label: `Adapter ${n}${nic.dhcp ? " · DHCP" : ""}`,
          description: `${at.title}: adapter ${n} on ${on}`,
        });
        relate(at, `Adapter ${n} on ${on}`);
        relate(net, `${at.title}: adapter ${n}`);
      });
    }

  const vmByName = new Map([...byComponent.values()].flat().map((v) => [v.name, v]));
  for (const m of members) {
    const v = vmByName.get(m.membership.vm);
    const at = v && nodeOfVm.get(v.id);
    if (!at) continue;
    if (m.sync_state === "drifted") {
      const problem = "Its NSX tags changed outside rvav.";
      at.problems.push(at.kind === "vm" ? problem : `${m.membership.vm}: ${problem}`);
    }
    for (const g of m.membership.groups) {
      const into = groupNode(g.id, g.name);
      edge({
        id: `member:${at.id}:${into.id}`,
        source: at.id,
        target: into.id,
        kind: "member",
        description: `${at.title} in ${into.title}`,
      });
      relate(at, `In ${into.title}`);
      relate(into, `Holds ${at.title}`);
    }
  }

  const verbs: Record<string, string> = { ALLOW: "Allows", DROP: "Drops", REJECT: "Rejects" };
  let any: CanvasNode | undefined;
  const end = (id: string) =>
    id === "ANY"
      ? (any ??= node({ id: "any", kind: "any", title: "Any", lines: [{ text: "Anywhere" }] }))
      : groupNode(id);
  for (const p of policies)
    for (const r of p.rules) {
      const svc = r.services.length === 0 || r.services.includes("ANY") ? "any service" : r.services.join(", ");
      const verb = verbs[r.action] ?? r.action;
      for (const from of r.from.length > 0 ? r.from : ["ANY"])
        for (const to of r.to.length > 0 ? r.to : ["ANY"]) {
          if (from === "ANY" && to === "ANY") continue;
          const [source, target] = [end(from), end(to)];
          edge({
            id: `rule:${p.id}:${r.id}:${from}>${to}`,
            source: source.id,
            target: target.id,
            kind: "rule",
            label: r.action === "ALLOW" ? svc : `${svc} · ${r.action.toLowerCase()}`,
            description: `${verb} ${svc} from ${source.title} to ${target.title}`,
            ...(r.action !== "ALLOW" ? { deny: true } : {}),
          });
          relate(target, `${verb} ${svc} from ${source.title}`);
          relate(source, `${verb} ${svc} to ${target.title}`);
        }
    }

  // The order the components were built in, as the creating request's blueprint says (ADR-0024).
  for (const [key, after] of dependsOn(obj(topology))) {
    const to = drawnAs.get(key);
    if (!to) continue;
    const before = after.flatMap((k) => (drawnAs.has(k) ? [drawnAs.get(k)!] : []));
    if (before.length === 0) continue;
    for (const from of before)
      edge({
        id: `after:${from.id}>${to.id}`,
        source: from.id,
        target: to.id,
        kind: "sequence",
        description: `${to.title} built after ${from.title}`,
      });
    const said = `Built after ${words(before.map((b) => b.title))}`;
    for (const n of computes) if (n.id === to.id || n.group === to.id) relate(n, said);
  }

  const custom = new Map<string, CanvasNode>();
  for (const r of d.resources)
    if (r.type === "custom")
      custom.set(
        r.key,
        node({
          id: resourceNodeId(r.id),
          kind: "resource",
          title: r.name,
          subtitle: r.key,
          badge: r.resource_type.title,
          lines: resourceLines(r),
        }),
      );
  /** What a key is drawn as: a resource's node, a component's node or frame, or a small component's VMs. */
  const drawn = (k: string): { id: string; title: string }[] =>
    custom.has(k)
      ? [custom.get(k)!]
      : drawnAs.has(k)
        ? [drawnAs.get(k)!]
        : (byComponent.get(k) ?? []).flatMap((v) => (nodeOfVm.has(v.id) ? [nodeOfVm.get(v.id)!] : []));
  for (const [key, after] of resourcesAfter(topology)) {
    const to = custom.get(key);
    if (!to) continue;
    const before = [...new Map(after.flatMap(drawn).map((b) => [b.id, b])).values()];
    for (const from of before)
      edge({
        id: `after:${from.id}>${to.id}`,
        source: from.id,
        target: to.id,
        kind: "sequence",
        description: `${to.title} made after ${from.title}`,
      });
    if (before.length > 0) relate(to, `Made after ${words(before.map((b) => b.title))}`);
  }

  const nodes = [
    ...computes,
    ...custom.values(),
    ...nets.values(),
    ...sgs.values(),
    ...others.values(),
    ...(any ? [any] : []),
  ];
  return { kind: "vm", direction: "LR", nodes, edges, groups: frames, problems: [] };
}
