// The composite designer's edits (ADR-0032): each takes the definition's
// YAML and gives it back with one change, made on the parsed document
// (yaml's Document API) rather than on the data, so the comments, the
// quoting and the flow or block style of everything else stay as written.
// The text the server's YAML is written in (definitionYaml.toYaml) comes
// back unchanged but for the edit; YAML written in another indentation is
// re-indented the first time, its comments kept.
//
// A step is named by its node's ID on the canvas: `step:<key>`, or
// `step@<pointer>` when its key is missing or taken twice. A step of a catch
// path or of the error handler is named so too (ADR-0057).
//
// An edit changes the step it is made on and no other, whatever anchors
// and aliases (&name, *name) the text shares between steps (../yamlDoc).
import { Document, isMap, isScalar, isSeq, YAMLMap, YAMLSeq } from "yaml";
import { catchRefused, caughtRefused, loopRefused, parseUrl } from "@/lib/composite";
import { i18n } from "@/lib/i18n";
import { parseText } from "../definitionYaml";
import { EditError, load, own, put, remember, save, scalarText } from "../yamlDoc";

export { EditError, yamlOutput } from "../yamlDoc";

const tr = i18n.getFixedT(null, "admin-catalog");

type Raw = Record<string, unknown>;

/** The order a step's keys are written in, so a key added lands where a reader expects it. */
const stepOrder = [
  "key",
  "title",
  "item",
  "awx",
  "workflow",
  "rest",
  "ask",
  "wait",
  "poll",
  "signal",
  "branch",
  "notify",
  "resource",
  "when",
  "for_each",
  "with",
  "undo",
  "catch",
];
const undoOrder = ["job_template", "workflow", "rest", "with"];
const restOrder = ["method", "url", "body", "secret", "accept", "keep"];
const askOrder = [
  "group",
  "approvers",
  "requester",
  "owner",
  "message",
  "form",
  "timeout",
  "on_timeout",
  "escalate_to",
];
const waitOrder = ["for", "until"];
const pollOrder = ["rest", "until", "every", "timeout", "on_timeout"];
const questionOrder = ["key", "label", "type", "required", "options", "default"];
const branchOrder = ["cases", "else"];
const caseOrder = ["title", "when", "steps"];
const notifyOrder = ["requester", "owner", "groups", "users", "message"];
const awxOrder = ["job_template", "template_id", "title", "workflow", "inventory", "credentials"];
const signalOrder = ["name", "event", "when", "timeout", "on_timeout"];
const forEachOrder = ["items", "parallel"];
const groupOrder = ["parallel", "catch"];
const blueprintOrder = ["kind", "rollback_on_failure", "steps", "on_error"];

export const blueprintPrefix = "/definition/blueprint";

interface Located {
  /** The slot: its index in steps. */
  g: number;
  /** Its index in the slot's parallel list, or null for a step on its own. */
  j: number | null;
  node: YAMLMap;
  key: string;
  pointer: string;
  /** The side of a branch it is on. */
  side?: { branch: YAMLMap; key: string; list: YAMLSeq; index: number; taken: number };
  /**
   * The catch path it is on: the list, its place there, and what it catches,
   * the step's key, "" for a parallel group (slot g), or "on_error" for the
   * error handler.
   */
  caught?: { holder: YAMLMap; list: YAMLSeq; index: number; of: string };
}

function blueprintOf(doc: Document): YAMLMap {
  const bp = doc.getIn(["blueprint"], true);
  if (!isMap(bp)) throw new EditError(tr("composite.edit.noBlueprint"));
  return bp;
}

/** The blueprint's steps, made a block list if it was empty or written inline. */
function stepsOf(doc: Document, create = false): YAMLSeq {
  const bp = blueprintOf(doc);
  const found: unknown = bp.get("steps", true);
  let seq: YAMLSeq;
  if (isSeq(found)) seq = found;
  else {
    if (!create) throw new EditError(tr("composite.edit.noSteps"));
    seq = new YAMLSeq();
    put(doc, bp, "steps", seq, ["kind", "rollback_on_failure", "steps"]);
  }
  if (seq.items.length === 0) seq.flow = false;
  return seq;
}

/** A slot's parallel list. */
function parallelOf(slot: unknown): YAMLSeq {
  const list = isMap(slot) ? slot.get("parallel", true) : undefined;
  if (!isSeq(list)) throw new EditError(tr("composite.edit.notParallel"));
  return list;
}

function locateAll(doc: Document): Located[] {
  const out: Located[] = [];
  const steps = doc.getIn(["blueprint", "steps"], true);
  /** The steps of the catch path of holder, after what it catches. */
  const catchOf = (holder: YAMLMap, g: number, at: string, of: string, field = "catch") => {
    const list = holder.get(field, true);
    if (!isSeq(list)) return;
    list.items.forEach((s, index) => {
      if (isMap(s))
        out.push({
          g,
          j: null,
          node: s,
          key: scalarText(s.get("key", true)),
          pointer: `${at}/${index}`,
          caught: { holder, list, index, of },
        });
    });
  };
  if (isSeq(steps))
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
        catchOf(item, g, `${blueprintPrefix}/steps/${g}/catch`, "");
        return;
      }
      const key = scalarText(item.get("key", true));
      const pointer = `${blueprintPrefix}/steps/${g}`;
      out.push({ g, j: null, node: item, key, pointer });
      catchOf(item, g, `${pointer}/catch`, key);
      const branch = item.get("branch", true);
      if (!isMap(branch)) return;
      const onSide = (list: unknown, taken: number, at: string) => {
        if (!isSeq(list)) return;
        list.items.forEach((s, index) => {
          if (!isMap(s)) return;
          const own = scalarText(s.get("key", true));
          out.push({
            g,
            j: null,
            node: s,
            key: own,
            pointer: `${at}/${index}`,
            side: { branch, key, list, index, taken },
          });
          catchOf(s, g, `${at}/${index}/catch`, own);
        });
      };
      const cases = branch.get("cases", true);
      if (isSeq(cases))
        cases.items.forEach((c, i) => {
          if (isMap(c)) onSide(c.get("steps", true), i + 1, `${pointer}/branch/cases/${i}/steps`);
        });
      onSide(branch.get("else", true), 0, `${pointer}/branch/else`);
    });
  const bp = doc.getIn(["blueprint"], true);
  if (isMap(bp))
    catchOf(bp, isSeq(steps) ? steps.items.length : 0, `${blueprintPrefix}/on_error`, "on_error", "on_error");
  return out;
}

function locate(doc: Document, id: string): Located {
  const all = locateAll(doc);
  let found: Located | undefined;
  if (id.startsWith("step:")) {
    const matches = all.filter((s) => s.key === id.slice(5));
    if (matches.length === 1) found = matches[0];
  } else if (id.startsWith("step@")) found = all.find((s) => s.pointer === id.slice(5));
  if (!found) throw new EditError(tr("composite.edit.gone"));
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

/** Every CEL string of a step: its condition and bindings, its undo's bindings, the bodies of its REST calls, what a wait or a poll waits for, a branch's cases. */
function expressionNodes(step: YAMLMap): { holder: YAMLMap; key: string }[] {
  const out: { holder: YAMLMap; key: string }[] = [];
  if (isScalar(step.get("when", true))) out.push({ holder: step, key: "when" });
  const maps = [step.get("with", true)];
  const undo = step.get("undo", true);
  if (isMap(undo)) maps.push(undo.get("with", true));
  for (const m of maps)
    if (isMap(m)) for (const p of m.items) if (isScalar(p.value)) out.push({ holder: m, key: scalarText(p.key) });
  for (const call of restCallsOf(step)) if (isScalar(call.get("body", true))) out.push({ holder: call, key: "body" });
  for (const part of [step.get("wait", true), step.get("poll", true)])
    if (isMap(part) && isScalar(part.get("until", true))) out.push({ holder: part, key: "until" });
  const signal = step.get("signal", true);
  if (isMap(signal) && isScalar(signal.get("when", true))) out.push({ holder: signal, key: "when" });
  const forEach = step.get("for_each", true);
  if (isMap(forEach) && isScalar(forEach.get("items", true))) out.push({ holder: forEach, key: "items" });
  const branch = step.get("branch", true);
  const cases = isMap(branch) ? branch.get("cases", true) : undefined;
  if (isSeq(cases))
    for (const c of cases.items) if (isMap(c) && isScalar(c.get("when", true))) out.push({ holder: c, key: "when" });
  return out;
}

/** A step's REST call, its undo's and a poll's. */
function restCallsOf(step: YAMLMap): YAMLMap[] {
  const undo = step.get("undo", true);
  const poll = step.get("poll", true);
  const calls: unknown[] = [
    step.get("rest", true),
    isMap(undo) ? undo.get("rest", true) : undefined,
    isMap(poll) ? poll.get("rest", true) : undefined,
  ];
  return calls.filter((c): c is YAMLMap => isMap(c));
}

/** A URL template with a step's key renamed in its expressions; its text isn't CEL, and stays. */
function renameInUrl(template: string, old: RegExp, next: string): string {
  const { parts, error } = parseUrl(template);
  if (error) return template;
  return parts.map((p) => ("expr" in p ? `\${${p.expr.replace(old, next)}}` : p.text)).join("");
}

function escape(s: string) {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// ---- Adding, removing and moving steps --------------------------------------

/**
 * Where a step is added: at the end, after a step, at the same time as one,
 * on a side of a branch, on the catch path of a step (of its group, if it is
 * in one), or in the error handler.
 */
export type Where =
  { after: string } | { with: string } | { side: string; taken: number } | { catchOf: string } | "onError" | "end";

/** A branch's list of steps for a side (1 for its first case, 0 for otherwise), made if it has none. */
function sideList(doc: Document, id: string, taken: number): YAMLSeq {
  const branch = branchOf(doc, id);
  let holder = branch;
  const key = taken === 0 ? "else" : "steps";
  if (taken > 0) {
    const c = casesOf(doc, branch).items[taken - 1];
    if (!isMap(c)) throw new EditError(tr("composite.edit.noCase"));
    holder = c;
  }
  let list = own(doc, holder, key);
  if (!isSeq(list)) {
    put(doc, holder, key, new YAMLSeq(), taken === 0 ? branchOrder : caseOrder);
    list = holder.get(key, true);
  }
  const seq = list as YAMLSeq;
  seq.flow = false;
  return seq;
}

const noBranchOnSide = () => tr("composite.edit.noBranchOnSide");
const noBranchInGroup = () => tr("composite.edit.noBranchInGroup");
const noGroupOnCatch = () => tr("composite.edit.noGroupOnCatch");
const noCatchInGroup = () => tr("composite.edit.noCatchInGroup");

/** A list of a map's, made a block list, and made if it has none. */
function listOf(doc: Document, holder: YAMLMap, key: string, order: string[]): YAMLSeq {
  let list = own(doc, holder, key);
  if (!isSeq(list)) {
    put(doc, holder, key, new YAMLSeq(), order);
    list = holder.get(key, true);
  }
  const seq = list as YAMLSeq;
  seq.flow = false;
  return seq;
}

/** Refuses a step on a catch path, or in the error handler (handler), that the server would refuse there. */
function mayCatch(step: Raw, handler = false) {
  const why = catchRefused(step, handler);
  if (why) throw new EditError(why);
}

/** The catch path of the step at, or of its group: the server's refusals said before it is written. */
function catchListOf(doc: Document, at: Located): YAMLSeq {
  if (at.caught) throw new EditError(tr("composite.edit.noCatchOnCatch"));
  const steps = stepsOf(doc);
  const holder = at.j === null ? at.node : steps.items[at.g];
  if (!isMap(holder)) throw new EditError(tr("composite.edit.gone"));
  const caught = at.j === null ? [at.node] : parallelOf(holder).items.filter(isMap);
  for (const s of caught) {
    const why = caughtRefused(s.toJS(doc) as Raw);
    if (why) throw new EditError(why);
  }
  return listOf(doc, holder, "catch", at.j === null ? stepOrder : groupOrder);
}

/** Adds a step: at the end, after a step (after its group, if it is in one, on its side, if it is on one), at the same time as one, or on a side of a branch. */
export function addStep(text: string, step: Raw, where: Where): string {
  const doc = load(text);
  const steps = stepsOf(doc, true);
  const node = doc.createNode(step) as YAMLMap;
  if (where === "end") steps.items.push(node);
  else if (where === "onError") {
    mayCatch(step, true);
    listOf(doc, blueprintOf(doc), "on_error", blueprintOrder).items.push(node);
  } else if ("catchOf" in where) {
    mayCatch(step);
    catchListOf(doc, locate(doc, where.catchOf)).items.push(node);
  } else if ("side" in where) {
    if (step.branch !== undefined) throw new EditError(noBranchOnSide());
    sideList(doc, where.side, where.taken).items.push(node);
  } else if ("after" in where) {
    const at = locate(doc, where.after);
    if (at.caught) {
      mayCatch(step, at.caught.of === "on_error");
      at.caught.list.items.splice(at.caught.index + 1, 0, node);
    } else if (at.side) {
      if (step.branch !== undefined) throw new EditError(noBranchOnSide());
      at.side.list.flow = false;
      at.side.list.items.splice(at.side.index + 1, 0, node);
    } else steps.items.splice(at.g + 1, 0, node);
  } else {
    const at = locate(doc, where.with);
    if (at.caught) throw new EditError(noGroupOnCatch());
    if (at.side) throw new EditError(tr("composite.edit.noGroupOnSide"));
    if (step.branch !== undefined || at.node.has("branch")) throw new EditError(noBranchInGroup());
    if (step.catch !== undefined || (at.j === null && at.node.has("catch"))) throw new EditError(noCatchInGroup());
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
  if (at.caught) {
    at.caught.list.items.splice(at.caught.index, 1);
    // A catch path with nothing left to run is no list at all.
    if (at.caught.list.items.length === 0) at.caught.holder.delete(at.caught.of === "on_error" ? "on_error" : "catch");
    return at.node;
  }
  const steps = stepsOf(doc);
  if (at.side) {
    at.side.list.items.splice(at.side.index, 1);
    // Otherwise, with nothing left to run, is no list at all; a case keeps its own, empty.
    if (at.side.taken === 0 && at.side.list.items.length === 0) at.side.branch.delete("else");
  } else if (at.j === null) steps.items.splice(at.g, 1);
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
 * leaves the group, to run just before or just after it; on a side of a branch,
 * it stays on that side.
 */
export function moveStep(text: string, id: string, dir: -1 | 1): string {
  const doc = load(text);
  const at = locate(doc, id);
  if (at.caught) {
    const to = at.caught.index + dir;
    if (to < 0 || to >= at.caught.list.items.length)
      throw new EditError(dir < 0 ? tr("composite.edit.firstOnCatch") : tr("composite.edit.lastOnCatch"));
    at.caught.list.items.splice(at.caught.index, 1);
    at.caught.list.items.splice(to, 0, at.node);
    return save(doc);
  }
  const steps = stepsOf(doc);
  if (at.side) {
    const to = at.side.index + dir;
    if (to < 0 || to >= at.side.list.items.length)
      throw new EditError(dir < 0 ? tr("composite.edit.firstOnSide") : tr("composite.edit.lastOnSide"));
    at.side.list.items.splice(at.side.index, 1);
    at.side.list.items.splice(to, 0, at.node);
  } else if (at.j === null) {
    const to = at.g + dir;
    if (to < 0 || to >= steps.items.length)
      throw new EditError(dir < 0 ? tr("composite.edit.first") : tr("composite.edit.last"));
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
  if (at.caught) throw new EditError(noGroupOnCatch());
  if (at.side) throw new EditError(tr("composite.edit.noGroupOnSide"));
  if (at.j !== null) throw new EditError(tr("composite.edit.alreadyGroup"));
  if (at.g === 0) throw new EditError(tr("composite.edit.first"));
  const steps = stepsOf(doc);
  const before = steps.items[at.g - 1];
  if (at.node.has("branch") || (isMap(before) && before.has("branch"))) throw new EditError(noBranchInGroup());
  if (at.node.has("catch") || (isMap(before) && !before.has("parallel") && before.has("catch")))
    throw new EditError(noCatchInGroup());
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
    for (const s of locateAll(doc))
      for (const call of restCallsOf(s.node)) {
        const n = call.get("url", true);
        if (isScalar(n) && typeof n.value === "string") n.value = renameInUrl(n.value, old, `steps.${v}`);
      }
    // A custom resource names the VM step whose deployment it joins.
    for (const s of locateAll(doc)) {
      const resource = s.node.get("resource", true);
      const n = isMap(resource) ? resource.get("deployment", true) : undefined;
      if (isScalar(n) && n.value === at.key) n.value = v;
    }
    // A message's text isn't CEL either, but what it fills in is.
    for (const s of locateAll(doc)) {
      const notify = s.node.get("notify", true);
      const n = isMap(notify) ? notify.get("message", true) : undefined;
      if (isScalar(n) && typeof n.value === "string") n.value = renameInUrl(n.value, old, `steps.${v}`);
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
  | { kind: "workflow"; ref: string }
  /** A custom resource: its type's key (ADR-0056). */
  | { kind: "resource"; ref: string }
  /** A REST call: what it names is in its own fields. */
  | { kind: "rest"; ref: "" }
  /** A wait for a person: who, in its own fields. */
  | { kind: "ask"; ref: "" }
  /** A wait for a time: how long, in its own fields. */
  | { kind: "wait"; ref: "" }
  /** A poll: what it calls and waits for, in its own fields. */
  | { kind: "poll"; ref: "" }
  /** A branch: its cases and what runs otherwise, in its own fields. */
  | { kind: "branch"; ref: "" }
  /** A message: who it tells and what it says, in its own fields. */
  | { kind: "notify"; ref: "" }
  /** A wait for a signal or an event: which, and for how long, in its own fields (ADR-0057). */
  | { kind: "signal"; ref: "" };

/** A REST call just added: it says what is missing until its URL is written. */
export const newRestCall = (method: string) => ({ method, url: "" });

/** A poll just added: every 10 minutes for three days, unless said otherwise; it says what is missing until its URL and condition are written. */
export const newPoll = () => ({ rest: newRestCall("GET"), until: "", every: "10m", timeout: "72h" });

/** A branch just added: one case with no steps, which says what is missing until its condition is written. */
export const newBranch = () => ({ cases: [{ when: "", steps: [] }] });

/** A wait for a signal just added: named as its step, for three days. */
export const newSignal = (key: string) => ({ name: key.replace(/_/g, "-") || "go", timeout: "72h" });

/**
 * What a step runs: one of an item, an AWX template, a custom workflow, a
 * REST call or a wait for a person, a time or a condition; the others are
 * removed. An AWX template is written by its name and, when known, its ID
 * (awx.template_id: it is launched by it); another template's ID and title go
 * with it, its inputs' bindings stay. A REST call has no inputs: its URL and
 * body read them, so its bindings go. A wait has no inputs, and nothing to undo.
 * Nor has a message, or a branch, whose cases have the conditions; a branch runs
 * neither in a parallel group nor on a side of another (ADR-0033). A custom
 * resource is undone by its type's delete, so its undo goes (ADR-0056). A
 * wait for a signal has neither inputs nor an undo; a step that can't loop
 * loses its loop (ADR-0057).
 */
export function setTarget(text: string, id: string, t: Target): string {
  const doc = load(text);
  const { node, side, j } = locate(doc, id);
  if (t.kind === "branch" && side) throw new EditError(noBranchOnSide());
  if (t.kind === "branch" && j !== null) throw new EditError(noBranchInGroup());
  for (const k of ["item", "awx", "workflow", "rest", "ask", "wait", "poll", "signal", "branch", "notify", "resource"])
    if (k !== t.kind) node.delete(k);
  if (["ask", "wait", "poll", "signal", "branch", "resource"].includes(t.kind)) node.delete("for_each");
  if (t.kind === "branch") {
    node.delete("with");
    node.delete("undo");
    node.delete("when");
    if (!isMap(own(doc, node, "branch"))) put(doc, node, "branch", newBranch(), stepOrder);
  } else if (t.kind === "notify") {
    node.delete("with");
    node.delete("undo");
    if (!isMap(own(doc, node, "notify"))) put(doc, node, "notify", {}, stepOrder);
  } else if (t.kind === "rest") {
    node.delete("with");
    if (!isMap(own(doc, node, "rest"))) put(doc, node, "rest", newRestCall("GET"), stepOrder);
  } else if (t.kind === "ask") {
    node.delete("with");
    node.delete("undo");
    if (!isMap(own(doc, node, "ask"))) put(doc, node, "ask", {}, stepOrder);
  } else if (t.kind === "signal") {
    node.delete("with");
    node.delete("undo");
    if (!isMap(own(doc, node, "signal")))
      put(doc, node, "signal", newSignal(scalarText(node.get("key", true))), stepOrder);
  } else if (t.kind === "wait" || t.kind === "poll") {
    node.delete("with");
    node.delete("undo");
    if (!isMap(own(doc, node, t.kind))) put(doc, node, t.kind, t.kind === "poll" ? newPoll() : {}, stepOrder);
  } else if (t.kind === "resource") {
    node.delete("undo");
    const resource = own(doc, node, "resource");
    if (isMap(resource)) put(doc, resource, "type", t.ref, ["type", "deployment"]);
    else put(doc, node, "resource", { type: t.ref }, stepOrder);
  } else if (t.kind === "awx") {
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
    if (!isMap(undo)) throw new EditError(tr("composite.edit.noUndo"));
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

export type Undo = { kind: "awx" | "workflow"; ref: string } | { kind: "rest" } | null;

/**
 * What undoes a step when a later one fails: an AWX template, a custom
 * workflow or a REST call; null for nothing. An AWX template's or a
 * workflow's bindings stay; a REST call has none.
 */
export function setUndo(text: string, id: string, undo: Undo): string {
  const doc = load(text);
  const { node } = locate(doc, id);
  if (!undo) {
    node.delete("undo");
    return save(doc);
  }
  const current = own(doc, node, "undo");
  if (undo.kind === "rest") {
    if (isMap(current)) {
      for (const k of ["job_template", "workflow", "with"]) current.delete(k);
      if (!isMap(own(doc, current, "rest"))) put(doc, current, "rest", newRestCall("DELETE"), undoOrder);
    } else put(doc, node, "undo", { rest: newRestCall("DELETE") }, stepOrder);
    return save(doc);
  }
  const key = undo.kind === "awx" ? "job_template" : "workflow";
  if (isMap(current)) {
    current.delete("rest");
    current.delete(undo.kind === "awx" ? "workflow" : "job_template");
    put(doc, current, key, undo.ref, undoOrder);
  } else put(doc, node, "undo", { [key]: undo.ref }, stepOrder);
  return save(doc);
}

export type RestField = "method" | "url" | "body" | "secret" | "accept" | "keep";

/**
 * One field of a step's REST call (scope step), its undo's (scope undo) or a
 * poll's (scope poll). Empty removes it, but for the method and the URL, which
 * stay for the step to say what is missing. The statuses it accepts are written
 * on one line.
 */
export function setRestField(
  text: string,
  id: string,
  field: RestField,
  value: string | number[],
  scope: "step" | "undo" | "poll" = "step",
): string {
  const doc = load(text);
  const { node } = locate(doc, id);
  let holder: YAMLMap = node;
  if (scope === "undo") {
    const undo = own(doc, node, "undo");
    if (!isMap(undo)) throw new EditError(tr("composite.edit.noUndo"));
    holder = undo;
  }
  if (scope === "poll") holder = partOf(doc, id, "poll");
  const call = own(doc, holder, "rest");
  if (!isMap(call)) throw new EditError(tr("composite.edit.noRest"));
  if (typeof value === "string") {
    const v = value.trim();
    if (!v && field !== "method" && field !== "url") call.delete(field);
    else put(doc, call, field, v, restOrder);
  } else if (value.length === 0) call.delete(field);
  else {
    const list = doc.createNode(value) as YAMLSeq;
    list.flow = true;
    put(doc, call, field, list, restOrder);
  }
  return save(doc);
}

/** A step's wait for a time or poll, from a document; `{}` as just added opens up as it fills. */
function partOf(doc: Document, id: string, kind: "wait" | "poll"): YAMLMap {
  const part = own(doc, locate(doc, id).node, kind);
  if (!isMap(part)) throw new EditError(kind === "wait" ? tr("composite.edit.noWait") : tr("composite.edit.noPoll"));
  part.flow = false;
  return part;
}

/** The VM step whose deployment a custom resource joins; empty: the only one before it. */
export function setResourceDeployment(text: string, id: string, step: string): string {
  const doc = load(text);
  const resource = own(doc, locate(doc, id).node, "resource");
  if (!isMap(resource)) throw new EditError(tr("composite.edit.noResource"));
  if (step.trim()) put(doc, resource, "deployment", step.trim(), ["type", "deployment"]);
  else resource.delete("deployment");
  return save(doc);
}

/** How long a step waits, or until when: one or the other, so that writing one removes the other. */
export function setWaitField(text: string, id: string, field: "for" | "until", value: string): string {
  const doc = load(text);
  const wait = partOf(doc, id, "wait");
  const v = value.trim();
  if (!v) wait.delete(field);
  else {
    put(doc, wait, field, v, waitOrder);
    wait.delete(field === "for" ? "until" : "for");
  }
  return save(doc);
}

export type SignalField = "name" | "event" | "when" | "timeout" | "on_timeout";

/**
 * One field of a step's wait for a signal. It waits for a signal by its name
 * or for an event: writing one removes the other, and a name the event's
 * condition. Empty removes it, for the step to say what is missing.
 */
export function setSignalField(text: string, id: string, field: SignalField, value: string): string {
  const doc = load(text);
  const signal = own(doc, locate(doc, id).node, "signal");
  if (!isMap(signal)) throw new EditError(tr("composite.edit.noSignal"));
  signal.flow = false;
  const v = value.trim();
  if (!v) signal.delete(field);
  else {
    put(doc, signal, field, v, signalOrder);
    if (field === "name") {
      signal.delete("event");
      signal.delete("when");
    } else if (field === "event") signal.delete("name");
  }
  return save(doc);
}

/**
 * A step's loop (ADR-0057): the list it goes over, and how many passes run
 * at once, 1 written as none. A loop has no undo: adding one to a step that
 * has one is refused, as one that can't loop.
 */
export function setForEach(text: string, id: string, field: "items" | "parallel", value: string): string {
  const doc = load(text);
  const at = locate(doc, id);
  const step = at.node.toJS(doc) as Raw;
  if (step.for_each === undefined) {
    const why = at.caught ? catchRefused({ for_each: {} }, at.caught.of === "on_error") : loopRefused(step);
    if (why) throw new EditError(why);
    if (step.undo !== undefined) throw new EditError(tr("composite.edit.loopUndo"));
  }
  let forEach = own(doc, at.node, "for_each");
  if (!isMap(forEach)) {
    put(doc, at.node, "for_each", { items: "" }, stepOrder);
    forEach = at.node.get("for_each", true);
  }
  const map = forEach as YAMLMap;
  map.flow = false;
  const v = value.trim();
  if (field === "items") put(doc, map, "items", v, forEachOrder);
  else if (!v || v === "0" || v === "1") map.delete("parallel");
  else put(doc, map, "parallel", /^\d+$/.test(v) ? Number(v) : v, forEachOrder);
  return save(doc);
}

/** The step runs once again: its loop goes. */
export function removeForEach(text: string, id: string): string {
  const doc = load(text);
  locate(doc, id).node.delete("for_each");
  return save(doc);
}

export type PollField = "until" | "every" | "timeout" | "on_timeout";

/** One field of a step's poll. Empty removes it, for the step to say what is missing. */
export function setPollField(text: string, id: string, field: PollField, value: string): string {
  const doc = load(text);
  const poll = partOf(doc, id, "poll");
  const v = value.trim();
  if (!v) poll.delete(field);
  else put(doc, poll, field, v, pollOrder);
  return save(doc);
}

export type AskField = "group" | "approvers" | "requester" | "owner" | "message" | "timeout" | "on_timeout";

/** A flow list on one line: approvers, a question's options. */
function flowList(doc: Document, value: string[]): YAMLSeq {
  const list = doc.createNode(value) as YAMLSeq;
  list.flow = true;
  return list;
}

/** A step's wait for a person, from a document. */
function askOf(doc: Document, id: string): YAMLMap {
  const ask = own(doc, locate(doc, id).node, "ask");
  if (!isMap(ask)) throw new EditError(tr("composite.edit.noAsk"));
  // `ask: {}` as just added opens up as it fills.
  ask.flow = false;
  return ask;
}

/**
 * One field of a step's wait for a person. Empty or false removes it; without
 * a timeout, what it does and whom it escalates to go, and whom it escalates
 * to when it no longer does.
 */
export function setAskField(text: string, id: string, field: AskField, value: string | string[] | boolean): string {
  const doc = load(text);
  const ask = askOf(doc, id);
  const v = typeof value === "string" ? value.trim() : value;
  if (v === "" || v === false || (Array.isArray(v) && v.length === 0)) ask.delete(field);
  else put(doc, ask, field, Array.isArray(v) ? flowList(doc, v) : v, askOrder);
  if (field === "timeout" && !v) {
    ask.delete("on_timeout");
    ask.delete("escalate_to");
  }
  if (field === "on_timeout" && v !== "escalate") ask.delete("escalate_to");
  return save(doc);
}

/** Whom a wait escalates to: a group or people. Neither removes it. */
export function setAskEscalate(
  text: string,
  id: string,
  field: "group" | "approvers",
  value: string | string[],
): string {
  const doc = load(text);
  const ask = askOf(doc, id);
  if (!isMap(own(doc, ask, "escalate_to"))) put(doc, ask, "escalate_to", {}, askOrder);
  const to = own(doc, ask, "escalate_to") as YAMLMap;
  to.flow = false;
  const v = typeof value === "string" ? value.trim() : value;
  if (v.length === 0) to.delete(field);
  else put(doc, to, field, Array.isArray(v) ? flowList(doc, v) : v, ["group", "approvers"]);
  if (to.items.length === 0) ask.delete("escalate_to");
  return save(doc);
}

/** The next free key for a question of a form. */
function questionKey(form: YAMLSeq | undefined): string {
  const taken = new Set((form?.items ?? []).map((q) => (isMap(q) ? scalarText(q.get("key", true)) : "")));
  let n = 1;
  while (taken.has(`question_${n}`)) n++;
  return `question_${n}`;
}

/** Adds a question to a wait's form: text, until told otherwise. */
export function addAskQuestion(text: string, id: string): string {
  const doc = load(text);
  const ask = askOf(doc, id);
  const form = own(doc, ask, "form");
  const q = doc.createNode({ key: questionKey(isSeq(form) ? form : undefined), label: "", type: "string" });
  if (isSeq(form)) {
    form.flow = false;
    form.items.push(q);
  } else put(doc, ask, "form", doc.createNode([q]), askOrder);
  return save(doc);
}

/** Removes a question from a wait's form, and the form with its last question. */
export function removeAskQuestion(text: string, id: string, index: number): string {
  const doc = load(text);
  const ask = askOf(doc, id);
  const form = own(doc, ask, "form");
  if (!isSeq(form) || index < 0 || index >= form.items.length) throw new EditError(tr("composite.edit.noQuestion"));
  form.items.splice(index, 1);
  if (form.items.length === 0) ask.delete("form");
  return save(doc);
}

export type QuestionField = "key" | "label" | "type" | "required" | "options" | "default";

/**
 * One field of a question. Its key, text and type stay, for the step to say
 * what is missing; another type takes what the old one offered with it.
 */
export function setAskQuestion(
  text: string,
  id: string,
  index: number,
  field: QuestionField,
  value: string | number | boolean | string[] | undefined,
): string {
  const doc = load(text);
  const ask = askOf(doc, id);
  const form = own(doc, ask, "form");
  const q = isSeq(form) ? form.items[index] : undefined;
  if (!isMap(q)) throw new EditError(tr("composite.edit.noQuestion"));
  const v = typeof value === "string" ? value.trim() : value;
  if (field === "key" || field === "label" || field === "type") put(doc, q, field, v ?? "", questionOrder);
  else if (v === undefined || v === "" || v === false || (Array.isArray(v) && v.length === 0)) q.delete(field);
  else put(doc, q, field, Array.isArray(v) ? flowList(doc, v) : v, questionOrder);
  if (field === "type") {
    q.delete("default");
    if (v !== "choice") q.delete("options");
  }
  return save(doc);
}

/** A step's branch, from a document. */
function branchOf(doc: Document, id: string): YAMLMap {
  const branch = own(doc, locate(doc, id).node, "branch");
  if (!isMap(branch)) throw new EditError(tr("composite.edit.notBranch"));
  branch.flow = false;
  return branch;
}

function casesOf(doc: Document, branch: YAMLMap): YAMLSeq {
  let cases = own(doc, branch, "cases");
  if (!isSeq(cases)) {
    put(doc, branch, "cases", new YAMLSeq(), branchOrder);
    cases = branch.get("cases", true);
  }
  const seq = cases as YAMLSeq;
  seq.flow = false;
  return seq;
}

/** Adds a case to a branch, last: it says what is missing until its condition is written. */
export function addBranchCase(text: string, id: string): string {
  const doc = load(text);
  casesOf(doc, branchOf(doc, id)).items.push(doc.createNode({ when: "", steps: [] }));
  return save(doc);
}

/** Removes a case, and the steps on its side. */
export function removeBranchCase(text: string, id: string, index: number): string {
  const doc = load(text);
  const cases = casesOf(doc, branchOf(doc, id));
  if (index < 0 || index >= cases.items.length) throw new EditError(tr("composite.edit.noCase"));
  cases.items.splice(index, 1);
  return save(doc);
}

/** Moves a case one place earlier or later: the first whose condition holds is taken. */
export function moveBranchCase(text: string, id: string, index: number, dir: -1 | 1): string {
  const doc = load(text);
  const cases = casesOf(doc, branchOf(doc, id));
  const to = index + dir;
  if (index < 0 || index >= cases.items.length) throw new EditError(tr("composite.edit.noCase"));
  if (to < 0 || to >= cases.items.length)
    throw new EditError(dir < 0 ? tr("composite.edit.caseFirst") : tr("composite.edit.caseLast"));
  const [c] = cases.items.splice(index, 1);
  cases.items.splice(to, 0, c);
  return save(doc);
}

/** A case's title or condition. An empty title goes; an empty condition stays, for the step to say what is missing. */
export function setBranchCase(text: string, id: string, index: number, field: "title" | "when", value: string): string {
  const doc = load(text);
  const c = casesOf(doc, branchOf(doc, id)).items[index];
  if (!isMap(c)) throw new EditError(tr("composite.edit.noCase"));
  const v = value.trim();
  if (!v && field === "title") c.delete(field);
  else put(doc, c, field, v, caseOrder);
  return save(doc);
}

export type NotifyField = "requester" | "owner" | "groups" | "users" | "message";

/** One field of a message. Empty or false removes it, for the step to say what is missing. */
export function setNotifyField(
  text: string,
  id: string,
  field: NotifyField,
  value: string | string[] | boolean,
): string {
  const doc = load(text);
  const notify = own(doc, locate(doc, id).node, "notify");
  if (!isMap(notify)) throw new EditError(tr("composite.edit.noNotify"));
  // `notify: {}` as just added opens up as it fills.
  notify.flow = false;
  const v = typeof value === "string" ? value.trim() : value;
  if (v === "" || v === false || (Array.isArray(v) && v.length === 0)) notify.delete(field);
  else put(doc, notify, field, Array.isArray(v) ? flowList(doc, v) : v, notifyOrder);
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
):
  | {
      step: Raw;
      g: number;
      j: number | null;
      pointer: string;
      /** The side of a branch it is on: the branch's key, its place in it, and how many steps are there. */
      side?: { branch: string; taken: number; i: number; length: number };
      /**
       * The catch path it is on: what it catches (a step's key, "" for the
       * parallel group of slot g, "on_error" for the error handler), the
       * list's pointer, its place there and how many steps are there.
       */
      caught?: { of: string; list: string; i: number; length: number };
    }
  | undefined {
  // The text the graph and the designer parsed, read only.
  const { doc, error } = parseText(text);
  if (error) return undefined;
  try {
    const at = locate(doc, id);
    return {
      step: (at.node.toJS(doc) as Raw) ?? {},
      g: at.g,
      j: at.j,
      pointer: at.pointer,
      ...(at.side
        ? {
            side: {
              branch: at.side.key,
              taken: at.side.taken,
              i: at.side.index,
              length: at.side.list.items.length,
            },
          }
        : {}),
      ...(at.caught
        ? {
            caught: {
              of: at.caught.of,
              list: at.pointer.slice(0, at.pointer.lastIndexOf("/")),
              i: at.caught.index,
              length: at.caught.list.items.length,
            },
          }
        : {}),
    };
  } catch {
    return undefined;
  }
}
