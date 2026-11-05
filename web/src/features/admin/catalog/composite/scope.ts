// What the composite designer knows about a step (ADR-0022, ADR-0032): what
// it runs, among what the server says steps can run (GET
// /admin/catalog/composite-steps); where each of its inputs can come from at
// that point (the form, the request, the outputs of the steps before it: not
// those running at the same time, nor any for a VM step, which is planned
// when the request is submitted); and what the server would refuse when the
// item is requested, said on the step before then.
import type { CompositeStepOutput, CompositeStepTarget, FieldError } from "@/lib/api/types";

type Raw = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});

export const stepRef = /\bsteps\.([a-z][a-z0-9_]*)/;

export interface StepTarget {
  kind: CompositeStepTarget["kind"];
  ref: string;
  workflow?: boolean;
}

/** What a step names: an item, an AWX template or a custom workflow (the first, when it names several). */
export function targetOf(step: Raw): StepTarget | undefined {
  if (str(step.item)) return { kind: "item", ref: str(step.item) };
  const awx = obj(step.awx);
  if (step.awx !== undefined) return { kind: "awx", ref: str(awx.job_template), workflow: awx.workflow === true };
  if (str(step.workflow)) return { kind: "workflow", ref: str(step.workflow) };
  return undefined;
}

export const targetValue = (t: StepTarget) => `${t.kind}:${t.ref}`;

export function findTarget(targets: CompositeStepTarget[], t: StepTarget | undefined) {
  return t ? targets.find((x) => x.kind === t.kind && x.ref === t.ref) : undefined;
}

/** What a step gives the steps after it: its target's outputs, or an AWX job's ID for a template not listed. */
export function outputsOf(
  step: Raw,
  targets: CompositeStepTarget[],
): { outputs: CompositeStepOutput[]; open: boolean } {
  const t = targetOf(step);
  const found = findTarget(targets, t);
  if (found) return { outputs: found.outputs, open: found.open_outputs };
  if (t?.kind === "awx")
    return { outputs: [{ name: "job_id", type: "integer", description: "The AWX job's ID" }], open: false };
  return { outputs: [], open: t?.kind === "workflow" };
}

/** A step of the blueprint, flattened: its slot, its place in a parallel group, its pointer. */
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
}

export function flatten(blueprint: Raw, prefix = "/definition/blueprint"): FlatStep[] {
  const out: FlatStep[] = [];
  const steps = Array.isArray(blueprint.steps) ? (blueprint.steps as unknown[]) : [];
  steps.forEach((raw, g) => {
    const slot = obj(raw);
    const list = Array.isArray(slot.parallel) ? (slot.parallel as unknown[]) : null;
    (list ?? [slot]).forEach((s, j) => {
      const step = obj(s);
      const key = str(step.key);
      const awx = obj(step.awx);
      out.push({
        step,
        key,
        label: str(step.title) || key || "(no key)",
        name: str(step.title) || str(awx.title) || key || "(no key)",
        g,
        j: list ? j : null,
        pointer: list ? `${prefix}/steps/${g}/parallel/${j}` : `${prefix}/steps/${g}`,
      });
    });
  });
  return out;
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
  group: "The form" | "Earlier steps" | "The request";
}

/**
 * Where an input of the step at `at` can come from: the form's inputs, the
 * request's environment and project, and the outputs of the steps in the
 * slots before its own (none for a planned step: its VMs are planned from
 * the form when the request is submitted).
 */
export function bindingSources(
  def: Raw,
  at: { g: number; planned: boolean },
  targets: CompositeStepTarget[],
): Source[] {
  const out: Source[] = formInputsOf(def).map((i) => ({
    value: `inputs.${i.name}`,
    label: i.title,
    group: "The form",
  }));
  if (!at.planned)
    for (const s of flatten(obj(def.blueprint)).filter((x) => x.g < at.g && x.key))
      for (const o of outputsOf(s.step, targets).outputs)
        out.push({ value: `steps.${s.key}.${o.name}`, label: `${s.name}: ${o.description}`, group: "Earlier steps" });
  out.push(
    { value: "environment", label: "The environment's key", group: "The request" },
    { value: "project.code", label: "The project's code", group: "The request" },
    { value: "project.name", label: "The project's name", group: "The request" },
    { value: "project.id", label: "The project's ID", group: "The request" },
  );
  return out;
}

/** The steps before the step at `at` whose outputs are open (custom workflows): a binding to them is written as CEL. */
export function openEarlier(def: Raw, g: number, targets: CompositeStepTarget[]): string[] {
  return flatten(obj(def.blueprint))
    .filter((s) => s.g < g && s.key && outputsOf(s.step, targets).open)
    .map((s) => s.key);
}

/**
 * What the server would refuse when the item is requested (core.evaluateComposite),
 * in its words, with the pointer of what it is about: an item that isn't
 * published (or doesn't exist, or is a composite), a custom workflow not
 * registered for composite steps, and a VM step with a condition, an undo
 * or an input from a step. Known only once the targets are loaded.
 */
export function designerProblems(def: Raw, targets: CompositeStepTarget[]): FieldError[] {
  const out: FieldError[] = [];
  const add = (field: string, code: string, message: string) => out.push({ field, code, message });
  for (const s of flatten(obj(def.blueprint))) {
    const t = targetOf(s.step);
    const found = findTarget(targets, t);
    if (t?.kind === "item" && !found)
      add(`${s.pointer}/item`, "not_available", `${s.label} uses ${t.ref}, which isn't published.`);
    if (t?.kind === "workflow" && !found)
      add(
        `${s.pointer}/workflow`,
        "not_available",
        `${s.label}: no custom workflow ${t.ref} is registered for composite steps.`,
      );
    const undo = obj(s.step.undo);
    const undoFlow = str(undo.workflow);
    if (undoFlow && !targets.some((x) => x.kind === "workflow" && x.ref === undoFlow))
      add(
        `${s.pointer}/undo/workflow`,
        "not_available",
        `${s.label} (undo): no custom workflow ${undoFlow} is registered for composite steps.`,
      );
    if (!found?.planned) continue;
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
  return out;
}
