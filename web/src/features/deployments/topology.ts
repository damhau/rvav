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
import { formatSize } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { componentTitles } from "./componentTitles";
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

// In the language shown when called: the tab draws the graph again when it changes.
const t = i18n.getFixedT(null, "deployments");

const size = (cpu: number, mb: number) =>
  t("topology.sizeValue", { cpu, memory: formatSize(Math.round(mb / 1024), "GB") });

function vmLines(v: VmResource): CanvasLine[] {
  const { vm } = v;
  const ip = vm.nics.flatMap((n) => n.ip_addresses ?? []).find((a) => !a.includes(":"));
  return [
    { label: t("topology.line.power"), text: t(`vmPower.${vm.power_state}`) },
    { label: t("topology.line.address"), text: ip ?? t("noneReported"), mono: Boolean(ip) },
    { label: t("topology.line.size"), text: size(vm.cpu_count, vm.memory_mb) },
    {
      label: t("topology.line.disks"),
      text: vm.disks.map((d) => formatSize(d.capacity_gb, "GB")).join(", ") || t("none"),
    },
  ];
}

const syncProblem = (v: VmResource) =>
  v.sync_state === "drifted" ? t("topology.drifted") : v.sync_state === "missing" ? t("topology.missing") : undefined;

const verb = (action: string) =>
  action === "ALLOW" ? "allow" : action === "DROP" ? "drop" : action === "REJECT" ? "reject" : "other";

/** An adapter's number, as vCenter labels it: "Network adapter 2". */
const adapterNumber = (label: string | undefined, index: number) =>
  /(\d+)$/.exec(label ?? "")?.[1] ?? String(index + 1);

const resourceLines = (r: CustomResource): CanvasLine[] => labelledProperties(r).slice(0, SHOWN_PROPERTIES);

const readsOf = (text: string, root: "components" | "resources") =>
  [...celCode(text).matchAll(new RegExp(`(?<![\\w.])${root}\\.([a-z][a-z0-9-]*)`, "g"))].map((m) => m[1]!);

/**
 * The components and resources each custom resource of the request's
 * topology is made after: those its inputs read as components.<key> or
 * resources.<key>, all the topology keeps of its `with`.
 */
function resourcesAfter(topology: unknown): Map<string, string[]> {
  const out = new Map<string, string[]>();
  const resources = obj(topology).resources;
  if (!Array.isArray(resources)) return out;
  for (const r of resources.map(obj)) {
    const key = str(r.key);
    if (!key) continue;
    const texts = Object.values(obj(r.with)).filter((v): v is string => typeof v === "string");
    const read = texts.flatMap((x) => [...readsOf(x, "components"), ...readsOf(x, "resources")]);
    out.set(
      key,
      [...new Set(read)].filter((k) => k && k !== key),
    );
  }
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
        badge: t("topology.badge.segment"),
        lines: [
          { label: t("topology.line.subnet"), text: s.segment.subnet, mono: true },
          { label: t("topology.line.tier1"), text: s.segment.tier1, mono: true },
        ],
      }),
    );
  const network = (name: string) => {
    let n = nets.get(name);
    if (!n) {
      n = node({ id: `network:${name}`, kind: "network", title: name, badge: t("topology.badge.network"), lines: [] });
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
        badge: t("topology.badge.group"),
        lines: [{ text: t("topology.madeFor") }],
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
        badge: t("topology.badge.group"),
        lines: [{ text: t("topology.existing") }],
      });
      others.set(id, n);
    }
    return n;
  };

  const byComponent = vmsByComponent(d);
  const framed = byComponent.size > 1;
  const titled = componentTitles(topology);
  /** What each component is drawn as (its node, or the frame around its VMs), and each VM's node. */
  const drawnAs = new Map<string, { id: string; title: string }>();
  const nodeOfVm = new Map<string, CanvasNode>();

  for (const [key, list] of byComponent) {
    const title = titled.get(key) ?? (key === "vm" ? d.name : key);
    if (list.length > ONE_BY_ONE) {
      const on = list.filter((v) => v.vm.power_state === "on").length;
      const sizes = new Set(list.map((v) => size(v.vm.cpu_count, v.vm.memory_mb)));
      const n = node({
        id: `component:${key}`,
        kind: "component",
        title,
        ...(key !== title ? { subtitle: key } : {}),
        badge: t("topology.badge.vms", { count: list.length }),
        lines: [
          {
            label: t("topology.line.names"),
            text: t("topology.namesRange", { first: list[0]!.name, last: list[list.length - 1]!.name }),
            mono: true,
          },
          { label: t("topology.line.power"), text: t("onOf", { count: on, total: list.length }) },
          { label: t("topology.line.size"), text: sizes.size === 1 ? [...sizes][0]! : t("topology.mixedSizes") },
        ],
        problems: list.flatMap((v) => {
          const problem = syncProblem(v);
          return problem ? [t("topology.vmProblem", { name: v.name, problem })] : [];
        }),
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
      if (framed) relate(n, t("topology.in", { group: title }));
      computes.push(n);
      nodeOfVm.set(v.id, n);
    }
    if (framed) {
      frames.push({
        id: `frame:${key}`,
        label: t("vms.group", { component: title, count: list.length }),
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
        const on = t(net.kind === "segment" ? "topology.onSegment" : "topology.onNetwork", { name: net.title });
        const dhcp = Boolean(nic.dhcp);
        edge({
          id: `nic:${at.id}:${n}:${nic.network}`,
          source: net.id,
          target: at.id,
          kind: "network",
          label: t(dhcp ? "topology.adapterLabelDhcp" : "topology.adapterLabel", { n }),
          description: t(dhcp ? "topology.adapterEdgeDhcp" : "topology.adapterEdge", {
            title: at.title,
            n,
            network: on,
          }),
        });
        relate(at, t(dhcp ? "topology.adapterDhcp" : "topology.adapter", { n, network: on }));
        relate(net, t("topology.adapterOf", { title: at.title, n }));
      });
    }

  const vmByName = new Map([...byComponent.values()].flat().map((v) => [v.name, v]));
  for (const m of members) {
    const v = vmByName.get(m.membership.vm);
    const at = v && nodeOfVm.get(v.id);
    if (!at) continue;
    if (m.sync_state === "drifted") {
      const problem = t("topology.tagsDrifted");
      at.problems.push(at.kind === "vm" ? problem : t("topology.vmProblem", { name: m.membership.vm, problem }));
    }
    for (const g of m.membership.groups) {
      const into = groupNode(g.id, g.name);
      edge({
        id: `member:${at.id}:${into.id}`,
        source: at.id,
        target: into.id,
        kind: "member",
        description: t("topology.memberEdge", { title: at.title, group: into.title }),
      });
      relate(at, t("topology.in", { group: into.title }));
      relate(into, t("topology.holds", { title: at.title }));
    }
  }

  let any: CanvasNode | undefined;
  const end = (id: string) =>
    id === "ANY"
      ? (any ??= node({
          id: "any",
          kind: "any",
          title: t("topology.any"),
          lines: [{ text: t("topology.anywhere") }],
        }))
      : groupNode(id);
  for (const p of policies)
    for (const r of p.rules) {
      const services =
        r.services.length === 0 || r.services.includes("ANY") ? t("security.anyService") : r.services.join(", ");
      const kind = verb(r.action);
      const action = r.action.toLowerCase();
      for (const from of r.from.length > 0 ? r.from : ["ANY"])
        for (const to of r.to.length > 0 ? r.to : ["ANY"]) {
          if (from === "ANY" && to === "ANY") continue;
          const [source, target] = [end(from), end(to)];
          edge({
            id: `rule:${p.id}:${r.id}:${from}>${to}`,
            source: source.id,
            target: target.id,
            kind: "rule",
            label: kind === "allow" ? services : t(`topology.ruleLabel.${kind}`, { services, action }),
            description: t(`topology.rule.${kind}.edge`, {
              services,
              action: r.action,
              from: source.title,
              to: target.title,
            }),
            ...(r.action !== "ALLOW" ? { deny: true } : {}),
          });
          relate(target, t(`topology.rule.${kind}.into`, { services, action: r.action, from: source.title }));
          relate(source, t(`topology.rule.${kind}.out`, { services, action: r.action, to: target.title }));
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
        description: t("topology.builtAfterEdge", { to: to.title, from: from.title }),
      });
    const said = t("topology.builtAfter", { names: words(before.map((b) => b.title)) });
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
        description: t("topology.madeAfterEdge", { title: to.title, from: from.title }),
      });
    if (before.length > 0) relate(to, t("topology.madeAfter", { names: words(before.map((b) => b.title)) }));
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
