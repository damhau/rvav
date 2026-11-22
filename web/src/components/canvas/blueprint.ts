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
//   with no steps goes there straight. A catch path (ADR-0057) follows
//   what it catches by a dashed edge, "if it fails", and leads on to what
//   comes next too; the error handler follows Done the same way.
// - A VM item (ADR-0024, ADR-0028), left to right: the networks and NSX
//   segments, the components with their counts and disks (an edge into one
//   from each it is built after), then the groups their VMs are in, the
//   security groups' firewall rules arriving at them from Any, another
//   group or an existing one. For the VM designer (designed), each disk
//   is a node of its own, attached to the components whose VMs have it.
import { formatList, formatNumber, formatSize } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import {
  allSteps,
  readComposite,
  type CompositeAudience,
  type CompositeSideView,
  type CompositeStepView,
  type OnTimeout,
} from "@/lib/composite";
import { celCode, dependsOn } from "@/lib/dependsOn";
import type { FieldError } from "@/lib/api/types";
import type { CanvasEdge, CanvasGraph, CanvasGroup, CanvasLine, CanvasNode, CanvasSide } from "./graph";
import { within } from "./graph";

type Raw = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const arr = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const strings = (v: unknown): string[] =>
  Array.isArray(v) ? v.map((x) => (typeof x === "string" ? x : JSON.stringify(x))) : str(v) ? [str(v)] : [];

/** "a", "a and b", "a, b and c", in the language shown. */
export function words(names: string[]): string {
  return formatList(names);
}

// In the language shown when called: a graph is drawn again when it changes.
const t = i18n.getFixedT(null, "canvas");

const capital = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

export interface GraphOptions {
  /** Drawn for the VM designer: each disk is a node (disk:<component>:<index>, or disk@<index> for the blueprint's, which components without their own take). */
  designed?: boolean;
}

/** The graph of a definition's blueprint, or null for kinds drawn as no graph (AWX and workflow items). */
export function blueprintGraph(
  blueprint: unknown,
  prefix = "/definition/blueprint",
  options: GraphOptions = {},
): CanvasGraph | null {
  const bp = obj(blueprint);
  if (bp.kind === "composite") return compositeGraph(bp, prefix);
  if (bp.kind === "vm") return vmGraph(bp, prefix, options);
  return null;
}

// ---- Composites ----------------------------------------------------------

const stepBadge = (kind: CompositeStepView["kind"]) => i18n.t(`canvas:badge.${kind === "?" ? "step" : kind}`);

const timeoutLine = (timeout: string, then: OnTimeout) => t(`timeoutThen.${then}`, { timeout });

/** Whom a step asks or tells, in the order the server lists them. */
function audience(a: CompositeAudience): string {
  return [
    a.requester ? t("who.requester") : "",
    a.owner ? t("who.owner") : "",
    ...a.groups,
    a.people > 0 ? t("who.people", { count: a.people }) : "",
  ]
    .filter(Boolean)
    .join(", ");
}

/** A side's title as written, else "Case 2" or "Otherwise". */
const sideLabel = (s: CompositeSideView) =>
  s.title ?? (s.taken === 0 ? t("composite.otherwise") : t("composite.case", { number: s.taken }));

function stepLines(s: CompositeStepView): CanvasLine[] {
  const lines: CanvasLine[] = [];
  if (s.target)
    lines.push({
      label: t(
        s.kind === "rest" || s.kind === "poll" ? "lines.calls" : s.kind === "resource" ? "lines.makes" : "lines.runs",
      ),
      text: s.target,
      mono: true,
    });
  if (s.ask) {
    lines.push(
      { label: t("lines.asks"), text: audience(s.ask) || t("who.anyone") },
      {
        label: t("lines.wants"),
        text: s.ask.questions === 0 ? t("wants.decision") : t("wants.answers", { count: s.ask.questions }),
      },
    );
    if (s.ask.timeout) lines.push({ label: t("lines.timeout"), text: timeoutLine(s.ask.timeout, s.ask.onTimeout) });
  }
  if (s.forEach) {
    lines.push({ label: t("lines.forEach"), text: s.forEach.items, mono: true });
    if (s.forEach.parallel > 1) lines.push({ label: t("lines.atATime"), text: formatNumber(s.forEach.parallel) });
  }
  if (s.signal) {
    lines.push({
      label: t("lines.waitsFor"),
      text:
        s.signal.event !== undefined
          ? t("waitsFor.event", { name: s.signal.event })
          : t("waitsFor.signal", { name: s.signal.name ?? "" }),
    });
    if (s.signal.when) lines.push({ label: t("lines.picks"), text: s.signal.when, mono: true });
    if (s.signal.timeout)
      lines.push({ label: t("lines.timeout"), text: timeoutLine(s.signal.timeout, s.signal.onTimeout) });
  }
  if (s.wait?.for) lines.push({ label: t("lines.for"), text: s.wait.for });
  if (s.wait?.until) lines.push({ label: t("lines.until"), text: s.wait.until, mono: true });
  if (s.poll) {
    if (s.poll.until) lines.push({ label: t("lines.until"), text: s.poll.until, mono: true });
    if (s.poll.every) lines.push({ label: t("lines.every"), text: s.poll.every });
    if (s.poll.timeout) lines.push({ label: t("lines.timeout"), text: timeoutLine(s.poll.timeout, s.poll.onTimeout) });
  }
  for (const side of s.sides ?? [])
    lines.push(
      side.when === undefined
        ? {
            label: sideLabel(side),
            text: side.steps.length === 0 ? t("composite.goesOn") : t("composite.steps", { count: side.steps.length }),
          }
        : { label: sideLabel(side), text: side.when, mono: true },
    );
  if (s.notify) {
    lines.push({ label: t("lines.tells"), text: audience(s.notify) || t("who.others") });
    if (s.notify.message) lines.push({ label: t("lines.says"), text: s.notify.message });
  }
  if (s.when) lines.push({ label: t("lines.when"), text: s.when, mono: true });
  if (s.uses.length > 0) lines.push({ label: t("lines.uses"), text: s.uses.join(", "), mono: true });
  if (s.undo) lines.push({ label: t("lines.undo"), text: s.undo, mono: true });
  return lines;
}

/** Where the way goes on from: a step or a frame, with the side of a branch it is on. */
interface Tail {
  id: string;
  names: string[];
  /** Said on the edge from here: the side a branch's edge leads to, or "if it fails". */
  label?: string;
  branch?: CanvasEdge["branch"];
  /** The way from here is taken only on a failure: into a catch path or the error handler. */
  failure?: boolean;
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
  const titleOf = (s: CompositeStepView) => s.title || s.key || t("composite.noKey");
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
    badge: stepBadge(s.kind),
    stepKind: s.kind,
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
      title: t("composite.request"),
      lines: [{ text: t("composite.requestLine") }],
      relations: [],
      problems: [],
    },
  ];
  const groups: CanvasGroup[] = [];
  const edges: CanvasEdge[] = [];
  // Where the way goes on from: one step or frame, or each side of a branch.
  let tails: Tail[] = [{ id: "inputs", names: [t("composite.theRequest")] }];
  const follow = (target: string, names: string[], when?: string) => {
    for (const tail of tails) {
      const label = [tail.label && labelOf(tail.label), when ? t("composite.when") : ""].filter(Boolean).join(", ");
      const from = `${words(tail.names)}${tail.label ? ` (${tail.label})` : ""}`;
      edges.push({
        id: `seq:${tail.id}>${target}`,
        source: tail.id,
        target,
        kind: "sequence",
        label: label || undefined,
        description: capital(
          when
            ? t("composite.edgeWhen", { from, to: words(names), when })
            : t("composite.edge", { from, to: words(names) }),
        ),
        conditional: Boolean(when) || Boolean(tail.branch) || tail.failure || undefined,
        ...(tail.branch ? { branch: tail.branch } : {}),
      });
    }
  };
  const after = () => tails.flatMap((x) => x.names);
  const isFirst = () => tails.length === 1 && tails[0]!.id === "inputs";
  /**
   * A catch path's nodes after what it catches, from: each follows the one
   * before it, the first by a dashed edge. Built before the node or frame it
   * catches, whose problems would take theirs by pointer. Gives the way on
   * from its last step.
   */
  const catchNodes = (
    caught: CompositeStepView[],
    from: Tail,
    catches: string,
    extra: Partial<CanvasNode> = {},
  ): { nodes: CanvasNode[]; tail: Tail } => {
    let tail: Tail = { ...from, label: t("composite.ifItFails"), failure: true };
    const out = caught.map((c, i) => {
      tails = [tail];
      follow(stepId(c), [titleOf(c)], c.when);
      tail = { id: stepId(c), names: [titleOf(c)], branch: from.branch };
      const relations = [
        i === 0
          ? t("composite.runsIf", { name: catches })
          : t("composite.runsAfter", { names: titleOf(caught[i - 1]!) }),
      ];
      return stepNode(c, relations, {
        ...extra,
        lines: [{ label: t("lines.catches"), text: catches }, ...stepLines(c)],
      });
    });
    return { nodes: out, tail };
  };

  view.groups.forEach((group, g) => {
    if (group.length === 0) return;
    const at = `${prefix}/steps/${g}`;
    const ids = group.map(stepId);
    const titles = group.map(titleOf);
    // A `parallel:` list is a frame, even of one step (the designer adds the next).
    const parallel = Array.isArray(obj(arr(bp.steps)[g]).parallel);
    const slot = parallel ? `parallel:${g}` : ids[0]!;
    const runsAfter = isFirst() ? t("composite.runsFirst") : t("composite.runsAfter", { names: words(after()) });
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
        const label = sideLabel(side);
        const way = { node: slot, cases: [side.taken] };
        let sideTails: Tail[] = [{ id: slot, names: titles, label, branch: way }];
        const before = [t("composite.onSide", { side: label, name: titles[0]! })];
        const sideNodes: string[] = [];
        side.steps.forEach((s, i) => {
          tails = sideTails;
          follow(stepId(s), [titleOf(s)], s.when);
          const own: Tail = { id: stepId(s), names: [titleOf(s)], branch: way };
          const caught = s.catch?.length
            ? catchNodes(s.catch, own, titleOf(s), { onSideOf: slot })
            : { nodes: [], tail: undefined };
          onSides.push(
            stepNode(
              s,
              i === 0 ? before : [...before, t("composite.runsAfter", { names: titleOf(side.steps[i - 1]!) })],
              {
                onSideOf: slot,
              },
            ),
            ...caught.nodes,
          );
          sideNodes.push(stepId(s), ...caught.nodes.map((n) => n.id));
          sideTails = caught.tail ? [own, caught.tail] : [own];
        });
        sides.push({ label, when: side.when, taken: side.taken, nodes: sideNodes });
        if (side.steps.length === 0) straight.push({ label, taken: side.taken });
        else next.push(...sideTails);
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
    const own: Tail = { id: slot, names: titles };
    const caughtBy = parallel ? view.groupCatch[g] : group[0]!.catch;
    const caught = caughtBy?.length
      ? catchNodes(caughtBy, own, parallel ? t("composite.stepOf", { names: words(titles) }) : titles[0]!)
      : { nodes: [], tail: undefined };
    nodes.push(
      ...group.map((s, j) => {
        const others = titles.filter((_, k) => k !== j);
        const relations = [runsAfter];
        if (others.length > 0) relations.push(t("composite.sameTimeAs", { names: words(others) }));
        return stepNode(s, relations, { group: parallel ? slot : undefined });
      }),
      ...caught.nodes,
    );
    if (parallel)
      groups.push({ id: slot, label: t("composite.sameTime"), nodes: ids, problems: problemsIn(at), pointer: at });
    tails = caught.tail ? [own, caught.tail] : [own];
  });

  nodes.push({
    id: "end",
    kind: "end",
    title: t("composite.done"),
    lines: [
      {
        label: t("lines.onFailure"),
        text: t(bp.rollback_on_failure === true ? "composite.undoBuilt" : "composite.keepBuilt"),
      },
    ],
    relations: isFirst() ? [t("composite.noStepYet")] : [t("composite.after", { names: words(after()) })],
    problems: [],
  });
  follow("end", [t("composite.theEnd")]);
  const request = t("composite.theRequest");
  if (view.onError.length > 0) nodes.push(...catchNodes(view.onError, { id: "end", names: [request] }, request).nodes);
  return { kind: "composite", direction: "TB", nodes, edges, groups, problems: view.problems };
}

// ---- VM items --------------------------------------------------------------

interface Builder {
  node(n: Omit<CanvasNode, "relations" | "problems"> & { problems?: string[] }, bucket: Bucket): CanvasNode;
}

type Bucket = "component" | "disk" | "resource" | "network" | "segment" | "group" | "other" | "any";
const bucketOrder: Bucket[] = ["component", "disk", "resource", "network", "segment", "group", "other", "any"];

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
  if (typeof v === "number") return formatSize(v, "GB");
  return i18n.t("common:units.GB", { value: str(v) || "?" });
}

/** What a custom resource's inputs read of the components and the resources before it. */
const readsOf = (text: string, root: "components" | "resources") =>
  [...celCode(text).matchAll(new RegExp(`(?<![\\w.])${root}\\.([a-z][a-z0-9_]*)`, "g"))].map((m) => m[1]!);

/**
 * A VM item's topology: `component:<key>`, `resource:<key>` for a custom
 * resource (ADR-0056), `network:<binding>`, `segment:<key>`, `sg:<key>`,
 * `nsx:<id>`, `any`; designed, the disks too.
 */
export function vmGraph(bp: Raw, prefix = "/definition/blueprint", options: GraphOptions = {}): CanvasGraph {
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
        badge: t("badge.segment"),
        lines: [{ text: t("vm.madeEach") }],
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
        badge: t("badge.group"),
        lines: [{ text: t("vm.madeEach") }],
        pointer: `${prefix}/security_groups/${i}`,
      },
      "group",
    );
  });

  const unknownGroup = (key: string, field: string) => {
    const n = b.node(
      { id: `sg:${key}`, kind: "security_group", title: key, subtitle: key, badge: t("badge.group"), lines: [] },
      "group",
    );
    if (n.problems.length === 0) problem(n, field, "unknown_group", t("vm.noGroup", { key }));
    return n;
  };
  const existingGroup = (ref: string) =>
    b.node(
      {
        id: ref,
        kind: "nsx_group",
        title: ref.slice("nsx:".length),
        badge: t("badge.group"),
        lines: [{ text: t("vm.existing") }],
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
          title: t("vm.byPlacement"),
          badge: t("badge.network"),
          lines: [],
        },
        "network",
      );
    const seg = segmentRef.exec(e);
    if (seg) {
      const key = seg[1]!;
      const n = b.node(
        { id: `segment:${key}`, kind: "segment", title: key, subtitle: key, badge: t("badge.segment"), lines: [] },
        "segment",
      );
      if (!n.pointer && n.problems.length === 0) problem(n, field, "unknown_segment", t("vm.noSegment", { key }));
      return n;
    }
    const lit = quoted.exec(e);
    if (lit) {
      const name = lit[1] ?? lit[2] ?? "";
      return b.node(
        {
          id: `network:${e}`,
          kind: "network",
          title: name,
          badge: t("badge.network"),
          lines: [{ text: t("vm.namedInItem") }],
        },
        "network",
      );
    }
    return b.node(
      {
        id: `network:${e}`,
        kind: "network",
        title: t("vm.fromRequest"),
        subtitle: e,
        badge: t("badge.network"),
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
  const byKey = new Map<string, { node: CanvasNode; title: string }>();

  for (const { c, pointer } of components) {
    const key = declared ? str(c.key) : "vm";
    const bindings = { ...obj(bp.bindings), ...obj(c.bindings) };
    const title = declared ? str(c.title) || key : t("vm.machine");
    const lines: CanvasLine[] = [];
    let badge = t("badge.vm");
    if (declared) {
      const count = c.count ?? 1;
      if (typeof count === "number") badge = t("badge.vmCount", { count });
      else {
        badge = t("badge.vms");
        lines.push({ label: t("lines.count"), text: str(count), mono: true });
      }
    }
    const template = str(c.template) || str(bp.template) || str(bindings.template);
    const library = str(c.library) || str(bp.library);
    const os = str(obj(c.guest).os_family) || str(bpGuest.os_family);
    if (template)
      lines.push({
        label: t("lines.template"),
        text: [library ? `${library}/${template}` : template, os].filter(Boolean).join(" · "),
        mono: true,
      });
    const disks = arr(c.disks ?? bp.disks).map(obj);
    if (disks.length > 0 && !options.designed)
      lines.push({
        label: t("lines.disks"),
        text: [
          t("vm.system"),
          ...disks.map((d) => sizeText(d.size_gb) + (str(d.storage_policy) ? ` (${str(d.storage_policy)})` : "")),
        ].join(", "),
      });
    if (c.anti_affinity === true) lines.push({ text: t("vm.separateHosts") });
    const post = bindings.post_config;
    if (post !== undefined) {
      const keys = listed(post);
      if (keys)
        lines.push({
          label: t("lines.then"),
          text: keys.map((k) => str(obj(awx[k]).title) || k).join(", ") || t("vm.nothing"),
        });
      else lines.push({ label: t("lines.then"), text: str(post), mono: true });
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
    if (declared) byKey.set(key, { node: comp, title });
    if (options.designed) {
      const own = !declared || c.disks !== undefined;
      disks.forEach((d, i) => {
        const disk = b.node(
          {
            id: own ? `disk:${key}:${i}` : `disk@${i}`,
            kind: "disk",
            title: str(d.key) || t("vm.diskNumber", { number: i + 1 }),
            badge: t("badge.disk"),
            lines: [
              { label: t("lines.size"), text: sizeText(d.size_gb) },
              ...(str(d.storage_policy) ? [{ label: t("lines.policy"), text: str(d.storage_policy) }] : []),
            ],
            pointer: `${own ? pointer : prefix}/disks/${i}`,
          },
          "disk",
        );
        edges.push({
          id: `attach:${disk.id}>${comp.id}`,
          source: disk.id,
          target: comp.id,
          kind: "disk",
          description: t("vm.diskEdge", { disk: disk.title, title }),
        });
        relate(comp, t("vm.hasDisk", { disk: disk.title, size: sizeText(d.size_gb) }));
        relate(disk, t("vm.diskOf", { title }));
      });
    }

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
          ? t("vm.placementNetwork")
          : net.kind === "network" && net.subtitle
            ? t("vm.requestNetwork", { name: net.subtitle })
            : net.title;
      const nic = { title, key: a.key, network: on };
      edges.push({
        id: `nic:${comp.id}:${a.key}`,
        source: net.id,
        target: comp.id,
        kind: "network",
        label: a.dhcp ? `${a.key} · DHCP` : a.key,
        description: t(a.dhcp ? "vm.adapterEdgeDhcp" : "vm.adapterEdge", nic),
      });
      relate(comp, t(a.dhcp ? "vm.adapterDhcp" : "vm.adapter", nic));
      relate(net, t("vm.adapterOf", nic));
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
            title: t("vm.chosenGroups"),
            subtitle: chosen,
            badge: t("badge.group"),
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
        description: t("vm.memberEdge", { title, group: g.title }),
      });
      relate(comp, t("vm.in", { group: g.title }));
      relate(g, t("vm.holds", { title }));
    }
  }

  const after = dependsOn(bp);
  for (const { c, pointer } of declared ? components : []) {
    const key = str(c.key);
    const to = byKey.get(key);
    if (!to) continue;
    strings(c.depends_on).forEach((d, i) => {
      const field = `${pointer}/depends_on/${i}`;
      if (d === key) problem(to.node, field, "invalid", t("vm.selfDepends"));
      else if (!byKey.has(d)) problem(to.node, field, "unknown_component", t("vm.noComponent", { key: d }));
    });
    for (const d of after.get(key) ?? []) {
      const from = byKey.get(d)!;
      edges.push({
        id: `after:${d}>${key}`,
        source: from.node.id,
        target: to.node.id,
        kind: "sequence",
        description: t("vm.builtAfterEdge", { to: to.title, from: from.title }),
      });
      relate(to.node, t("vm.builtAfter", { from: from.title }));
      relate(from.node, t("vm.builtBefore", { to: to.title }));
    }
  }

  // Custom resources are made once the VMs are built, one after the other:
  // drawn after the components and resources their inputs read.
  const made = new Map<string, CanvasNode>();
  arr(bp.resources).forEach((raw, i) => {
    const r = obj(raw);
    const key = str(r.key);
    const title = str(r.title) || key;
    const n = b.node(
      {
        id: `resource:${key || i}`,
        kind: "resource",
        title,
        subtitle: key && key !== title ? key : undefined,
        badge: t("badge.resource"),
        lines: str(r.type) ? [{ label: t("lines.type"), text: str(r.type), mono: true }] : [],
        pointer: `${prefix}/resources/${i}`,
      },
      "resource",
    );
    const texts = Object.values(obj(r.with)).filter((v): v is string => typeof v === "string");
    const before = [
      ...new Set([
        ...texts.flatMap((x) => readsOf(x, "components")).flatMap((k) => (byKey.has(k) ? [byKey.get(k)!.node] : [])),
        ...texts.flatMap((x) => readsOf(x, "resources")).flatMap((k) => (made.has(k) ? [made.get(k)!] : [])),
      ]),
    ];
    for (const from of before) {
      edges.push({
        id: `after:${from.id}>${n.id}`,
        source: from.id,
        target: n.id,
        kind: "sequence",
        description: t("vm.madeAfterEdge", { title, from: from.title }),
      });
      relate(from, t("vm.before", { title }));
    }
    relate(
      n,
      before.length > 0 ? t("vm.madeAfter", { names: words(before.map((x) => x.title)) }) : t("vm.madeOnceBuilt"),
    );
    if (key) made.set(key, n);
  });

  const ruleKind = (action: string) =>
    action === "allow" || action === "drop" || action === "reject" ? action : "other";
  securityGroups.forEach((sg, i) => {
    const into = buckets.get("group")!.get(`sg:${str(sg.key)}`)!;
    const key = str(sg.key);
    arr(sg.rules).forEach((raw, k) => {
      const r = obj(raw);
      const from = strings(r.from);
      const services = strings(r.services);
      const action = str(r.action) || "allow";
      const svc =
        services.length === 0 || services.every((s) => s === "any") ? t("vm.anyService") : services.join(", ");
      const rule = ruleKind(action);
      (from.length > 0 ? from : ["any"]).forEach((f, m) => {
        const field = `${prefix}/security_groups/${i}/rules/${k}/from/${m}`;
        const source =
          f === "any"
            ? b.node({ id: "any", kind: "any", title: t("vm.any"), lines: [{ text: t("vm.anySource") }] }, "any")
            : groupRef(f, field);
        edges.push({
          id: `rule:${key}:${k}:${m}`,
          source: source.id,
          target: into.id,
          kind: "rule",
          label: action === "allow" ? svc : `${svc} · ${action}`,
          description: t(`vm.rule.${rule}.edge`, { action, services: svc, from: source.title, to: into.title }),
          deny: action !== "allow",
        });
        relate(into, t(`vm.rule.${rule}.into`, { action, services: svc, from: source.title }));
        relate(source, t(`vm.rule.${rule}.from`, { action, services: svc, to: into.title }));
      });
    });
  });

  const nodes = bucketOrder.flatMap((k) => [...buckets.get(k)!.values()]);
  return { kind: "vm", direction: "LR", nodes, edges, groups: [], problems };
}
