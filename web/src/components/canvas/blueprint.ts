// A blueprint as the canvas draws it (ADR-0032). The YAML is the source of
// truth: these read the definition's blueprint (parsed) and give the graph,
// nothing more; the canvas keeps no positions of its own.
//
// - A composite (ADR-0022), top to bottom: the request's form, each step
//   in order, a parallel group as a frame around its steps, then Done. A
//   sequence edge goes from one step (or frame) to the next; a step's
//   `when` dashes the edge into it and shows on the step; its `undo` and
//   the earlier steps it binds to show on it. A branch (ADR-0033) leads to
//   each of its sides, a chain of steps, by a dashed edge labelled with the
//   side; each side's last step leads on to what comes next, and a side
//   with no steps goes there straight.
// - A VM item (ADR-0024, ADR-0028), left to right: the networks and NSX
//   segments, the components with their counts and disks, then the groups
//   their VMs are in, the security groups' firewall rules arriving at them
//   from Any, another group or an existing one.
import { allSteps, readComposite, type CompositeStepView } from "@/lib/composite";
import type { FieldError } from "@/lib/api/types";
import type { CanvasEdge, CanvasGraph, CanvasGroup, CanvasLine, CanvasNode, CanvasSide } from "./graph";
import { within } from "./graph";

type Raw = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const strings = (v: unknown): string[] =>
  Array.isArray(v) ? v.map((x) => (typeof x === "string" ? x : JSON.stringify(x))) : str(v) ? [str(v)] : [];

/** "a", "a and b", "a, b and c". */
export function words(names: string[]): string {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** The graph of a definition's blueprint, or null for kinds drawn as no graph (AWX and workflow items). */
export function blueprintGraph(blueprint: unknown, prefix = "/definition/blueprint"): CanvasGraph | null {
  const bp = obj(blueprint);
  if (bp.kind === "composite") return compositeGraph(bp, prefix);
  if (bp.kind === "vm") return vmGraph(bp, prefix);
  return null;
}

// ---- Composites ----------------------------------------------------------

const stepBadge: Record<CompositeStepView["kind"], string> = {
  item: "Item",
  awx: "AWX",
  workflow: "Workflow",
  rest: "REST",
  ask: "Person",
  wait: "Wait",
  poll: "Poll",
  branch: "Branch",
  notify: "Message",
  "?": "Step",
};

function stepLines(s: CompositeStepView): CanvasLine[] {
  const lines: CanvasLine[] = [];
  if (s.target)
    lines.push({ label: s.kind === "rest" || s.kind === "poll" ? "Calls" : "Runs", text: s.target, mono: true });
  if (s.ask) {
    lines.push({ label: "Asks", text: s.ask.who }, { label: "Wants", text: s.ask.wants });
    if (s.ask.timeout) lines.push({ label: "Timeout", text: s.ask.timeout });
  }
  if (s.wait?.for) lines.push({ label: "For", text: s.wait.for });
  if (s.wait?.until) lines.push({ label: "Until", text: s.wait.until, mono: true });
  if (s.poll) {
    if (s.poll.until) lines.push({ label: "Until", text: s.poll.until, mono: true });
    if (s.poll.every) lines.push({ label: "Every", text: s.poll.every });
    if (s.poll.timeout) lines.push({ label: "Timeout", text: s.poll.timeout });
  }
  for (const side of s.sides ?? [])
    lines.push(
      side.when === undefined
        ? {
            label: side.label,
            text:
              side.steps.length === 0 ? "goes on" : side.steps.length === 1 ? "1 step" : `${side.steps.length} steps`,
          }
        : { label: side.label, text: side.when, mono: true },
    );
  if (s.notify) {
    lines.push({ label: "Tells", text: s.notify.who });
    if (s.notify.message) lines.push({ label: "Says", text: s.notify.message });
  }
  if (s.when) lines.push({ label: "When", text: s.when, mono: true });
  if (s.uses.length > 0) lines.push({ label: "Uses", text: s.uses.join(", "), mono: true });
  if (s.undo) lines.push({ label: "Undo", text: s.undo, mono: true });
  return lines;
}

/** Where the way goes on from: a step or a frame, with the side of a branch it is on. */
interface Tail {
  id: string;
  names: string[];
  /** Said on the edge from here: the side a branch's edge leads to. */
  label?: string;
  branch?: CanvasEdge["branch"];
}

// A side's title is what its author wrote: the card holds it in full, an edge doesn't have the room.
const labelOf = (s: string) => (s.length > 24 ? `${s.slice(0, 23)}…` : s);

/** A composite's steps: `step:<key>` for each (by pointer when its key is missing or taken twice). */
export function compositeGraph(bp: Raw, prefix = "/definition/blueprint"): CanvasGraph {
  const read = readComposite(bp, prefix);
  // A requester's topology is masked, so the checks read holes (a REST call's
  // URL, who a wait asks) as mistakes. What was published was checked in full.
  const view = prefix === "/topology" ? { ...read, problems: [] } : read;
  const keys = new Map<string, number>();
  for (const s of allSteps(view)) if (s.key) keys.set(s.key, (keys.get(s.key) ?? 0) + 1);
  const stepId = (s: CompositeStepView) => (s.key && keys.get(s.key) === 1 ? `step:${s.key}` : `step@${s.pointer}`);
  const titleOf = (s: CompositeStepView) => s.title || s.key || "(no key)";
  const claimed = new Set<FieldError>();
  const problemsIn = (pointer: string) =>
    view.problems
      .filter((p) => !claimed.has(p) && within(p.field, pointer))
      .map((p) => {
        claimed.add(p);
        return p.message;
      });
  const stepNode = (s: CompositeStepView, relations: string[], extra: Partial<CanvasNode> = {}): CanvasNode => ({
    id: stepId(s),
    kind: "step",
    title: titleOf(s),
    subtitle: s.key && s.key !== titleOf(s) ? s.key : undefined,
    badge: stepBadge[s.kind],
    lines: stepLines(s),
    relations,
    problems: problemsIn(s.pointer),
    pointer: s.pointer,
    ...extra,
  });

  const nodes: CanvasNode[] = [
    {
      id: "inputs",
      kind: "inputs",
      title: "Request",
      lines: [{ text: "The form's values, as inputs" }],
      relations: [],
      problems: [],
    },
  ];
  const groups: CanvasGroup[] = [];
  const edges: CanvasEdge[] = [];
  // Where the way goes on from: one step or frame, or each side of a branch.
  let tails: Tail[] = [{ id: "inputs", names: ["the request"] }];
  const follow = (target: string, names: string[], when?: string) => {
    for (const t of tails) {
      const label = [t.label && labelOf(t.label), when ? "when" : ""].filter(Boolean).join(", ");
      edges.push({
        id: `seq:${t.id}>${target}`,
        source: t.id,
        target,
        kind: "sequence",
        label: label || undefined,
        description: capital(
          `${words(t.names)}${t.label ? ` (${t.label})` : ""}, then ${words(names)}${when ? ` when ${when}` : ""}`,
        ),
        conditional: Boolean(when) || Boolean(t.branch) || undefined,
        ...(t.branch ? { branch: t.branch } : {}),
      });
    }
  };
  const after = () => tails.flatMap((t) => t.names);
  const isFirst = () => tails.length === 1 && tails[0]!.id === "inputs";

  view.groups.forEach((group, g) => {
    if (group.length === 0) return;
    const at = `${prefix}/steps/${g}`;
    const ids = group.map(stepId);
    const titles = group.map(titleOf);
    // A `parallel:` list is a frame, even of one step (the designer adds the next).
    const parallel = Array.isArray(obj(arr(bp.steps)[g]).parallel);
    const slot = parallel ? `parallel:${g}` : ids[0]!;
    const runsAfter = isFirst() ? "Runs first" : `Runs after ${words(after())}`;
    const when = !parallel ? group[0]!.when : undefined;
    follow(slot, titles, when);
    const branch = !parallel ? group[0]!.sides : undefined;
    if (branch) {
      // Its sides' problems are their steps': read before its own, which hold them by pointer.
      const onSides: CanvasNode[] = [];
      const sides: CanvasSide[] = [];
      const next: Tail[] = [];
      const straight: { label: string; taken: number }[] = [];
      for (const side of branch) {
        const way = { node: slot, cases: [side.taken] };
        let tail: Tail = { id: slot, names: titles, label: side.label, branch: way };
        const before = [`On the side ${side.label} of ${titles[0]!}`];
        side.steps.forEach((s, i) => {
          tails = [tail];
          follow(stepId(s), [titleOf(s)], s.when);
          onSides.push(
            stepNode(s, i === 0 ? before : [...before, `Runs after ${titleOf(side.steps[i - 1]!)}`], {
              onSideOf: slot,
            }),
          );
          tail = { id: stepId(s), names: [titleOf(s)], branch: way };
        });
        sides.push({ label: side.label, when: side.when, taken: side.taken, nodes: side.steps.map(stepId) });
        if (side.steps.length === 0) straight.push(side);
        else next.push(tail);
      }
      if (straight.length > 0)
        next.push({
          id: slot,
          names: titles,
          label: straight.map((s) => s.label).join(", "),
          branch: { node: slot, cases: straight.map((s) => s.taken) },
        });
      nodes.push(stepNode(group[0]!, [runsAfter], { sides }), ...onSides);
      tails = next;
      return;
    }
    nodes.push(
      ...group.map((s, j) => {
        const others = titles.filter((_, k) => k !== j);
        const relations = [runsAfter];
        if (others.length > 0) relations.push(`At the same time as ${words(others)}`);
        return stepNode(s, relations, { group: parallel ? slot : undefined });
      }),
    );
    if (parallel)
      groups.push({ id: slot, label: "At the same time", nodes: ids, problems: problemsIn(at), pointer: at });
    tails = [{ id: slot, names: titles }];
  });

  nodes.push({
    id: "end",
    kind: "end",
    title: "Done",
    lines: [
      { label: "On a failure", text: bp.rollback_on_failure === true ? "undo what was built" : "keep what was built" },
    ],
    relations: isFirst() ? ["No step yet"] : [`After ${words(after())}`],
    problems: [],
  });
  follow("end", ["done"]);
  return { kind: "composite", direction: "TB", nodes, edges, groups, problems: view.problems };
}

// ---- VM items --------------------------------------------------------------

interface Builder {
  node(n: Omit<CanvasNode, "relations" | "problems"> & { problems?: string[] }, bucket: Bucket): CanvasNode;
}

type Bucket = "component" | "network" | "segment" | "group" | "other" | "any";
const bucketOrder: Bucket[] = ["component", "network", "segment", "group", "other", "any"];

const segmentRef = /^segments\.([a-z][a-z0-9_]*)$/;
const quoted = /^"([^"]*)"$|^'([^']*)'$/;

/** The keys of a post-configuration binding: a CEL list of quoted keys, or a list. */
function listed(v: unknown): string[] | null {
  if (Array.isArray(v)) return v.map(String);
  const s = str(v).trim();
  if (!/^\[.*\]$/s.test(s)) return null;
  return [...s.matchAll(/"([^"]+)"|'([^']+)'/g)].map((m) => m[1] ?? m[2]!);
}

function sizeText(v: unknown): string {
  if (typeof v === "number") return `${v} GB`;
  return str(v) ? `${str(v)} GB` : "? GB";
}

/** A VM item's topology: `component:<key>`, `network:<binding>`, `segment:<key>`, `sg:<key>`, `nsx:<id>`, `any`. */
export function vmGraph(bp: Raw, prefix = "/definition/blueprint"): CanvasGraph {
  const buckets = new Map<Bucket, Map<string, CanvasNode>>(bucketOrder.map((b) => [b, new Map()]));
  const edges: CanvasEdge[] = [];
  const problems: FieldError[] = [];
  const b: Builder = {
    node(n, bucket) {
      for (const m of buckets.values()) {
        const found = m.get(n.id);
        if (found) return found;
      }
      const made: CanvasNode = { ...n, relations: [], problems: n.problems ?? [] };
      buckets.get(bucket)!.set(n.id, made);
      return made;
    },
  };
  const relate = (n: CanvasNode, text: string) => {
    if (!n.relations.includes(text)) n.relations.push(text);
  };
  const problem = (n: CanvasNode, field: string, code: string, message: string) => {
    n.problems.push(message);
    problems.push({ field, code, message });
  };

  arr(bp.segments).forEach((raw, i) => {
    const s = obj(raw);
    const key = str(s.key);
    b.node(
      {
        id: `segment:${key}`,
        kind: "segment",
        title: str(s.title) || key,
        subtitle: key,
        badge: "Segment",
        lines: [{ text: "Made for each deployment" }],
        pointer: `${prefix}/segments/${i}`,
      },
      "segment",
    );
  });
  const securityGroups = arr(bp.security_groups).map(obj);
  securityGroups.forEach((sg, i) => {
    const key = str(sg.key);
    b.node(
      {
        id: `sg:${key}`,
        kind: "security_group",
        title: str(sg.title) || key,
        subtitle: key,
        badge: "Group",
        lines: [{ text: "Made for each deployment" }],
        pointer: `${prefix}/security_groups/${i}`,
      },
      "group",
    );
  });

  const unknownGroup = (key: string, field: string) => {
    const n = b.node(
      { id: `sg:${key}`, kind: "security_group", title: key, subtitle: key, badge: "Group", lines: [] },
      "group",
    );
    if (n.problems.length === 0) problem(n, field, "unknown_group", `No security group has the key ${key}.`);
    return n;
  };
  const existingGroup = (ref: string) =>
    b.node(
      {
        id: ref,
        kind: "nsx_group",
        title: ref.slice("nsx:".length),
        badge: "Group",
        lines: [{ text: "Existing, in NSX" }],
      },
      "other",
    );
  const groupKeys = new Set(securityGroups.map((sg) => str(sg.key)));
  /** A group a component is in, or a rule comes from: a key of security_groups or nsx:<ID>. */
  const groupRef = (ref: string, field: string) => {
    if (ref.startsWith("nsx:")) return existingGroup(ref);
    if (groupKeys.has(ref)) return buckets.get("group")!.get(`sg:${ref}`)!;
    return unknownGroup(ref, field);
  };

  /** What an adapter's network binding names. */
  const network = (expr: unknown, field: string): CanvasNode => {
    const e = str(expr).trim();
    if (!e)
      return b.node(
        {
          id: "network:auto",
          kind: "network",
          title: "Chosen by placement",
          badge: "Network",
          lines: [],
        },
        "network",
      );
    const seg = segmentRef.exec(e);
    if (seg) {
      const key = seg[1]!;
      const n = b.node(
        { id: `segment:${key}`, kind: "segment", title: key, subtitle: key, badge: "Segment", lines: [] },
        "segment",
      );
      if (!n.pointer && n.problems.length === 0) problem(n, field, "unknown_segment", `No segment has the key ${key}.`);
      return n;
    }
    const lit = quoted.exec(e);
    if (lit) {
      const name = lit[1] ?? lit[2] ?? "";
      return b.node(
        { id: `network:${e}`, kind: "network", title: name, badge: "Network", lines: [{ text: "Named in the item" }] },
        "network",
      );
    }
    return b.node(
      {
        id: `network:${e}`,
        kind: "network",
        title: "From the request",
        subtitle: e,
        badge: "Network",
        lines: [],
      },
      "network",
    );
  };

  const declared = Array.isArray(bp.components) && bp.components.length > 0;
  const components = declared
    ? arr(bp.components).map((c, i) => ({ c: obj(c), pointer: `${prefix}/components/${i}` }))
    : [{ c: {} as Raw, pointer: prefix }];
  const awx = obj(bp.awx_templates);
  const bpGuest = obj(bp.guest);

  for (const { c, pointer } of components) {
    const key = declared ? str(c.key) : "vm";
    const bindings = { ...obj(bp.bindings), ...obj(c.bindings) };
    const title = declared ? str(c.title) || key : "Virtual machine";
    const lines: CanvasLine[] = [];
    let badge = "VM";
    if (declared) {
      const count = c.count ?? 1;
      if (typeof count === "number") badge = count === 1 ? "1 VM" : `${count} VMs`;
      else {
        badge = "VMs";
        lines.push({ label: "Count", text: str(count), mono: true });
      }
    }
    const template = str(c.template) || str(bp.template) || str(bindings.template);
    const library = str(c.library) || str(bp.library);
    const os = str(obj(c.guest).os_family) || str(bpGuest.os_family);
    if (template)
      lines.push({
        label: "Template",
        text: [library ? `${library}/${template}` : template, os].filter(Boolean).join(" · "),
        mono: true,
      });
    const disks = arr(c.disks ?? bp.disks).map(obj);
    if (disks.length > 0)
      lines.push({
        label: "Disks",
        text: [
          "system",
          ...disks.map((d) => sizeText(d.size_gb) + (str(d.storage_policy) ? ` (${str(d.storage_policy)})` : "")),
        ].join(", "),
      });
    if (c.anti_affinity === true) lines.push({ text: "On separate hosts" });
    const post = bindings.post_config;
    if (post !== undefined) {
      const keys = listed(post);
      if (keys)
        lines.push({ label: "Then", text: keys.map((k) => str(obj(awx[k]).title) || k).join(", ") || "nothing" });
      else lines.push({ label: "Then", text: str(post), mono: true });
    }
    const comp = b.node(
      {
        id: `component:${key || pointer}`,
        kind: "component",
        title,
        subtitle: declared && key !== title ? key : undefined,
        badge,
        lines,
        pointer,
      },
      "component",
    );

    const nics = arr(c.nics ?? bp.nics).map(obj);
    const adapters = [
      {
        key: "nic1",
        network: bindings.network,
        dhcp: false,
        field: `${obj(c.bindings).network !== undefined ? pointer : prefix}/bindings/network`,
      },
      ...nics.map((n, i) => ({
        key: str(n.key) || `nic${i + 2}`,
        network: n.network,
        dhcp: str(n.ipam) === "dhcp",
        field: `${pointer}/nics/${i}/network`,
      })),
    ];
    for (const a of adapters) {
      const net = network(a.network, a.field);
      const on =
        net.id === "network:auto"
          ? "the network placement chooses"
          : net.kind === "network" && net.subtitle
            ? `the request's network (${net.subtitle})`
            : net.title;
      edges.push({
        id: `nic:${comp.id}:${a.key}`,
        source: net.id,
        target: comp.id,
        kind: "network",
        label: a.dhcp ? `${a.key} · DHCP` : a.key,
        description: `${title}: adapter ${a.key} on ${on}${a.dhcp ? ", by DHCP" : ""}`,
      });
      relate(comp, `Adapter ${a.key} on ${on}${a.dhcp ? ", by DHCP" : ""}`);
      relate(net, `${title}: adapter ${a.key}`);
    }

    const memberOf = strings(c.groups ?? bp.groups).map((ref, i) => ({
      ref,
      field: `${c.groups ? pointer : prefix}/groups/${i}`,
    }));
    const chosen = str(bindings.groups);
    const targets = memberOf.map(({ ref, field }) => groupRef(ref, field));
    if (chosen)
      targets.push(
        b.node(
          {
            id: `groups:${chosen}`,
            kind: "nsx_group",
            title: "Groups chosen in the request",
            subtitle: chosen,
            badge: "Group",
            lines: [],
          },
          "other",
        ),
      );
    for (const g of targets) {
      edges.push({
        id: `member:${comp.id}:${g.id}`,
        source: comp.id,
        target: g.id,
        kind: "member",
        description: `${title} in ${g.title}`,
      });
      relate(comp, `In ${g.title}`);
      relate(g, `Holds ${title}`);
    }
  }

  const verbs: Record<string, string> = { allow: "Allows", drop: "Drops", reject: "Rejects" };
  securityGroups.forEach((sg, i) => {
    const into = buckets.get("group")!.get(`sg:${str(sg.key)}`)!;
    const key = str(sg.key);
    arr(sg.rules).forEach((raw, k) => {
      const r = obj(raw);
      const from = strings(r.from);
      const services = strings(r.services);
      const action = str(r.action) || "allow";
      const svc = services.length === 0 || services.every((s) => s === "any") ? "any service" : services.join(", ");
      const verb = verbs[action] ?? action;
      (from.length > 0 ? from : ["any"]).forEach((f, m) => {
        const field = `${prefix}/security_groups/${i}/rules/${k}/from/${m}`;
        const source =
          f === "any"
            ? b.node({ id: "any", kind: "any", title: "Any", lines: [{ text: "Any source" }] }, "any")
            : groupRef(f, field);
        edges.push({
          id: `rule:${key}:${k}:${m}`,
          source: source.id,
          target: into.id,
          kind: "rule",
          label: action === "allow" ? svc : `${svc} · ${action}`,
          description: `${verb} ${svc} from ${source.title} to ${into.title}`,
          deny: action !== "allow",
        });
        relate(into, `${verb} ${svc} from ${source.title}`);
        relate(source, `Rule into ${into.title}: ${verb.toLowerCase()} ${svc}`);
      });
    });
  });

  const nodes = bucketOrder.flatMap((k) => [...buckets.get(k)!.values()]);
  return { kind: "vm", direction: "LR", nodes, edges, groups: [], problems };
}
