// What the composite designer knows about a step (ADR-0022, ADR-0032): what
// it runs, among what the server says steps can run (GET
// /admin/catalog/composite-steps); where each of its inputs can come from at
// that point (the form, the request, the outputs of the steps before it: not
// those running at the same time, nor any for a VM step, which is planned
// when the request is submitted; a catch path's, those before what it
// catches and before it on the path); and what the server would refuse when
// the item is requested, said on the step before then.
import type {
  CatalogEntitlements,
  CompositeStepOutput,
  CompositeStepTarget,
  FieldError,
  Variable,
} from "@/lib/api/types";
import { eachStep, secretInMessages, variablesRead } from "@/lib/composite";
import { i18n } from "@/lib/i18n";

type Raw = Record<string, unknown>;

const tr = i18n.getFixedT(null, "admin-catalog");
const tc = i18n.getFixedT(null, "canvas");

const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});

export const stepRef = /\bsteps\.([a-z][a-z0-9_]*)/;

export interface StepTarget {
  kind: CompositeStepTarget["kind"] | "rest" | "ask" | "wait" | "poll" | "signal" | "branch" | "notify";
  ref: string;
  /** An AWX workflow template (awx.workflow). */
  workflow?: boolean;
  /** An AWX template's ID (awx.template_id): it is launched by it, whatever its name. */
  templateId?: number;
}

/** What a step names: an item, an AWX template, a custom workflow, a REST call, a wait for a person, a time, a condition or a signal, a branch, a message or a custom resource's type (the first, when it names several). */
export function targetOf(step: Raw): StepTarget | undefined {
  if (str(step.item)) return { kind: "item", ref: str(step.item) };
  const awx = obj(step.awx);
  if (step.awx !== undefined) {
    const id = typeof awx.template_id === "number" && awx.template_id > 0 ? awx.template_id : undefined;
    return {
      kind: "awx",
      ref: str(awx.job_template),
      workflow: awx.workflow === true,
      ...(id ? { templateId: id } : {}),
    };
  }
  if (str(step.workflow)) return { kind: "workflow", ref: str(step.workflow) };
  if (step.rest !== undefined) return { kind: "rest", ref: "" };
  if (step.ask !== undefined) return { kind: "ask", ref: "" };
  if (step.wait !== undefined) return { kind: "wait", ref: "" };
  if (step.poll !== undefined) return { kind: "poll", ref: "" };
  if (step.signal !== undefined) return { kind: "signal", ref: "" };
  if (step.branch !== undefined) return { kind: "branch", ref: "" };
  if (step.notify !== undefined) return { kind: "notify", ref: "" };
  if (step.resource !== undefined) return { kind: "resource", ref: str(obj(step.resource).type) };
  return undefined;
}

/** What a listed target is as a step names it. */
export function listedTarget(t: CompositeStepTarget): StepTarget {
  return {
    kind: t.kind,
    ref: t.ref,
    ...(t.awx_workflow ? { workflow: true } : {}),
    ...(t.awx_id ? { templateId: t.awx_id } : {}),
  };
}

/** A picker's value for a target, unique among them: a job template and a workflow template may share a name. */
export const targetValue = (t: StepTarget) => `${t.kind === "awx" && t.workflow ? "awx-workflow" : t.kind}:${t.ref}`;

/**
 * The listed target a step names. An AWX template is a job template or a
 * workflow template as the step says, found by its ID when the step gives
 * one (it is launched by it), else by its name.
 */
export function findTarget(targets: CompositeStepTarget[], t: StepTarget | undefined) {
  if (!t) return undefined;
  if (t.kind !== "awx") return targets.find((x) => x.kind === t.kind && x.ref === t.ref);
  const awx = targets.filter((x) => x.kind === "awx" && Boolean(x.awx_workflow) === Boolean(t.workflow));
  return t.templateId ? awx.find((x) => x.awx_id === t.templateId) : awx.find((x) => x.ref === t.ref);
}

/**
 * The codes of the projects that may request the composite (its
 * entitlements: every project, or those listed) and that a target isn't
 * offered to (its project_ids). Empty when it is offered to each of them.
 */
export function notOfferedTo(t: CompositeStepTarget | undefined, offered: CatalogEntitlements | undefined): string[] {
  const limits = t?.project_ids;
  if (!limits?.length || !offered) return [];
  const projects = offered.projects ?? [];
  const ids = offered.all_projects ? projects.map((p) => p.id) : offered.project_ids;
  const code = (id: string) => projects.find((p) => p.id === id)?.code ?? id;
  return ids
    .filter((id) => !limits.includes(id))
    .map(code)
    .sort();
}

/** What a REST call gives the steps after it (its answer's headers never). */
export const restOutputs = (): CompositeStepOutput[] => [
  { name: "status", type: "integer", description: tr("composite.out.restStatus") },
  { name: "json", type: "object", description: tr("composite.out.restJson") },
];

/** What a wait for a time gives the steps after it. */
export const waitOutputs = (): CompositeStepOutput[] => [
  { name: "until", type: "string", description: tr("composite.out.waitUntil") },
];

/** What a poll gives the steps after it: its last answer, and whether its condition held (ADR-0033). */
export const pollOutputs = (): CompositeStepOutput[] => [
  { name: "status", type: "integer", description: tr("composite.out.pollStatus") },
  { name: "json", type: "object", description: tr("composite.out.pollJson") },
  { name: "met", type: "boolean", description: tr("composite.out.pollMet") },
];

/** What a branch gives the steps after it: which side it took (ADR-0033). */
export const branchOutputs = (): CompositeStepOutput[] => [
  { name: "case", type: "integer", description: tr("composite.out.branchCase") },
];

/** What a message gives the steps after it: never what it said (ADR-0033). */
export const notifyOutputs = (): CompositeStepOutput[] => [
  { name: "told", type: "integer", description: tr("composite.out.notifyTold") },
];

const answerType: Record<string, string> = { number: "number", boolean: "boolean" };

/** What a wait for a person gives the steps after it: each of its form's answers by its key (ADR-0033). */
export function askOutputs(ask: Raw): CompositeStepOutput[] {
  const form = Array.isArray(ask.form) ? (ask.form as Raw[]) : [];
  return [
    { name: "answered", type: "boolean", description: tr("composite.out.askAnswered") },
    { name: "by", type: "string", description: tr("composite.out.askBy") },
    { name: "comment", type: "string", description: tr("composite.out.askComment") },
    ...form
      .filter((f) => str(f.key))
      .map((f) => ({
        name: `answer.${str(f.key)}`,
        type: answerType[str(f.type)] ?? "string",
        description: str(f.label) || str(f.key),
      })),
  ];
}

/** What a loop gives the steps after it, whatever its step gives (ADR-0057). */
export const loopOutputs = (): CompositeStepOutput[] => [
  { name: "items", type: "array", description: tr("composite.out.loopItems") },
  { name: "count", type: "integer", description: tr("composite.out.loopCount") },
];

/** What a step whose catch path ran gives the steps after it: read it with has() (ADR-0057). */
export const caughtOutputs = (): CompositeStepOutput[] => [
  { name: "caught", type: "boolean", description: tr("composite.out.caught") },
  { name: "error", type: "object", description: tr("composite.out.error") },
];

/**
 * What a step gives the steps after it: its target's outputs, a REST call's,
 * a wait's, a poll's, or an AWX job's ID for a template not listed. A loop
 * gives its passes' instead; a step with a catch path, what failed when it did.
 * A wait for a signal gives what came, whatever it is.
 */
export function outputsOf(
  step: Raw,
  targets: CompositeStepTarget[],
): { outputs: CompositeStepOutput[]; open: boolean } {
  const own = ownOutputs(step, targets);
  const caught = Array.isArray(step.catch) && step.catch.length > 0 ? caughtOutputs() : [];
  if (step.for_each !== undefined) return { outputs: [...loopOutputs(), ...caught], open: false };
  return { outputs: [...own.outputs, ...caught], open: own.open };
}

function ownOutputs(step: Raw, targets: CompositeStepTarget[]): { outputs: CompositeStepOutput[]; open: boolean } {
  const t = targetOf(step);
  const found = findTarget(targets, t);
  if (found) return { outputs: found.outputs, open: found.open_outputs };
  if (t?.kind === "rest") return { outputs: restOutputs(), open: false };
  if (t?.kind === "ask") return { outputs: askOutputs(obj(step.ask)), open: false };
  if (t?.kind === "wait") return { outputs: waitOutputs(), open: false };
  if (t?.kind === "poll") return { outputs: pollOutputs(), open: false };
  if (t?.kind === "branch") return { outputs: branchOutputs(), open: false };
  if (t?.kind === "notify") return { outputs: notifyOutputs(), open: false };
  if (t?.kind === "signal") return { outputs: [], open: true };
  if (t?.kind === "awx")
    return { outputs: [{ name: "job_id", type: "integer", description: tr("composite.out.jobId") }], open: false };
  return { outputs: [], open: t?.kind === "workflow" || t?.kind === "resource" };
}

/** A step of the blueprint, flattened: its slot, its place in a parallel group, on a side of a branch or on a catch path, its pointer. */
export interface FlatStep {
  step: Raw;
  key: string;
  /** What the server calls it: its title, else its key. */
  label: string;
  /** What the canvas calls it: its title, an AWX step's title, else its key. */
  name: string;
  g: number;
  j: number | null;
  pointer: string;
  /** The side of a branch it is on: 1 for the first case, 0 for otherwise, and its place there. */
  side?: { taken: number; i: number };
  /** The catch path it is on, or the error handler (slot: after the last): the list's pointer, and its place there. */
  caught?: { list: string; i: number };
}

/** Where a step is, for what it can read. */
export interface StepPlace {
  g: number;
  side?: { taken: number; i: number };
  caught?: { list: string; i: number };
}

export function flatten(blueprint: Raw, prefix = "/definition/blueprint"): FlatStep[] {
  const out: FlatStep[] = [];
  const steps = Array.isArray(blueprint.steps) ? (blueprint.steps as unknown[]) : [];
  const push = (
    s: unknown,
    g: number,
    j: number | null,
    pointer: string,
    side?: FlatStep["side"],
    caught?: FlatStep["caught"],
  ) => {
    const step = obj(s);
    const key = str(step.key);
    const awx = obj(step.awx);
    out.push({
      step,
      key,
      // The server's words for it, as its problems say them.
      label: str(step.title) || key || "(no key)",
      name: str(step.title) || str(awx.title) || key || tc("composite.noKey"),
      g,
      j,
      pointer,
      ...(side ? { side } : {}),
      ...(caught ? { caught } : {}),
    });
  };
  const catchPath = (list: unknown, g: number, at: string) => {
    if (Array.isArray(list)) list.forEach((s, i) => push(s, g, null, `${at}/${i}`, undefined, { list: at, i }));
  };
  steps.forEach((raw, g) => {
    const slot = obj(raw);
    const list = Array.isArray(slot.parallel) ? (slot.parallel as unknown[]) : null;
    const flat = (s: unknown, j: number | null, pointer: string, side?: FlatStep["side"]) => {
      push(s, g, j, pointer, side);
      if (j === null) catchPath(obj(s).catch, g, `${pointer}/catch`);
    };
    (list ?? [slot]).forEach((s, j) =>
      flat(s, list ? j : null, list ? `${prefix}/steps/${g}/parallel/${j}` : `${prefix}/steps/${g}`),
    );
    if (list) catchPath(slot.catch, g, `${prefix}/steps/${g}/catch`);
    if (list || slot.branch === undefined) return;
    const branch = obj(slot.branch);
    const onSide = (steps: unknown, taken: number, at: string) => {
      if (Array.isArray(steps)) steps.forEach((s, i) => flat(s, null, `${at}/${i}`, { taken, i }));
    };
    if (Array.isArray(branch.cases))
      branch.cases.forEach((c, i) => onSide(obj(c).steps, i + 1, `${prefix}/steps/${g}/branch/cases/${i}/steps`));
    onSide(branch.else, 0, `${prefix}/steps/${g}/branch/else`);
  });
  catchPath(blueprint.on_error, steps.length, `${prefix}/on_error`);
  return out;
}

/**
 * The steps that one at a place can read. Those of the slots before its own
 * (a step on a side of a branch among them: it reads as one `when` skipped,
 * and a catch path's: as one skipped when nothing failed); on a side, also
 * its branch and the steps before it there, not another side's. On a catch
 * path, those of the slots before what it catches and the steps before it
 * on the path.
 */
export function earlierSteps(blueprint: Raw, at: StepPlace): FlatStep[] {
  return flatten(blueprint).filter((x) => {
    if (at.caught) return x.caught?.list === at.caught.list ? x.caught.i < at.caught.i : x.g < at.g;
    if (x.g < at.g) return true;
    if (x.g !== at.g || !at.side || x.caught) return false;
    return x.side ? x.side.taken === at.side.taken && x.side.i < at.side.i : x.j === null;
  });
}

/** The inputs of the composite's own form, in the order its sections show them. */
export function formInputsOf(def: Raw): { name: string; title: string }[] {
  const props = obj(obj(def.form_schema).properties);
  const ui = obj(def.ui_schema);
  const fields = obj(ui.fields);
  const sections = Array.isArray(obj(ui.layout).sections) ? (obj(ui.layout).sections as unknown[]) : [];
  const order = [
    ...sections.flatMap((s) => (Array.isArray(obj(s).fields) ? (obj(s).fields as unknown[]).map(str) : [])),
    ...Object.keys(props),
  ];
  return [...new Set(order)]
    .filter((n) => n && n in props && obj(props[n]).readOnly !== true)
    .map((name) => ({ name, title: str(obj(fields[name]).label) || str(obj(props[name]).title) || name }));
}

export interface Source {
  /** The CEL a binding to it is. */
  value: string;
  label: string;
  /** The picker's group, in the language shown. */
  group: string;
}

/**
 * Where an input of the step at `at` can come from: the form's inputs, the
 * request's environment and project, the outputs of the steps before it
 * (earlierSteps; none for a planned step: its VMs are planned from the form
 * when the request is submitted) and the shared variables (ADR-0057).
 */
export function bindingSources(
  def: Raw,
  at: StepPlace & { planned: boolean },
  targets: CompositeStepTarget[],
  variables: Pick<Variable, "key" | "description">[] = [],
): Source[] {
  const out: Source[] = formInputsOf(def).map((i) => ({
    value: `inputs.${i.name}`,
    label: i.title,
    group: tr("composite.source.form"),
  }));
  if (!at.planned)
    for (const s of earlierSteps(obj(def.blueprint), at).filter((x) => x.key))
      for (const o of outputsOf(s.step, targets).outputs)
        out.push({
          value: `steps.${s.key}.${o.name}`,
          label: tr("composite.source.output", { step: s.name, output: o.description }),
          group: tr("composite.source.steps"),
        });
  out.push(
    { value: "environment", label: tr("composite.source.environment"), group: tr("composite.source.request") },
    { value: "project.code", label: tr("composite.source.projectCode"), group: tr("composite.source.request") },
    { value: "project.name", label: tr("composite.source.projectName"), group: tr("composite.source.request") },
    { value: "project.id", label: tr("composite.source.projectId"), group: tr("composite.source.request") },
    ...variables.map((v): Source => ({
      value: `vars.${v.key}`,
      label: v.description || v.key,
      group: tr("composite.source.variables"),
    })),
  );
  return out;
}

/** The steps before the step at `at` whose outputs are open (custom workflows): a binding to them is written as CEL. */
export function openEarlier(def: Raw, at: StepPlace, targets: CompositeStepTarget[]): string[] {
  return earlierSteps(obj(def.blueprint), at)
    .filter((s) => s.key && outputsOf(s.step, targets).open)
    .map((s) => s.key);
}

/** Projects' codes in a sentence: the first three, then how many more. */
function listing(codes: string[]) {
  const shown = codes.length > 4 ? [...codes.slice(0, 3), `${codes.length - 3} other projects`] : codes;
  return shown.length > 1 ? `${shown.slice(0, -1).join(", ")} and ${shown.at(-1)}` : (shown[0] ?? "");
}

/**
 * What the server would refuse when the item is requested (core.evaluateComposite),
 * in its words, with the pointer of what it is about: an item that isn't
 * published (or doesn't exist, or is a composite, or a workflow item whose
 * workflow can't start), a custom workflow not registered for composite
 * steps, one not offered to a project the composite is offered to (its
 * entitlements, `offered`: a request from that project is refused), and a
 * VM step with a condition, an undo, a loop, a catch path or an input from a
 * step, on a side of a branch or on a catch path, or in a parallel group
 * with a catch path. Known only once the targets are loaded. A message's
 * secret input is the form's business too: the server refuses it on save.
 * And, once the shared variables are loaded, a read of one that doesn't
 * exist, which the server refuses to publish (core.checkVariableRefs).
 */
export function designerProblems(
  def: Raw,
  targets: CompositeStepTarget[],
  offered?: CatalogEntitlements,
  variables?: Variable[],
): FieldError[] {
  const out: FieldError[] = secretInMessages(def);
  const add = (field: string, code: string, message: string) => out.push({ field, code, message });
  const notOffered = (field: string, what: string, t: CompositeStepTarget | undefined) => {
    const missing = notOfferedTo(t, offered);
    if (missing.length > 0)
      add(field, "not_available", `${what} isn't offered to ${listing(missing)}, which may request this item.`);
  };
  for (const s of flatten(obj(def.blueprint))) {
    const t = targetOf(s.step);
    const found = findTarget(targets, t);
    if (t?.kind === "item" && !found)
      add(`${s.pointer}/item`, "not_available", `${s.label} uses ${t.ref}, which isn't published.`);
    if (t?.kind === "item") notOffered(`${s.pointer}/item`, `${s.label}: ${t.ref}'s custom workflow`, found);
    if (t?.kind === "workflow" && !found)
      add(
        `${s.pointer}/workflow`,
        "not_available",
        `${s.label}: no custom workflow ${t.ref} is registered for composite steps.`,
      );
    if (t?.kind === "workflow") notOffered(`${s.pointer}/workflow`, `${s.label}: the custom workflow ${t.ref}`, found);
    const undo = obj(s.step.undo);
    const undoFlow = str(undo.workflow);
    const undoTarget = targets.find((x) => x.kind === "workflow" && x.ref === undoFlow);
    if (undoFlow && !undoTarget)
      add(
        `${s.pointer}/undo/workflow`,
        "not_available",
        `${s.label} (undo): no custom workflow ${undoFlow} is registered for composite steps.`,
      );
    if (undoFlow)
      notOffered(`${s.pointer}/undo/workflow`, `${s.label} (undo): the custom workflow ${undoFlow}`, undoTarget);
    if (!found?.planned) continue;
    if (s.side || s.caught)
      add(
        `${s.pointer}/item`,
        "invalid",
        `${s.label}: a VM is planned when the request is submitted, before a branch chooses or a step fails, so it can't be on a side of a branch or on a catch path.`,
      );
    if (s.step.for_each !== undefined)
      add(
        `${s.pointer}/for_each`,
        "invalid",
        `${s.label}: a VM step can't loop: its VMs are planned when the request is submitted. Give its item a count instead.`,
      );
    if (Array.isArray(s.step.catch) && s.step.catch.length > 0)
      add(
        `${s.pointer}/catch`,
        "invalid",
        `${s.label}: a VM step has no catch path: what it built is the rollback's to delete, so the request can't go on past it.`,
      );
    const slot = obj((obj(def.blueprint).steps as unknown[] | undefined)?.[s.g]);
    if (s.j !== null && Array.isArray(slot.catch) && slot.catch.length > 0)
      add(
        `${s.pointer.slice(0, s.pointer.lastIndexOf("/parallel/"))}/catch`,
        "invalid",
        `Step group ${s.g + 1} has a VM step, so it has no catch path: what it built is the rollback's to delete, so the request can't go on past it.`,
      );
    if (s.step.undo !== undefined)
      add(`${s.pointer}/undo`, "invalid", `${s.label}: a VM isn't undone by a job or workflow; a failure deletes it.`);
    if (str(s.step.when))
      add(
        `${s.pointer}/when`,
        "invalid",
        `${s.label}: a VM step has no condition: its VMs are planned when the request is submitted.`,
      );
    for (const [k, v] of Object.entries(obj(s.step.with)))
      if (typeof v === "string" && stepRef.test(v))
        add(
          `${s.pointer}/with/${k}`,
          "invalid",
          `${s.label}: a VM is planned when the request is submitted, so its inputs come from the form, not from steps.`,
        );
  }
  if (variables) {
    const known = new Set(variables.map((v) => v.key));
    eachStep(def.blueprint, "/definition/blueprint", (st, at) => {
      for (const key of variablesRead(st))
        if (!known.has(key))
          add(at, "unknown_variable", `There is no variable vars.${key}: add it under Administration > Variables.`);
    });
  }
  return out;
}
