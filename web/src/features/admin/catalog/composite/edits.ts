// The composite designer's edits (ADR-0032): each takes the definition's
// YAML and gives it back with one change, made on the parsed document
// (yaml's Document API) rather than on the data, so the comments, the
// quoting and the flow or block style of everything else stay as written.
// The text the server's YAML is written in (definitionYaml.toYaml) comes
// back unchanged but for the edit; YAML written in another indentation is
// re-indented the first time, its comments kept.
//
// A step is named by its node's ID on the canvas: `step:<key>`, or
// `step@<pointer>` when its key is missing or taken twice.
//
// An edit changes the step it is made on and no other, whatever anchors
// and aliases (&name, *name) the text shares between steps: a step whose
// part is an alias gets a copy of what it named before that part is edited,
// and an alias elsewhere whose anchor the edit changed, removed or moved
// after it becomes a copy of what it named before the edit.
import {
  type Alias,
  Document,
  isAlias,
  isMap,
  isNode,
  isScalar,
  isSeq,
  parseDocument,
  type Node,
  visit,
  YAMLMap,
  YAMLSeq,
} from "yaml";
import { parseText } from "../definitionYaml";

type Raw = Record<string, unknown>;

/** How the edited text is written: as toYaml writes it, flow collections as {a: b}. */
export const yamlOutput = { indent: 2, lineWidth: 0, flowCollectionPadding: false } as const;

/** The order a step's keys are written in, so a key added lands where a reader expects it. */
const stepOrder = ["key", "title", "item", "awx", "workflow", "when", "with", "undo"];
const undoOrder = ["job_template", "workflow", "with"];
const awxOrder = ["job_template", "template_id", "title", "workflow", "inventory", "credentials"];

export const blueprintPrefix = "/definition/blueprint";

interface Located {
  /** The slot: its index in steps. */
  g: number;
  /** Its index in the slot's parallel list, or null for a step on its own. */
  j: number | null;
  node: YAMLMap;
  key: string;
  pointer: string;
}

/** Thrown when an edit can't be made: the YAML doesn't parse, or the step isn't there. */
export class EditError extends Error {}

/** What each alias of a document named when it was loaded: its data, and a copy of it. */
type Shared = Map<Alias, { json: string; copy: unknown }>;
const shared = new WeakMap<Document, Shared>();

const dataOf = (doc: Document, node: Node) => JSON.stringify(node.toJS(doc));

/** A copy of a node, its aliases replaced by copies of what they name, without anchors. */
function copyOf(doc: Document, node: unknown): unknown {
  if (isAlias(node)) {
    const target = node.resolve(doc);
    if (!target) throw new EditError(`The alias *${node.source} names no anchor before it.`);
    return copyOf(doc, target);
  }
  if (!isNode(node)) return node;
  const out = node.clone();
  if (isScalar(out) || isMap(out) || isSeq(out)) out.anchor = undefined;
  if (isMap(node) && isMap(out))
    out.items = node.items.map((p) => {
      const c = p.clone();
      c.key = copyOf(doc, p.key);
      c.value = copyOf(doc, p.value);
      return c;
    });
  else if (isSeq(node) && isSeq(out)) out.items = node.items.map((i) => copyOf(doc, i));
  return out;
}

/** Notes what each alias names now, to keep it so through the edit (save). */
function remember(doc: Document) {
  const now: Shared = new Map();
  visit(doc, {
    Alias(_, a) {
      const target = a.resolve(doc);
      if (!target) throw new EditError(`The alias *${a.source} names no anchor before it.`);
      now.set(a, { json: dataOf(doc, target), copy: copyOf(doc, target) });
    },
  });
  shared.set(doc, now);
}

function load(text: string): Document {
  const doc = parseDocument(text);
  if (doc.errors.length > 0) throw new EditError("The YAML doesn't parse.");
  remember(doc);
  return doc;
}

/**
 * The text of an edited document. An alias whose anchor the edit changed,
 * removed or moved after it is replaced by a copy of what it named: the
 * other steps keep their data.
 */
function save(doc: Document): string {
  const before = shared.get(doc);
  if (before?.size)
    visit(doc, {
      Alias(_, a) {
        const was = before.get(a);
        if (!was) return undefined;
        const target = a.resolve(doc);
        let same = false;
        try {
          same = target !== undefined && dataOf(doc, target) === was.json;
        } catch {
          // It names something holding an alias the edit broke.
        }
        return same ? undefined : (was.copy as Node);
      },
    });
  return doc.toString(yamlOutput);
}

/** A map's value at a key to edit: an alias there is replaced by a copy of what it names first. */
function own(doc: Document, map: YAMLMap, key: string): unknown {
  const value: unknown = map.get(key, true);
  if (!isAlias(value)) return value;
  const copy = copyOf(doc, value);
  map.set(key, copy);
  return copy;
}

function blueprintOf(doc: Document): YAMLMap {
  const bp = doc.getIn(["blueprint"], true);
  if (!isMap(bp)) throw new EditError("The definition has no blueprint.");
  return bp;
}

/** The blueprint's steps, made a block list if it was empty or written inline. */
function stepsOf(doc: Document, create = false): YAMLSeq {
  const bp = blueprintOf(doc);
  const found: unknown = bp.get("steps", true);
  let seq: YAMLSeq;
  if (isSeq(found)) seq = found;
  else {
    if (!create) throw new EditError("The blueprint has no steps.");
    seq = new YAMLSeq();
    put(doc, bp, "steps", seq, ["kind", "rollback_on_failure", "steps"]);
  }
  if (seq.items.length === 0) seq.flow = false;
  return seq;
}

/** A slot's parallel list. */
function parallelOf(slot: unknown): YAMLSeq {
  const list = isMap(slot) ? slot.get("parallel", true) : undefined;
  if (!isSeq(list)) throw new EditError("That step isn't in a parallel group.");
  return list;
}

const scalarText = (n: unknown) => (isScalar(n) && typeof n.value === "string" ? n.value : "");

function locateAll(doc: Document): Located[] {
  const out: Located[] = [];
  const steps = doc.getIn(["blueprint", "steps"], true);
  if (!isSeq(steps)) return out;
  steps.items.forEach((item, g) => {
    if (!isMap(item)) return;
    const parallel = item.get("parallel", true);
    if (isSeq(parallel)) {
      parallel.items.forEach((s, j) => {
        if (isMap(s))
          out.push({
            g,
            j,
            node: s,
            key: scalarText(s.get("key", true)),
            pointer: `${blueprintPrefix}/steps/${g}/parallel/${j}`,
          });
      });
      return;
    }
    out.push({
      g,
      j: null,
      node: item,
      key: scalarText(item.get("key", true)),
      pointer: `${blueprintPrefix}/steps/${g}`,
    });
  });
  return out;
}

function locate(doc: Document, id: string): Located {
  const all = locateAll(doc);
  let found: Located | undefined;
  if (id.startsWith("step:")) {
    const matches = all.filter((s) => s.key === id.slice(5));
    if (matches.length === 1) found = matches[0];
  } else if (id.startsWith("step@")) found = all.find((s) => s.pointer === id.slice(5));
  if (!found) throw new EditError("That step isn't in the definition any more.");
  return found;
}

/** The canvas's ID for the step with that key. */
export const stepId = (key: string) => `step:${key}`;

/** The steps' keys, in order. */
export function stepKeys(text: string): string[] {
  try {
    return locateAll(load(text)).map((s) => s.key);
  } catch {
    return [];
  }
}

/** A key like base no step has: base, base_2, base_3... */
export function uniqueKey(text: string, base: string): string {
  const taken = new Set(stepKeys(text));
  const stem =
    base
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "_")
      .replace(/^[^a-z]+|_+$/g, "")
      .slice(0, 36) || "step";
  if (!taken.has(stem)) return stem;
  for (let i = 2; ; i++) if (!taken.has(`${stem}_${i}`)) return `${stem}_${i}`;
}

/** Sets a key of a map, keeping a scalar's style; a key new to it goes where `order` puts it. */
function put(doc: Document, map: YAMLMap, key: string, value: unknown, order: string[] = []) {
  const prev = map.get(key, true);
  if (isScalar(prev) && (value === null || typeof value !== "object")) {
    prev.value = value;
    return;
  }
  const node = doc.createNode(value) as Node;
  if (map.has(key)) {
    map.set(key, node);
    return;
  }
  if (map.items.length === 0) map.flow = false;
  const pair = doc.createPair(key, node);
  const rank = order.indexOf(key);
  const at =
    rank < 0
      ? -1
      : map.items.findIndex((p) => {
          const r = order.indexOf(scalarText(p.key));
          return r > rank;
        });
  if (at < 0) map.items.push(pair);
  else map.items.splice(at, 0, pair);
}

/** Every CEL string of a step: its condition and bindings, its undo's bindings. */
function expressionNodes(step: YAMLMap): { holder: YAMLMap; key: string }[] {
  const out: { holder: YAMLMap; key: string }[] = [];
  if (isScalar(step.get("when", true))) out.push({ holder: step, key: "when" });
  const maps = [step.get("with", true)];
  const undo = step.get("undo", true);
  if (isMap(undo)) maps.push(undo.get("with", true));
  for (const m of maps)
    if (isMap(m)) for (const p of m.items) if (isScalar(p.value)) out.push({ holder: m, key: scalarText(p.key) });
  return out;
}

function escape(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// ---- Adding, removing and moving steps --------------------------------------

export type Where = { after: string } | { with: string } | "end";

/** Adds a step: at the end, after a step (after its group, if it is in one), or at the same time as one. */
export function addStep(text: string, step: Raw, where: Where): string {
  const doc = load(text);
  const steps = stepsOf(doc, true);
  const node = doc.createNode(step) as YAMLMap;
  if (where === "end") steps.items.push(node);
  else if ("after" in where) {
    const at = locate(doc, where.after);
    steps.items.splice(at.g + 1, 0, node);
  } else {
    const at = locate(doc, where.with);
    if (at.j === null) {
      // On its own: it and the new one become a parallel group in its place.
      const group = doc.createNode({ parallel: [] }) as YAMLMap;
      parallelOf(group).items.push(at.node, node);
      steps.items[at.g] = group;
    } else {
      const list = parallelOf(steps.items[at.g]);
      list.items.splice(at.j + 1, 0, node);
    }
  }
  return save(doc);
}

/** A parallel group left with one step is that step again. */
function unwrap(steps: YAMLSeq, g: number) {
  const slot = steps.items[g];
  if (!isMap(slot)) return;
  const list = slot.get("parallel", true);
  if (!isSeq(list)) return;
  if (list.items.length === 0) steps.items.splice(g, 1);
  else if (list.items.length === 1 && slot.items.length === 1) steps.items[g] = list.items[0]!;
}

/** Takes a step out of where it is, and gives its node. */
function detach(doc: Document, at: Located): YAMLMap {
  const steps = stepsOf(doc);
  if (at.j === null) steps.items.splice(at.g, 1);
  else {
    parallelOf(steps.items[at.g]).items.splice(at.j, 1);
    unwrap(steps, at.g);
  }
  return at.node;
}

export function removeStep(text: string, id: string): string {
  const doc = load(text);
  detach(doc, locate(doc, id));
  return save(doc);
}

/**
 * Moves a step one place earlier (-1) or later (1). On its own, it trades
 * places with the step or group before or after it; in a parallel group, it
 * leaves the group, to run just before or just after it.
 */
export function moveStep(text: string, id: string, dir: -1 | 1): string {
  const doc = load(text);
  const at = locate(doc, id);
  const steps = stepsOf(doc);
  if (at.j === null) {
    const to = at.g + dir;
    if (to < 0 || to >= steps.items.length) throw new EditError(dir < 0 ? "It runs first." : "It runs last.");
    const [slot] = steps.items.splice(at.g, 1);
    steps.items.splice(to, 0, slot);
  } else {
    const list = parallelOf(steps.items[at.g]);
    list.items.splice(at.j, 1);
    steps.items.splice(dir < 0 ? at.g : at.g + 1, 0, at.node);
    unwrap(steps, dir < 0 ? at.g + 1 : at.g);
  }
  return save(doc);
}

/** A step on its own runs at the same time as the step (or group) before it. */
export function joinPrevious(text: string, id: string): string {
  const doc = load(text);
  const at = locate(doc, id);
  if (at.j !== null) throw new EditError("It already runs at the same time as others.");
  if (at.g === 0) throw new EditError("It runs first.");
  const steps = stepsOf(doc);
  const before = steps.items[at.g - 1];
  steps.items.splice(at.g, 1);
  const list = isMap(before) ? before.get("parallel", true) : undefined;
  if (isSeq(list)) list.items.push(at.node);
  else {
    const group = doc.createNode({ parallel: [] }) as YAMLMap;
    parallelOf(group).items.push(before, at.node);
    steps.items[at.g - 1] = group;
  }
  return save(doc);
}

// ---- A step's fields --------------------------------------------------------

/** Sets a step's key, title or condition; empty removes it. A new key is written into the steps that use it. */
export function setStepField(text: string, id: string, field: "key" | "title" | "when", value: string): string {
  const doc = load(text);
  const at = locate(doc, id);
  const v = value.trim();
  if (field === "key" && at.key && v && v !== at.key) {
    const old = new RegExp(`\\bsteps\\.${escape(at.key)}\\b`, "g");
    for (const s of locateAll(doc))
      for (const { holder, key } of expressionNodes(s.node)) {
        const n = holder.get(key, true);
        if (isScalar(n) && typeof n.value === "string") n.value = n.value.replace(old, `steps.${v}`);
      }
    // Every step's are renamed, an alias's with its anchor's: what they name now is kept.
    remember(doc);
  }
  if (!v && field !== "key") at.node.delete(field);
  else put(doc, at.node, field, v, stepOrder);
  return save(doc);
}

export type Target =
  | { kind: "item"; ref: string }
  /** An AWX template: its name, whether it is a workflow template, its ID in AWX when known. */
  | { kind: "awx"; ref: string; workflow?: boolean; templateId?: number }
  | { kind: "workflow"; ref: string };

/**
 * What a step runs: one of an item, an AWX template or a custom workflow;
 * the others are removed. An AWX template is written by its name and, when
 * known, its ID (awx.template_id: it is launched by it); another template's
 * ID and title go with it, its inputs' bindings stay.
 */
export function setTarget(text: string, id: string, t: Target): string {
  const doc = load(text);
  const { node } = locate(doc, id);
  for (const k of ["item", "awx", "workflow"]) if (k !== t.kind) node.delete(k);
  if (t.kind === "awx") {
    const awx = own(doc, node, "awx");
    if (isMap(awx)) {
      const was = awx.toJS(doc) as Raw;
      const same =
        Boolean(was.workflow) === Boolean(t.workflow) &&
        (typeof was.template_id === "number" && t.templateId
          ? was.template_id === t.templateId
          : was.job_template === t.ref);
      put(doc, awx, "job_template", t.ref, awxOrder);
      if (t.templateId) put(doc, awx, "template_id", t.templateId, awxOrder);
      else awx.delete("template_id");
      if (t.workflow) put(doc, awx, "workflow", true, awxOrder);
      else awx.delete("workflow");
      if (!same) awx.delete("title");
    } else
      put(
        doc,
        node,
        "awx",
        {
          job_template: t.ref,
          ...(t.templateId ? { template_id: t.templateId } : {}),
          ...(t.workflow ? { workflow: true } : {}),
        },
        stepOrder,
      );
  } else put(doc, node, t.kind, t.ref, stepOrder);
  return save(doc);
}

/** A binding's value: a CEL expression (a string) or a literal; undefined unbinds it. */
export type Binding = string | number | boolean | unknown[] | Raw | undefined;

/** Binds one of a step's inputs (scope with) or of its undo's (scope undo). */
export function setBinding(
  text: string,
  id: string,
  name: string,
  value: Binding,
  scope: "with" | "undo" = "with",
): string {
  const doc = load(text);
  const { node } = locate(doc, id);
  let holder: YAMLMap = node;
  if (scope === "undo") {
    const undo = own(doc, node, "undo");
    if (!isMap(undo)) throw new EditError("The step has no undo.");
    holder = undo;
  }
  const order = scope === "undo" ? undoOrder : stepOrder;
  const current = own(doc, holder, "with");
  if (value === undefined) {
    if (isMap(current)) {
      current.delete(name);
      if (current.items.length === 0) holder.delete("with");
    }
  } else if (isMap(current)) put(doc, current, name, value);
  else put(doc, holder, "with", { [name]: value }, order);
  return save(doc);
}

export type Undo = { kind: "awx" | "workflow"; ref: string } | null;

/** What undoes a step when a later one fails: an AWX template or a custom workflow; null for nothing. Its bindings stay. */
export function setUndo(text: string, id: string, undo: Undo): string {
  const doc = load(text);
  const { node } = locate(doc, id);
  if (!undo) {
    node.delete("undo");
    return save(doc);
  }
  const key = undo.kind === "awx" ? "job_template" : "workflow";
  const current = own(doc, node, "undo");
  if (isMap(current)) {
    current.delete(undo.kind === "awx" ? "workflow" : "job_template");
    put(doc, current, key, undo.ref, undoOrder);
  } else put(doc, node, "undo", { [key]: undo.ref }, stepOrder);
  return save(doc);
}

/** Whether a failure undoes what the earlier steps built. */
export function setRollback(text: string, on: boolean): string {
  const doc = load(text);
  put(doc, blueprintOf(doc), "rollback_on_failure", on, ["kind", "rollback_on_failure", "steps"]);
  return save(doc);
}

/** A step as the definition has it (parsed), with where it is; undefined when it isn't there. */
export function readStep(
  text: string,
  id: string,
): { step: Raw; g: number; j: number | null; pointer: string } | undefined {
  // The text the graph and the designer parsed, read only.
  const { doc, error } = parseText(text);
  if (error) return undefined;
  try {
    const at = locate(doc, id);
    return { step: (at.node.toJS(doc) as Raw) ?? {}, g: at.g, j: at.j, pointer: at.pointer };
  } catch {
    return undefined;
  }
}
