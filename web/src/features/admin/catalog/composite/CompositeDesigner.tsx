// The composite designer (ADR-0032): a composite draft's steps on the
// canvas, edited there or in the YAML beside it, the one document both
// change. Each edit on the canvas is made on the YAML (edits.ts) and written
// into the editor as one step of its history, so Undo and Redo go back
// through typed and designed changes alike, and the comments stay.
//
// Everything is reachable from the keyboard: the canvas (or its list view)
// selects a step, whose details below it hold every edit of it (moves,
// removal, what it runs, its condition, its inputs' bindings, its undo);
// Add a step, Undo and Redo sit above it. Ctrl+Z and Ctrl+Shift+Z (or
// Ctrl+Y) undo and redo outside the text fields.
//
// An input is bound through a picker that offers what exists at that point:
// the form's inputs, the request's environment and project, and the outputs
// of the steps before it (not those running at the same time; none for a
// VM step, planned from the form when the request is submitted), or a
// value or CEL expression written out. A REST call has no inputs: its URL and
// body read them, and it says what it can read. A wait for a person has neither
// inputs nor an undo: who it asks, what, and for how long. A branch has its cases
// and the steps on each side, added from its details; those steps are selected
// and edited as any other, and move within their side. A message says who it
// tells and what (ADR-0033). A step repeats over a list, waits for a signal or
// an event, and has a catch path (its group's, in a parallel group), whose
// steps are added from its details as a side's are; the error handler's are
// added from Done's (ADR-0057).
import { useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { type CanvasNode, NodeDetails, within } from "@/components/canvas";
import {
  type Design,
  HistoryButtons,
  legendClass as legend,
  PanelHeading,
  panelClass as panel,
  type Then,
  useDesigner,
  useFocusNext,
} from "@/components/canvas/designer";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input, Textarea } from "@/components/ui/input";
import { type Option, Select } from "@/components/ui/select";
import type {
  CatalogEntitlements,
  CompositeStepInput,
  CompositeStepOutput,
  CompositeStepTarget,
} from "@/lib/api/types";
import { eventTopics, loopRefused, maxStepMessage } from "@/lib/composite";
import { i18n } from "@/lib/i18n";
import { adm, q } from "@/lib/query";
import { cn } from "@/lib/utils";
import DefinitionGraph from "../DefinitionGraph";
import { parseText } from "../definitionYaml";
import { CommitInput } from "../designer/Properties";
import * as edits from "./edits";
import {
  bindingSources,
  designerProblems,
  findTarget,
  flatten,
  formInputsOf,
  listedTarget,
  openEarlier,
  outputsOf,
  type Source,
  type StepTarget,
  targetOf,
  targetValue,
} from "./scope";

type Raw = Record<string, unknown>;

const str = (v: unknown) => (typeof v === "string" ? v : "");
const obj = (v: unknown): Raw => (v && typeof v === "object" && !Array.isArray(v) ? (v as Raw) : {});
const keyPattern = /^[a-z][a-z0-9_]{0,40}$/;
const variablePattern = /^[A-Za-z_][A-Za-z0-9_]*$/;
const tr = i18n.getFixedT(null, "admin-catalog");

/** Code in a sentence: a path, an expression or a name, as written. */
const Code = ({ text }: { text: string }) => <code className="font-mono">{text}</code>;

/** Codes in a sentence, separated by commas. */
function Codes({ list }: { list: string[] }) {
  return (
    <>
      {list.map((r, i) => (
        <span key={r}>
          {i > 0 && ", "}
          <code className="font-mono">{r}</code>
        </span>
      ))}
    </>
  );
}

/** What a catch path's steps read of the failure. */
const errorCodes = { step: <Code text="error.step" />, message: <Code text="error.message" /> };

/** The examples fields show, written as the YAML has them. */
const examples = {
  when: 'inputs.environment == "prd"',
  url: "https://cmdb.corp.example/api/ci/${steps.vm.name}",
  body: "{'name': steps.vm.fqdn, 'owner': inputs.owner}",
  keep: "$.result",
  duration: "72h",
  window: "inputs.window",
  every: "10m",
  event: "item.slug == 'backup-host'",
  signal: "cab-approved",
  items: "inputs.hosts",
  caseWhen: "inputs.size == 'L'",
  askTimeout: "48h",
};
const pollExample = (own: string) => `${own}.json == 'approved'`;

/** The definition in a text, or undefined when it doesn't parse. Shared with the graph's: read only. */
function parseDefinition(text: string): Raw | undefined {
  const d = parseText(text).value;
  return d && typeof d === "object" ? (d as Raw) : undefined;
}

/** What a binding written out is: a number or a boolean as such, anything else a CEL expression; empty unbinds. */
export function writtenBinding(text: string): edits.Binding {
  const t = text.trim();
  if (!t) return undefined;
  if (/^-?\d+(\.\d+)?$/.test(t)) return Number(t);
  if (t === "true" || t === "false") return t === "true";
  return t;
}

const shown = (v: unknown) => (v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v));

export type { Design };

type KindGroup = "items" | "awx" | "workflows" | "rest" | "people" | "waits" | "way" | "resources";
const kindGroups: Record<StepTarget["kind"], KindGroup> = {
  item: "items",
  awx: "awx",
  workflow: "workflows",
  rest: "rest",
  ask: "people",
  wait: "waits",
  poll: "waits",
  signal: "waits",
  branch: "way",
  notify: "people",
  resource: "resources",
};
const kindGroup = (kind: StepTarget["kind"]) => tr(`composite.kind.${kindGroups[kind]}`);
const byName = "awx-by-name";
const restValue = targetValue({ kind: "rest", ref: "" });
const askValue = targetValue({ kind: "ask", ref: "" });
const waitValue = targetValue({ kind: "wait", ref: "" });
const pollValue = targetValue({ kind: "poll", ref: "" });
const signalValue = targetValue({ kind: "signal", ref: "" });
const branchValue = targetValue({ kind: "branch", ref: "" });
const notifyValue = targetValue({ kind: "notify", ref: "" });
const restMethods = ["GET", "POST", "PUT", "PATCH", "DELETE"];
// A poll repeats its call: only these may be repeated (ADR-0033).
const pollMethods = ["GET", "PUT", "DELETE"];

/**
 * What a step can run, for a picker: grouped, the step's own first if it
 * isn't listed. A branch only where it can go; on a catch path, nothing that
 * waits and no custom resource.
 */
function targetOptions(
  targets: CompositeStepTarget[],
  current?: StepTarget,
  branchHere = false,
  onCatch = false,
): Option[] {
  const out: Option[] = targets
    .filter((t) => !onCatch || t.kind !== "resource")
    .map((t) => ({
      value: targetValue(listedTarget(t)),
      label: t.title,
      meta: t.awx_workflow ? tr("composite.target.workflow", { ref: t.ref }) : t.ref,
      group: kindGroup(t.kind),
    }));
  if (current?.ref && !findTarget(targets, current))
    out.unshift({
      value: targetValue(current),
      label: current.ref,
      meta:
        current.kind === "item"
          ? tr("composite.target.notPublished")
          : current.kind === "awx"
            ? tr("composite.target.notImported")
            : current.kind === "resource"
              ? tr("composite.target.notDefined")
              : tr("composite.target.notRegistered"),
      group: kindGroup(current.kind),
    });
  out.push({ value: byName, label: tr("composite.target.byName"), group: kindGroup("awx") });
  out.push({ value: restValue, label: tr("composite.target.rest"), group: kindGroup("rest") });
  if (!onCatch) out.push({ value: askValue, label: tr("composite.target.ask"), group: kindGroup("ask") });
  out.push({ value: notifyValue, label: tr("composite.target.notify"), group: kindGroup("notify") });
  if (!onCatch) {
    out.push({ value: waitValue, label: tr("composite.target.wait"), group: kindGroup("wait") });
    out.push({ value: pollValue, label: tr("composite.target.poll"), group: kindGroup("poll") });
    out.push({ value: signalValue, label: tr("composite.target.signal"), group: kindGroup("signal") });
  }
  if (branchHere) out.push({ value: branchValue, label: tr("composite.target.branch"), group: kindGroup("branch") });
  return out;
}

/** The target a picker's value names (targetValue): a listed AWX template with its ID. */
function parseTarget(value: string, targets: CompositeStepTarget[]): edits.Target | undefined {
  const i = value.indexOf(":");
  const kind = value.slice(0, i);
  const ref = value.slice(i + 1);
  if (kind === "awx" || kind === "awx-workflow") {
    const workflow = kind === "awx-workflow";
    const t = targets.find((x) => x.kind === "awx" && x.ref === ref && Boolean(x.awx_workflow) === workflow);
    return { kind: "awx", ref, workflow, ...(t?.awx_id ? { templateId: t.awx_id } : {}) };
  }
  if (
    kind === "rest" ||
    kind === "ask" ||
    kind === "wait" ||
    kind === "poll" ||
    kind === "signal" ||
    kind === "branch" ||
    kind === "notify"
  )
    return { kind, ref: "" };
  if (kind !== "item" && kind !== "workflow" && kind !== "resource") return undefined;
  return { kind, ref };
}

/** The step a target makes, its inputs bound to the form's inputs of the same name. */
function newStep(key: string, title: string, target: edits.Target, inputs: CompositeStepInput[], def: Raw): Raw {
  const step: Raw = { key };
  if (title.trim()) step.title = title.trim();
  if (target.kind === "awx")
    step.awx = {
      job_template: target.ref,
      ...(target.templateId ? { template_id: target.templateId } : {}),
      ...(target.workflow ? { workflow: true } : {}),
    };
  else if (target.kind === "rest") step.rest = edits.newRestCall("GET");
  else if (target.kind === "ask") step.ask = {};
  else if (target.kind === "wait") step.wait = {};
  else if (target.kind === "poll") step.poll = edits.newPoll();
  else if (target.kind === "signal") step.signal = edits.newSignal(key);
  else if (target.kind === "branch") step.branch = edits.newBranch();
  else if (target.kind === "notify") step.notify = {};
  else if (target.kind === "resource") step.resource = { type: target.ref };
  else step[target.kind] = target.ref;
  const form = new Set(formInputsOf(def).map((i) => i.name));
  const bound = Object.fromEntries(inputs.filter((i) => form.has(i.name)).map((i) => [i.name, `inputs.${i.name}`]));
  if (Object.keys(bound).length > 0) step.with = bound;
  return step;
}

export default function CompositeDesigner({
  name,
  design,
  entitlements,
}: {
  name: string;
  design: Design;
  /** Who may request the composite: a step whose target isn't offered to them all is flagged. */
  entitlements?: CatalogEntitlements;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text } = design;
  const targetsQuery = useQuery(q.compositeStepTargets());
  const targets = useMemo(() => targetsQuery.data?.items ?? [], [targetsQuery.data]);
  const def = useMemo(() => parseDefinition(text), [text]);
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  /** Where the step being added goes when it was added from a step's or Done's details. */
  const [addingTo, setAddingTo] = useState<Into | null>(null);
  const addInto = useCallback((into: Into) => {
    setAddingTo(into);
    setAdding(true);
  }, []);

  const server = design.serverProblems;
  // Not for those who may not read them (403): the server says it when publishing.
  const variables = useQuery({ ...adm.variables(), staleTime: 60_000, retry: false });
  const problemsFor = useCallback(
    (t: string) => {
      const d = parseDefinition(t);
      const own =
        d && targetsQuery.data ? designerProblems(d, targetsQuery.data.items, entitlements, variables.data?.items) : [];
      // The steps' only: the rest are on their lines in the editor. One the designer says already isn't said twice.
      const refused =
        server && server.text === t
          ? server.errors.filter(
              (e) =>
                within(e.field, edits.blueprintPrefix) &&
                !own.some((o) => o.field === e.field && o.message === e.message),
            )
          : [];
      return [...own, ...refused];
    },
    [targetsQuery.data, server, entitlements, variables.data],
  );

  const { box, focusNext, apply } = useDesigner(design, setSelected);

  const ctx: PanelContext = { text, def: def ?? {}, targets, targetsQuery, apply, focusNext, setSelected, addInto };
  const renderDetails = (node: CanvasNode): ReactNode => {
    if (!def)
      return (
        <div className="flex flex-col gap-2">
          <p role="note" className="text-secondary text-warn">
            {t("composite.noParse")}
          </p>
          <NodeDetails node={node} />
        </div>
      );
    if (node.kind === "step") return <StepPanel key={node.id} node={node} ctx={ctx} />;
    if (node.kind === "inputs") return <InputsPanel key={node.id} ctx={ctx} />;
    if (node.kind === "end") return <EndPanel key={node.id} ctx={ctx} />;
    return <NodeDetails node={node} />;
  };

  const selectedStep = selected?.startsWith("step") ? selected : undefined;
  const actions = (
    <>
      <Button
        size="sm"
        disabled={!def}
        onClick={() => {
          setAddingTo(null);
          setAdding(true);
        }}
      >
        <Plus aria-hidden />
        {t("composite.addStep")}
      </Button>
      <HistoryButtons design={design} />
    </>
  );

  return (
    <div ref={box} tabIndex={-1} className="min-w-0 outline-none">
      <DefinitionGraph
        text={text}
        name={name}
        composite
        problemsFor={problemsFor}
        canvas={{ selected, onSelect: setSelected, actions, renderDetails }}
      />
      {def && (
        <AddStepDialog
          open={adding}
          onOpenChange={setAdding}
          text={text}
          def={def}
          targets={targets}
          loading={targetsQuery.isPending}
          into={addingTo}
          selected={
            selectedStep
              ? flatten(obj(def.blueprint)).find((s) => s.key && edits.stepId(s.key) === selectedStep)
              : undefined
          }
          onAdd={(step, where) =>
            apply(() => edits.addStep(text, step, where), {
              select: edits.stepId(str(step.key)),
              focus: "cd-heading",
            })
          }
        />
      )}
    </div>
  );
}

interface PanelContext {
  text: string;
  def: Raw;
  targets: CompositeStepTarget[];
  targetsQuery: { isPending: boolean; isError: boolean };
  apply: (make: () => string, then?: Then) => boolean;
  focusNext: RefObject<string | null>;
  setSelected: (id: string | null) => void;
  addInto: (into: Into) => void;
}

/** A side of a branch: its node's ID, which it is (1 for the first case, 0 for otherwise), and what it is called. */
interface SideRef {
  id: string;
  taken: number;
  label: string;
}

/** Where a step added from a step's or Done's details goes: a side of a branch, a step's catch path (its group's, in one), or the error handler. */
type Into = SideRef | { catchOf: string } | { onError: true };

const whereOf = (into: Into): edits.Where =>
  "taken" in into ? { side: into.id, taken: into.taken } : "catchOf" in into ? { catchOf: into.catchOf } : "onError";

/** Puts the focus on another element once a select's list has closed, instead of back on the select. */
function useFocusAfterSelect() {
  const target = useRef<string | null>(null);
  return {
    later: (id: string) => {
      target.current = id;
    },
    onCloseAutoFocus: (e: Event) => {
      const id = target.current;
      if (!id) return;
      target.current = null;
      e.preventDefault();
      requestAnimationFrame(() => document.getElementById(id)?.focus());
    },
  };
}

function InputsPanel({ ctx }: { ctx: PanelContext }) {
  const { t } = useTranslation(["admin-catalog", "canvas"]);
  useFocusNext(ctx.focusNext);
  const inputs = formInputsOf(ctx.def);
  const request = t("canvas:composite.request");
  return (
    <section aria-label={t("canvas:selected", { title: request })} className={panel}>
      <PanelHeading>{request}</PanelHeading>
      <p className="text-muted">
        <Trans
          t={t}
          i18nKey="composite.inputs.body"
          components={{
            inputs: <Code text="inputs.<name>" />,
            environment: <Code text="environment" />,
            project: <Code text="project" />,
          }}
        />
      </p>
      {inputs.length > 0 ? (
        <ul aria-label={t("composite.inputs.list")} className="flex flex-col gap-0.5">
          {inputs.map((i) => (
            <li key={i.name}>
              <Code text={`inputs.${i.name}`} /> <span className="text-muted">{i.title}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-muted">{t("composite.inputs.none")}</p>
      )}
    </section>
  );
}

function EndPanel({ ctx }: { ctx: PanelContext }) {
  const { t } = useTranslation(["admin-catalog", "canvas"]);
  useFocusNext(ctx.focusNext);
  const on = obj(ctx.def.blueprint).rollback_on_failure === true;
  const done = t("canvas:composite.done");
  return (
    <section aria-label={t("canvas:selected", { title: done })} className={panel}>
      <PanelHeading>{done}</PanelHeading>
      <div className="flex items-start gap-2 text-body">
        <Checkbox
          id="cd-rollback"
          checked={on}
          onChange={(v) => ctx.apply(() => edits.setRollback(ctx.text, v))}
          className="mt-0.5"
        />
        <div>
          <label htmlFor="cd-rollback">{t("composite.end.rollback")}</label>
          <p className="text-secondary text-muted">{t("composite.end.rollbackHint")}</p>
        </div>
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.end.handler")}</legend>
        <p className="text-muted">
          {t("composite.end.handlerBody")} <Trans t={t} i18nKey="composite.theyReadError" components={errorCodes} />
        </p>
        <SideSteps
          steps={
            Array.isArray(obj(ctx.def.blueprint).on_error)
              ? (obj(ctx.def.blueprint).on_error as unknown[]).map(obj)
              : []
          }
          side={{ onError: true }}
          empty={t("composite.end.handlerEmpty")}
          ctx={ctx}
        />
      </fieldset>
    </section>
  );
}

const onlyVMStep = "-";

/**
 * The VM step whose deployment a custom resource joins (ADR-0056): named
 * when several come before it, else the only one.
 */
function ResourceDeployment({ step, g, id, ctx }: { step: Raw; g: number; id: string; ctx: PanelContext }) {
  const { t } = useTranslation("admin-catalog");
  const { def, targets, text, apply } = ctx;
  const named = str(obj(step.resource).deployment);
  const vms = flatten(obj(def.blueprint)).filter(
    (s) => s.g < g && s.key && findTarget(targets, targetOf(s.step))?.planned,
  );
  const hint = vms.length === 0 ? t("composite.resource.noVm") : t("composite.resource.deleting");
  if (vms.length <= 1 && !named)
    return (
      <p className="text-muted md:col-span-2">
        {vms.length === 1 ? `${t("composite.resource.joins", { name: vms[0]!.name })} ` : ""}
        {hint}
      </p>
    );
  const options: Option[] = [
    { value: onlyVMStep, label: t("composite.resource.only") },
    ...vms.map((s) => ({ value: s.key, label: s.name, meta: s.key })),
  ];
  if (named && !vms.some((s) => s.key === named))
    options.push({ value: named, label: named, meta: t("composite.resource.notBefore") });
  return (
    <Field
      label={t("composite.resource.deployment")}
      htmlFor="cd-resource-deployment"
      className="md:col-span-2"
      hint={hint}
    >
      <Select
        id="cd-resource-deployment"
        value={named || onlyVMStep}
        options={options}
        onChange={(v) => apply(() => edits.setResourceDeployment(text, id, v === onlyVMStep ? "" : v))}
      />
    </Field>
  );
}

/** A step named in a sentence, as a button that selects it. */
function PickStep({
  label,
  text,
  mono = false,
  onPick,
}: {
  label: string;
  text: string;
  mono?: boolean;
  onPick: () => void;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      className={mono ? "font-mono text-accent hover:underline" : "text-accent hover:underline"}
      onClick={onPick}
    >
      {text}
    </button>
  );
}

function StepPanel({ node, ctx }: { node: CanvasNode; ctx: PanelContext }) {
  const { t } = useTranslation(["admin-catalog", "canvas"]);
  const { text, def, targets, apply, focusNext, setSelected } = ctx;
  useFocusNext(ctx.focusNext);
  const [byNameOpen, setByNameOpen] = useState(false);
  const runsFocus = useFocusAfterSelect();
  const [extra, setExtra] = useState<string[]>([]);
  const [undoExtra, setUndoExtra] = useState<string[]>([]);
  const variables = useQuery({ ...adm.variables(), staleTime: 60_000 });
  const found = edits.readStep(text, node.id);
  if (!found) return <NodeDetails node={node} />;
  const { step, g, j, side, caught, pointer } = found;
  const id = node.id;
  const key = str(step.key);
  const target = targetOf(step);
  const listed = findTarget(targets, target);
  const planned = listed?.planned ?? false;
  const slots = Array.isArray(obj(def.blueprint).steps) ? (obj(def.blueprint).steps as unknown[]).length : 0;
  const place = caught
    ? { g, caught: { list: caught.list, i: caught.i } }
    : side
      ? { g, side: { taken: side.taken, i: side.i } }
      : { g };
  const sources = bindingSources(def, { ...place, planned }, targets, variables.data?.items);
  const open = planned ? [] : openEarlier(def, place, targets);
  const withMap = obj(step.with);
  const rest = target?.kind === "rest";
  const ask = target?.kind === "ask";
  const wait = target?.kind === "wait";
  const poll = target?.kind === "poll";
  const signal = target?.kind === "signal";
  const branch = target?.kind === "branch";
  const notify = target?.kind === "notify";
  const resource = target?.kind === "resource";
  const inputs = listed?.inputs ?? [];
  const others = [...new Set([...Object.keys(withMap), ...extra])].filter((k) => !inputs.some((i) => i.name === k));
  const undo = step.undo === undefined ? undefined : obj(step.undo);
  const title = node.title;
  const flat = flatten(obj(def.blueprint));
  const at = flat.findIndex((s) => s.pointer === pointer);
  const slotBefore = g > 0 ? obj((obj(def.blueprint).steps as unknown[])[g - 1]) : {};
  const slot = obj((obj(def.blueprint).steps as unknown[] | undefined)?.[g]);
  const catchSteps = j === null ? step.catch : slot.catch;

  const bind = (name: string, scope: "with" | "undo") => (v: edits.Binding) =>
    apply(() => edits.setBinding(text, id, name, v, scope));
  const move = (dir: -1 | 1) => {
    const own = dir < 0 ? "cd-earlier" : "cd-later";
    const other = dir < 0 ? "cd-later" : "cd-earlier";
    apply(() => edits.moveStep(text, id, dir), { focus: own });
    // The button may be disabled now (it runs first or last): the other one, then.
    requestAnimationFrame(() => {
      const el = document.getElementById(own) as HTMLButtonElement | null;
      if (el?.disabled) document.getElementById(other)?.focus();
    });
  };
  const remove = () => {
    const before = flat[at - 1];
    apply(() => edits.removeStep(text, id), {
      select:
        before?.key && flat.filter((s) => s.key === before.key).length === 1 ? edits.stepId(before.key) : "inputs",
      focus: "cd-heading",
    });
  };
  // A listed template as listed: by its ID, it may be called otherwise now.
  const runsValue = byNameOpen
    ? byName
    : listed
      ? targetValue(listedTarget(listed))
      : target
        ? targetValue(target)
        : "";

  return (
    <section aria-label={t("canvas:selected", { title })} className={panel}>
      <div className="flex flex-wrap items-center gap-2">
        <PanelHeading badge={node.badge}>{title}</PanelHeading>
        <div className="ml-auto flex flex-wrap gap-1">
          <Button
            id="cd-earlier"
            size="sm"
            variant="quiet"
            disabled={caught ? caught.i === 0 : side ? side.i === 0 : j === null && g === 0}
            onClick={() => move(-1)}
          >
            <ArrowUp aria-hidden />
            {j === null ? t("composite.step.earlier") : t("composite.step.beforeOthers")}
          </Button>
          <Button
            id="cd-later"
            size="sm"
            variant="quiet"
            disabled={
              caught
                ? caught.i === caught.length - 1
                : side
                  ? side.i === side.length - 1
                  : j === null && g === slots - 1
            }
            onClick={() => move(1)}
          >
            <ArrowDown aria-hidden />
            {j === null ? t("composite.step.later") : t("composite.step.afterOthers")}
          </Button>
          {j === null &&
            g > 0 &&
            !side &&
            !caught &&
            !branch &&
            slotBefore.branch === undefined &&
            step.catch === undefined &&
            (slotBefore.parallel !== undefined || slotBefore.catch === undefined) && (
              <Button
                id="cd-join"
                size="sm"
                variant="quiet"
                onClick={() => apply(() => edits.joinPrevious(text, id), { focus: "cd-later" })}
              >
                {t("composite.step.join")}
              </Button>
            )}
          <Button id="cd-remove" size="sm" variant="danger" onClick={remove}>
            <Trash2 aria-hidden />
            {t("composite.step.remove")}
          </Button>
        </div>
      </div>
      {node.problems.length > 0 && (
        <ul aria-label={t("composite.step.problems")} className="flex flex-col gap-0.5 text-bad">
          {node.problems.map((p, i) => (
            <li key={i}>{p}</li>
          ))}
        </ul>
      )}
      {side && (
        <p className="text-muted">
          <Trans
            t={t}
            i18nKey="composite.step.onSide"
            components={{
              branch: (
                <PickStep
                  label={t("composite.step.selectBranch", { name: side.branch })}
                  text={side.branch}
                  mono
                  onPick={() => {
                    focusNext.current = "cd-heading";
                    setSelected(edits.stepId(side.branch));
                  }}
                />
              ),
            }}
          />
        </p>
      )}
      {caught && (
        <p className="text-muted">
          {caught.of === "on_error" ? (
            <Trans
              t={t}
              i18nKey="composite.step.inHandler"
              components={{
                handler: (
                  <PickStep
                    label={t("composite.step.selectDone")}
                    text={t("composite.step.handler")}
                    onPick={() => {
                      focusNext.current = "cd-heading";
                      setSelected("end");
                    }}
                  />
                ),
              }}
            />
          ) : caught.of ? (
            <Trans
              t={t}
              i18nKey="composite.step.onCatchOf"
              components={{
                step: (
                  <PickStep
                    label={t("composite.step.selectCaught", { name: caught.of })}
                    text={caught.of}
                    mono
                    onPick={() => {
                      focusNext.current = "cd-heading";
                      setSelected(edits.stepId(caught.of));
                    }}
                  />
                ),
              }}
            />
          ) : (
            t("composite.step.onGroupCatch", { number: g + 1 })
          )}{" "}
          <Trans t={t} i18nKey="composite.itReadsError" components={errorCodes} />
        </p>
      )}
      <div className="grid gap-3 md:grid-cols-2">
        <Field label={t("composite.key")} htmlFor="cd-key" hint={key ? t("composite.keyHint", { key }) : undefined}>
          <CommitInput
            id="cd-key"
            value={key}
            className="font-mono text-secondary"
            onCommit={(v) => {
              const next = v.trim();
              if (!keyPattern.test(next)) {
                toast.error(t("composite.keyFormat"));
                return;
              }
              if (next !== key && flat.some((s) => s.key === next)) {
                toast.error(t("composite.keyTaken", { key: next }));
                return;
              }
              apply(() => edits.setStepField(text, id, "key", next), { select: edits.stepId(next), focus: "cd-key" });
            }}
          />
        </Field>
        <Field label={t("composite.title")} htmlFor="cd-title">
          <CommitInput
            id="cd-title"
            value={str(step.title)}
            placeholder={listed?.title}
            onCommit={(v) => apply(() => edits.setStepField(text, id, "title", v))}
          />
        </Field>
        <Field
          label={t("composite.step.runs")}
          htmlFor="cd-runs"
          className="md:col-span-2"
          hint={
            ctx.targetsQuery.isError
              ? t("composite.step.runsError")
              : planned
                ? t("composite.step.runsVm")
                : listed?.summary
          }
        >
          <Select
            id="cd-runs"
            value={runsValue}
            loading={ctx.targetsQuery.isPending}
            placeholder={t("composite.chooseRuns")}
            options={targetOptions(targets, target, !side && !caught && j === null, Boolean(caught))}
            onCloseAutoFocus={runsFocus.onCloseAutoFocus}
            onChange={(v) => {
              if (v === byName) {
                runsFocus.later("cd-awx-name");
                return setByNameOpen(true);
              }
              setByNameOpen(false);
              if (v === runsValue) return;
              const t = parseTarget(v, targets);
              if (t) apply(() => edits.setTarget(text, id, t));
            }}
          />
        </Field>
        {byNameOpen && (
          <Field label={t("composite.awxName")} htmlFor="cd-awx-name" className="md:col-span-2">
            <CommitInput
              id="cd-awx-name"
              value=""
              className="font-mono text-secondary"
              onCommit={(v) => {
                if (!v.trim()) return;
                setByNameOpen(false);
                apply(() => edits.setTarget(text, id, { kind: "awx", ref: v.trim() }), { focus: "cd-runs" });
              }}
            />
          </Field>
        )}
        {(!branch || str(step.when)) && (
          <Field
            label={t("composite.when")}
            htmlFor="cd-when"
            className="md:col-span-2"
            hint={
              planned
                ? t("composite.step.whenVm")
                : branch
                  ? t("composite.step.whenBranch")
                  : t("composite.step.whenHint")
            }
          >
            <CommitInput
              id="cd-when"
              value={str(step.when)}
              className="font-mono text-secondary"
              placeholder={planned ? undefined : examples.when}
              disabled={planned && !str(step.when)}
              onCommit={(v) => apply(() => edits.setStepField(text, id, "when", v))}
            />
          </Field>
        )}
        {resource && <ResourceDeployment step={step} g={g} id={id} ctx={ctx} />}
      </div>

      {rest && (
        <fieldset className="flex flex-col gap-2">
          <legend className={legend}>{t("composite.step.rest")}</legend>
          <RestCallFields
            prefix="cd-rest"
            call={obj(step.rest)}
            stepKey={key}
            reads={sources.map((s) => s.value)}
            onChange={(field, v) => apply(() => edits.setRestField(text, id, field, v))}
          />
        </fieldset>
      )}

      {ask && <AskFields ask={obj(step.ask)} id={id} stepKey={key} ctx={ctx} />}

      {wait && <WaitFields wait={obj(step.wait)} id={id} reads={sources.map((s) => s.value)} ctx={ctx} />}

      {poll && <PollFields poll={obj(step.poll)} id={id} stepKey={key} reads={sources.map((s) => s.value)} ctx={ctx} />}

      {signal && <SignalFields signal={obj(step.signal)} id={id} stepKey={key} ctx={ctx} />}

      {branch && (
        <BranchFields branch={obj(step.branch)} id={id} stepKey={key} reads={sources.map((s) => s.value)} ctx={ctx} />
      )}

      {notify && (
        <NotifyFields notify={obj(step.notify)} id={id} stepKey={key} reads={sources.map((s) => s.value)} ctx={ctx} />
      )}

      {(step.for_each !== undefined || (!planned && !caught && !loopRefused(step))) && (
        <LoopFields
          forEach={step.for_each === undefined ? undefined : obj(step.for_each)}
          id={id}
          stepKey={key}
          ctx={ctx}
        />
      )}

      {!rest && !ask && !wait && !poll && !signal && !branch && !notify && (
        <fieldset className="flex flex-col gap-2">
          <legend className={legend}>{t("composite.step.inputs")}</legend>
          {target?.kind === "item" && !listed && <p className="text-muted">{t("composite.step.itemInputs")}</p>}
          {inputs.length === 0 && others.length === 0 && listed && (
            <p className="text-muted">{t("composite.step.noInputs")}</p>
          )}
          {inputs.map((input) => (
            <BindingRow
              key={input.name}
              id={`cd-in-${input.name}`}
              input={input}
              value={withMap[input.name]}
              sources={sources}
              onChange={bind(input.name, "with")}
            />
          ))}
          {others.map((name) => (
            <BindingRow
              key={name}
              id={`cd-in-${name}`}
              input={{ name, title: name, type: "", required: false }}
              value={withMap[name]}
              sources={sources}
              onChange={bind(name, "with")}
              onRemove={() => {
                setExtra((x) => x.filter((n) => n !== name));
                if (name in withMap) bind(name, "with")(undefined);
              }}
              note={target?.kind === "item" && listed ? t("composite.step.notFormInput") : undefined}
            />
          ))}
          {open.length > 0 && (
            <p className="text-muted">
              <Trans t={t} i18nKey="composite.step.open" components={{ reads: <Codes list={openReads(open)} /> }} />
            </p>
          )}
          {(target?.kind !== "item" || !listed) && (
            <AddVariable
              id="cd-add-input"
              rowId={(n) => `cd-in-${n}`}
              label={target?.kind === "awx" ? t("composite.step.addVariable") : t("composite.step.addInput")}
              taken={[...inputs.map((i) => i.name), ...others]}
              onAdd={(n) => setExtra((x) => [...x, n])}
            />
          )}
        </fieldset>
      )}

      {resource && <p className="text-muted">{t("composite.resource.undo")}</p>}

      {!planned &&
        !ask &&
        !wait &&
        !poll &&
        !signal &&
        !branch &&
        !notify &&
        !resource &&
        ((!caught && step.for_each === undefined) || undo !== undefined) && (
          <fieldset className="flex flex-col gap-2">
            <legend className={legend}>{t("composite.step.undo")}</legend>
            <UndoEditor
              undo={undo}
              targets={targets}
              sources={sources}
              extra={undoExtra}
              onExtra={(n) => setUndoExtra((x) => [...x, n])}
              onRemoveExtra={(n) => setUndoExtra((x) => x.filter((m) => m !== n))}
              onChange={(u) => apply(() => edits.setUndo(text, id, u))}
              bind={(name) => bind(name, "undo")}
              onRest={(field, v) => apply(() => edits.setRestField(text, id, field, v, "undo"))}
              reads={[...sources.map((s) => s.value), ...undoReads(key, outputsOf(step, targets))]}
            />
          </fieldset>
        )}

      {!caught && !branch && !resource && !planned && (
        <fieldset className="flex flex-col gap-2">
          <legend className={legend}>{j === null ? t("composite.step.catch") : t("composite.step.groupCatch")}</legend>
          <p className="text-muted">
            {j === null ? t("composite.step.catchBody") : t("composite.step.groupCatchBody")}{" "}
            <Trans t={t} i18nKey="composite.theyReadError" components={errorCodes} />
          </p>
          <SideSteps
            steps={Array.isArray(catchSteps) ? catchSteps.map(obj) : []}
            side={{ catchOf: id }}
            empty={j === null ? t("composite.step.catchEmpty") : t("composite.step.groupCatchEmpty")}
            ctx={ctx}
          />
        </fieldset>
      )}
    </section>
  );
}

function UndoEditor({
  undo,
  targets,
  sources,
  extra,
  onExtra,
  onRemoveExtra,
  onChange,
  bind,
  onRest,
  reads,
}: {
  undo: Raw | undefined;
  targets: CompositeStepTarget[];
  sources: Source[];
  extra: string[];
  onExtra: (name: string) => void;
  onRemoveExtra: (name: string) => void;
  onChange: (u: edits.Undo) => void;
  bind: (name: string) => (v: edits.Binding) => void;
  onRest: (field: edits.RestField, value: string | number[]) => void;
  reads: string[];
}) {
  const { t } = useTranslation("admin-catalog");
  const [byNameOpen, setByNameOpen] = useState(false);
  const focus = useFocusAfterSelect();
  const current = undo
    ? undo.rest !== undefined
      ? ({ kind: "rest", ref: "" } as const)
      : str(undo.workflow)
        ? ({ kind: "workflow", ref: str(undo.workflow) } as const)
        : ({ kind: "awx", ref: str(undo.job_template) } as const)
    : undefined;
  const options: Option[] = [
    { value: "none", label: t("composite.undoBy.nothing") },
    // An undo is launched as a job template, by its name.
    ...targetOptions(
      targets.filter((t) => t.kind !== "item" && !t.awx_workflow),
      current,
    ),
  ];
  const withMap = obj(undo?.with);
  const names = [...new Set([...Object.keys(withMap), ...extra])];
  return (
    <>
      <Field label={t("composite.undoBy.label")} htmlFor="cd-undo" hint={t("composite.undoBy.hint")}>
        <Select
          id="cd-undo"
          value={byNameOpen ? byName : current ? targetValue(current) : "none"}
          options={options}
          onCloseAutoFocus={focus.onCloseAutoFocus}
          onChange={(v) => {
            if (v === byName) {
              focus.later("cd-undo-name");
              return setByNameOpen(true);
            }
            setByNameOpen(false);
            if (v === "none") return onChange(null);
            const i = v.indexOf(":");
            const kind = v.slice(0, i);
            if (kind === "rest") onChange({ kind });
            if (kind === "awx" || kind === "workflow") onChange({ kind, ref: v.slice(i + 1) });
          }}
        />
      </Field>
      {byNameOpen && (
        <Field label={t("composite.awxName")} htmlFor="cd-undo-name">
          <CommitInput
            id="cd-undo-name"
            value=""
            className="font-mono text-secondary"
            onCommit={(v) => {
              if (!v.trim()) return;
              setByNameOpen(false);
              onChange({ kind: "awx", ref: v.trim() });
            }}
          />
        </Field>
      )}
      {undo && current?.kind === "rest" && (
        <RestCallFields prefix="cd-undo-rest" call={obj(undo.rest)} undo reads={reads} onChange={onRest} />
      )}
      {undo && current?.kind !== "rest" && (
        <>
          {names.map((name) => (
            <BindingRow
              key={name}
              id={`cd-undo-in-${name}`}
              input={{ name, title: name, type: "", required: false }}
              value={withMap[name]}
              sources={sources}
              onChange={bind(name)}
              onRemove={() => {
                onRemoveExtra(name);
                if (name in withMap) bind(name)(undefined);
              }}
            />
          ))}
          <AddVariable
            id="cd-undo-add"
            rowId={(n) => `cd-undo-in-${n}`}
            label={t("composite.undoBy.addVariable")}
            taken={names}
            onAdd={onExtra}
          />
        </>
      )}
    </>
  );
}

/** What the statuses written read as: the numbers in the text; undefined when something else is there. */
export function writtenStatuses(text: string): number[] | undefined {
  const words = text.split(/[\s,]+/).filter(Boolean);
  if (!words.every((w) => /^\d+$/.test(w))) return undefined;
  return words.map(Number);
}

/** What an undo reads of its own step: what that step gives, or any name a custom workflow returns. */
function undoReads(key: string, own: { outputs: CompositeStepOutput[]; open: boolean }): string[] {
  if (!key) return [];
  return [...own.outputs.map((o) => `steps.${key}.${o.name}`), ...(own.open ? [`steps.${key}.<name>`] : [])];
}

/** How later steps read what these give, whatever it is: a custom workflow, a custom resource, a wait for a signal. */
const openReads = (keys: string[]) => keys.map((k) => `steps.${k}.<name>`);

/** What a field's CEL can read at that point. */
function Reads({ reads }: { reads: string[] }) {
  const { t } = useTranslation("admin-catalog");
  return (
    <p className="text-muted">
      <Trans t={t} i18nKey="composite.reads" components={{ reads: <Codes list={reads} /> }} />
    </p>
  );
}

/** A REST call's fields: a step's, a poll's, or an undo's, whose answer nothing reads. */
function RestCallFields({
  prefix,
  call,
  stepKey,
  undo,
  poll,
  reads,
  onChange,
}: {
  prefix: string;
  call: Raw;
  stepKey?: string;
  undo?: boolean;
  /** A poll's call, which it repeats. */
  poll?: boolean;
  /** What its URL and body can read at that point. */
  reads: string[];
  onChange: (field: edits.RestField, value: string | number[]) => void;
}) {
  const { t } = useTranslation("admin-catalog");
  const secrets = useQuery(adm.secrets());
  const method = str(call.method);
  const own = `steps.${stepKey || "<key>"}`;
  const secret = str(call.secret);
  const accept = Array.isArray(call.accept) ? call.accept.join(", ") : "";
  // A name stored globally and by projects is one choice: the request's project picks which opens.
  const stored = [...new Map((secrets.data?.items ?? []).map((s) => [s.name, s])).values()];
  const secretOptions: Option[] = [
    { value: "none", label: t("composite.rest.noSecret") },
    ...stored.map((s) => ({ value: s.name, label: s.name, meta: s.kind })),
    ...(secret && !stored.some((s) => s.name === secret)
      ? [{ value: secret, label: secret, meta: secrets.isPending ? undefined : t("composite.rest.notStored") }]
      : []),
  ];
  return (
    <>
      <div className="grid gap-3 md:grid-cols-[8rem_minmax(0,1fr)]">
        <Field label={t(undo ? "composite.rest.undoMethod" : "composite.rest.method")} htmlFor={`${prefix}-method`}>
          <Select
            id={`${prefix}-method`}
            value={method}
            placeholder={t("composite.choose")}
            // A method written in the YAML that it may not use shows, and the step says why.
            options={[...new Set([...(poll ? pollMethods : restMethods), ...(method ? [method] : [])])].map((m) => ({
              value: m,
              label: m,
            }))}
            onChange={(v) => onChange("method", v)}
          />
        </Field>
        <Field
          label={t(undo ? "composite.rest.undoUrl" : "composite.rest.url")}
          htmlFor={`${prefix}-url`}
          hint={t("composite.rest.urlHint")}
        >
          <CommitInput
            id={`${prefix}-url`}
            value={str(call.url)}
            className="font-mono text-secondary"
            placeholder={examples.url}
            onCommit={(v) => onChange("url", v)}
          />
        </Field>
        <Field
          label={t(undo ? "composite.rest.undoBody" : "composite.rest.body")}
          htmlFor={`${prefix}-body`}
          className="md:col-span-2"
          hint={method === "GET" ? t("composite.rest.bodyGet") : t("composite.rest.bodyHint")}
        >
          <CommitInput
            id={`${prefix}-body`}
            value={str(call.body)}
            className="font-mono text-secondary"
            placeholder={method === "GET" ? undefined : examples.body}
            disabled={method === "GET" && !str(call.body)}
            onCommit={(v) => onChange("body", v)}
          />
        </Field>
        <Field
          label={t(undo ? "composite.rest.undoSecret" : "composite.rest.secret")}
          htmlFor={`${prefix}-secret`}
          className="md:col-span-2"
          hint={secrets.isError ? t("composite.rest.secretsError") : t("composite.rest.secretHint")}
        >
          <Select
            id={`${prefix}-secret`}
            value={secret || "none"}
            loading={secrets.isPending}
            options={secretOptions}
            onChange={(v) => onChange("secret", v === "none" ? "" : v)}
          />
        </Field>
        <Field
          label={t(undo ? "composite.rest.undoAccept" : "composite.rest.accept")}
          htmlFor={`${prefix}-accept`}
          className={undo ? "md:col-span-2" : undefined}
          hint={poll ? t("composite.rest.acceptPoll") : t("composite.rest.acceptHint")}
        >
          <CommitInput
            id={`${prefix}-accept`}
            value={accept}
            className="font-mono text-secondary"
            placeholder={undo ? "200, 204, 404" : "200, 201"}
            onCommit={(v) => {
              const statuses = writtenStatuses(v);
              if (statuses) onChange("accept", statuses);
              else toast.error(t("composite.rest.statuses"));
            }}
          />
        </Field>
        {!undo && (
          <Field
            label={t("composite.rest.keep")}
            htmlFor={`${prefix}-keep`}
            hint={
              <Trans
                t={t}
                i18nKey="composite.rest.keepHint"
                components={{ json: <Code text={`${own}.json`} />, status: <Code text=".status" /> }}
              />
            }
          >
            <CommitInput
              id={`${prefix}-keep`}
              value={str(call.keep)}
              className="font-mono text-secondary"
              placeholder={examples.keep}
              onCommit={(v) => onChange("keep", v)}
            />
          </Field>
        )}
      </div>
      {poll && <p className="text-muted">{t("composite.rest.pollNote")}</p>}
      <Reads reads={reads} />
    </>
  );
}

/** People, written as user IDs on one line. */
const writtenUsers = (text: string) => text.split(/[\s,]+/).filter(Boolean);
const listText = (v: unknown) => (Array.isArray(v) ? v.map(String).join(", ") : "");

const questionTypes = (): Option[] => [
  { value: "string", label: tr("composite.question.string") },
  { value: "number", label: tr("composite.question.number") },
  { value: "boolean", label: tr("composite.question.boolean") },
  { value: "choice", label: tr("composite.question.choice") },
];
const onTimeoutOptions = (): Option[] => [
  { value: "fail", label: tr("composite.onTimeout.fail") },
  { value: "continue", label: tr("composite.onTimeout.continue") },
  { value: "escalate", label: tr("composite.onTimeout.escalate") },
];

/** A wait for a time: how long, or until when. Writing one removes the other. */
function WaitFields({ wait, id, reads, ctx }: { wait: Raw; id: string; reads: string[]; ctx: PanelContext }) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const set = (field: "for" | "until", v: string) => apply(() => edits.setWaitField(text, id, field, v));
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className={legend}>{t("composite.wait.legend")}</legend>
      <div className="grid gap-3 md:grid-cols-[8rem_minmax(0,1fr)]">
        <Field label={t("composite.wait.for")} htmlFor="cd-wait-for" hint={t("composite.wait.forHint")}>
          <CommitInput
            id="cd-wait-for"
            value={str(wait.for)}
            className="font-mono text-secondary"
            placeholder={examples.duration}
            onCommit={(v) => set("for", v)}
          />
        </Field>
        <Field label={t("composite.wait.until")} htmlFor="cd-wait-until" hint={t("composite.wait.untilHint")}>
          <CommitInput
            id="cd-wait-until"
            value={str(wait.until)}
            className="font-mono text-secondary"
            placeholder={examples.window}
            onCommit={(v) => set("until", v)}
          />
        </Field>
      </div>
      <Reads reads={reads} />
    </fieldset>
  );
}

const pollThenOptions = (): Option[] => [
  { value: "fail", label: tr("composite.onTimeout.fail") },
  { value: "continue", label: tr("composite.onTimeout.goesOn") },
];

/** A poll: the call it repeats, what it waits for, how often and how long. */
function PollFields({
  poll,
  id,
  stepKey,
  reads,
  ctx,
}: {
  poll: Raw;
  id: string;
  stepKey: string;
  reads: string[];
  ctx: PanelContext;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const set = (field: edits.PollField, v: string) => apply(() => edits.setPollField(text, id, field, v));
  const own = `steps.${stepKey || "<key>"}`;
  return (
    <>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.poll.repeats")}</legend>
        <RestCallFields
          prefix="cd-poll-rest"
          call={obj(poll.rest)}
          stepKey={stepKey}
          poll
          reads={reads}
          onChange={(field, v) => apply(() => edits.setRestField(text, id, field, v, "poll"))}
        />
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.poll.waitsFor")}</legend>
        <div className="grid gap-3 md:grid-cols-[8rem_minmax(0,1fr)]">
          <Field label={t("composite.poll.every")} htmlFor="cd-poll-every" hint={t("composite.poll.everyHint")}>
            <CommitInput
              id="cd-poll-every"
              value={str(poll.every)}
              className="font-mono text-secondary"
              placeholder={examples.every}
              onCommit={(v) => set("every", v)}
            />
          </Field>
          <Field
            label={t("composite.poll.until")}
            htmlFor="cd-poll-until"
            hint={
              <Trans
                t={t}
                i18nKey="composite.poll.untilHint"
                components={{ json: <Code text={`${own}.json`} />, status: <Code text={`${own}.status`} /> }}
              />
            }
          >
            <CommitInput
              id="cd-poll-until"
              value={str(poll.until)}
              className="font-mono text-secondary"
              placeholder={pollExample(own)}
              onCommit={(v) => set("until", v)}
            />
          </Field>
        </div>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.poll.never")}</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label={t("composite.poll.deadline")} htmlFor="cd-poll-timeout" hint={t("composite.poll.deadlineHint")}>
            <CommitInput
              id="cd-poll-timeout"
              value={str(poll.timeout)}
              className="font-mono text-secondary"
              placeholder={examples.duration}
              onCommit={(v) => set("timeout", v)}
            />
          </Field>
          <Field label={t("composite.then")} htmlFor="cd-poll-on-timeout">
            <Select
              id="cd-poll-on-timeout"
              value={str(poll.on_timeout) || "fail"}
              options={pollThenOptions()}
              onChange={(v) => set("on_timeout", v)}
            />
          </Field>
        </div>
      </fieldset>
    </>
  );
}

const signalByName = "signal:";
const eventOptions = (): Option[] => [
  { value: signalByName, label: tr("composite.signal.sent") },
  ...eventTopics.map((topic) => ({ value: `event:${topic}`, label: topic, group: tr("composite.signal.events") })),
];

/** A wait for a signal or an event (ADR-0057): which, the event's condition, and its deadline. */
function SignalFields({ signal, id, stepKey, ctx }: { signal: Raw; id: string; stepKey: string; ctx: PanelContext }) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const set = (field: edits.SignalField, v: string) => apply(() => edits.setSignalField(text, id, field, v));
  const event = str(signal.event);
  const name = str(signal.name);
  const own = `steps.${stepKey || "<key>"}`;
  const sentTo = `/requests/<number>/signal/${name || "<name>"}`;
  return (
    <>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.poll.waitsFor")}</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label={t("composite.signal.waitsFor")} htmlFor="cd-signal-for">
            <Select
              id="cd-signal-for"
              value={event ? `event:${event}` : signalByName}
              options={eventOptions()}
              onChange={(v) => {
                if (v === signalByName) set("name", name || edits.newSignal(stepKey).name);
                else set("event", v.slice("event:".length));
              }}
            />
          </Field>
          {event ? (
            <Field
              label={t("composite.signal.condition")}
              htmlFor="cd-signal-when"
              hint={
                <Trans
                  t={t}
                  i18nKey="composite.signal.conditionHint"
                  components={{ waiting: <Code text="waiting" /> }}
                />
              }
            >
              <CommitInput
                id="cd-signal-when"
                value={str(signal.when)}
                className="font-mono text-secondary"
                placeholder={examples.event}
                onCommit={(v) => set("when", v)}
              />
            </Field>
          ) : (
            <Field
              label={t("composite.signal.name")}
              htmlFor="cd-signal-name"
              hint={
                <Trans
                  t={t}
                  i18nKey="composite.signal.nameHint"
                  components={{ path: <Code text={sentTo} />, cli: <Code text="rvavctl requests signal" /> }}
                />
              }
            >
              <CommitInput
                id="cd-signal-name"
                value={name}
                className="font-mono text-secondary"
                placeholder={examples.signal}
                onCommit={(v) => set("name", v)}
              />
            </Field>
          )}
        </div>
        <p className="text-muted">
          <Trans t={t} i18nKey="composite.signal.outputs" components={{ field: <Code text={`${own}.<field>`} /> }} />
        </p>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.signal.nothing")}</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label={t("composite.timeout")} htmlFor="cd-signal-timeout" hint={t("composite.signal.timeoutHint")}>
            <CommitInput
              id="cd-signal-timeout"
              value={str(signal.timeout)}
              className="font-mono text-secondary"
              placeholder={examples.duration}
              onCommit={(v) => set("timeout", v)}
            />
          </Field>
          <Field
            label={t("composite.then")}
            htmlFor="cd-signal-on-timeout"
            hint={
              str(signal.on_timeout) === "continue" ? (
                <Trans
                  t={t}
                  i18nKey="composite.laterReads"
                  components={{ path: <Code text={`has(${own}.timed_out)`} /> }}
                />
              ) : undefined
            }
          >
            <Select
              id="cd-signal-on-timeout"
              value={str(signal.on_timeout) || "fail"}
              options={pollThenOptions()}
              onChange={(v) => set("on_timeout", v)}
            />
          </Field>
        </div>
      </fieldset>
    </>
  );
}

/** A step's loop (ADR-0057): the list it goes over and how many passes run at once, or the way to add one. */
function LoopFields({
  forEach,
  id,
  stepKey,
  ctx,
}: {
  forEach: Raw | undefined;
  id: string;
  stepKey: string;
  ctx: PanelContext;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const own = `steps.${stepKey || "<key>"}`;
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className={legend}>{t("composite.loop.legend")}</legend>
      {forEach ? (
        <>
          <div className="grid gap-3 md:grid-cols-[minmax(0,1fr)_8rem]">
            <Field
              label={t("composite.loop.items")}
              htmlFor="cd-loop-items"
              hint={
                <Trans
                  t={t}
                  i18nKey="composite.loop.itemsHint"
                  components={{ each: <Code text="each" />, index: <Code text="index" /> }}
                />
              }
            >
              <CommitInput
                id="cd-loop-items"
                value={str(forEach.items)}
                className="font-mono text-secondary"
                placeholder={examples.items}
                onCommit={(v) => apply(() => edits.setForEach(text, id, "items", v))}
              />
            </Field>
            <Field
              label={t("composite.loop.atATime")}
              htmlFor="cd-loop-parallel"
              hint={t("composite.loop.atATimeHint")}
            >
              <CommitInput
                id="cd-loop-parallel"
                value={typeof forEach.parallel === "number" ? String(forEach.parallel) : str(forEach.parallel) || "1"}
                className="font-mono text-secondary"
                onCommit={(v) => apply(() => edits.setForEach(text, id, "parallel", v))}
              />
            </Field>
          </div>
          <p className="text-muted">
            <Trans
              t={t}
              i18nKey="composite.loop.reads"
              components={{ items: <Code text={`${own}.items`} />, count: <Code text={`${own}.count`} /> }}
            />
          </p>
          <div>
            <Button
              id="cd-loop-remove"
              size="sm"
              variant="quiet"
              onClick={() => apply(() => edits.removeForEach(text, id), { focus: "cd-loop-add" })}
            >
              <Trash2 aria-hidden />
              {t("composite.loop.once")}
            </Button>
          </div>
        </>
      ) : (
        <div className="flex flex-col gap-1.5">
          <p className="text-muted">{t("composite.loop.runsOnce")}</p>
          <div>
            <Button
              id="cd-loop-add"
              size="sm"
              variant="secondary"
              onClick={() => apply(() => edits.setForEach(text, id, "items", ""), { focus: "cd-loop-items" })}
            >
              <Plus aria-hidden />
              {t("composite.loop.repeat")}
            </Button>
          </div>
        </div>
      )}
    </fieldset>
  );
}

/** The steps of a side of a branch, a catch path or the error handler: each a button that selects it, and the way to add another. */
function SideSteps({ steps, side, empty, ctx }: { steps: Raw[]; side: Into; empty?: string; ctx: PanelContext }) {
  const { t } = useTranslation(["admin-catalog", "canvas"]);
  const which = "taken" in side ? "side" : "catchOf" in side ? "catch" : "handler";
  const label = "taken" in side ? side.label : "";
  return (
    <div className="flex flex-col gap-1.5">
      {steps.length > 0 ? (
        <ol aria-label={t(`composite.side.stepsOf.${which}`, { label })} className="flex flex-col gap-0.5">
          {steps.map((s, i) => {
            const key = str(s.key);
            return (
              <li key={key || i}>
                <button
                  type="button"
                  disabled={!key}
                  className="text-left font-medium text-accent hover:underline"
                  onClick={() => {
                    ctx.focusNext.current = "cd-heading";
                    ctx.setSelected(edits.stepId(key));
                  }}
                >
                  {str(s.title) || key || t("canvas:composite.noKey")}
                </button>
              </li>
            );
          })}
        </ol>
      ) : (
        <p className="text-muted">{empty ?? t("composite.side.empty")}</p>
      )}
      <div>
        <Button size="sm" variant="secondary" onClick={() => ctx.addInto(side)}>
          <Plus aria-hidden />
          {t(`composite.side.addTo.${which}`, { label })}
        </Button>
      </div>
    </div>
  );
}

const casePrefix = (i: number) => `cd-case${i}`;

/** A branch: its cases, the first whose condition holds being taken, each with its steps, and what runs otherwise. */
function BranchFields({
  branch,
  id,
  stepKey,
  reads,
  ctx,
}: {
  branch: Raw;
  id: string;
  stepKey: string;
  reads: string[];
  ctx: PanelContext;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const own = `steps.${stepKey || "<key>"}`;
  const cases = Array.isArray(branch.cases) ? (branch.cases as unknown[]).map(obj) : [];
  const otherwise = Array.isArray(branch.else) ? (branch.else as unknown[]).map(obj) : [];
  const moveCase = (i: number, dir: -1 | 1) => {
    const to = i + dir;
    const own = `cd-case${to}-${dir < 0 ? "earlier" : "later"}`;
    const other = `cd-case${to}-${dir < 0 ? "later" : "earlier"}`;
    apply(() => edits.moveBranchCase(text, id, i, dir), { focus: own });
    // The button may be disabled now (it comes first or last): the other one, then.
    requestAnimationFrame(() => {
      const el = document.getElementById(own) as HTMLButtonElement | null;
      if (el?.disabled) document.getElementById(other)?.focus();
    });
  };
  return (
    <>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.branch.cases")}</legend>
        <p className="text-muted">{t("composite.branch.casesBody")}</p>
        {cases.map((c, i) => {
          const prefix = casePrefix(i);
          const number = t("composite.branch.case", { number: i + 1 });
          const label = str(c.title) || number;
          return (
            <fieldset key={i} className="flex flex-col gap-2 rounded-md border border-line p-2">
              <legend className="px-1 text-secondary font-medium">{number}</legend>
              <div className="grid gap-3 md:grid-cols-2">
                <Field label={t("composite.title")} htmlFor={`${prefix}-title`} hint={t("composite.branch.titleHint")}>
                  <CommitInput
                    id={`${prefix}-title`}
                    value={str(c.title)}
                    placeholder={number}
                    onCommit={(v) => apply(() => edits.setBranchCase(text, id, i, "title", v))}
                  />
                </Field>
                <Field label={t("composite.when")} htmlFor={`${prefix}-when`} hint={t("composite.branch.whenHint")}>
                  <CommitInput
                    id={`${prefix}-when`}
                    value={str(c.when)}
                    className="font-mono text-secondary"
                    placeholder={examples.caseWhen}
                    onCommit={(v) => apply(() => edits.setBranchCase(text, id, i, "when", v))}
                  />
                </Field>
              </div>
              <SideSteps
                steps={Array.isArray(c.steps) ? (c.steps as unknown[]).map(obj) : []}
                side={{ id, taken: i + 1, label }}
                ctx={ctx}
              />
              <div className="flex flex-wrap gap-1">
                <Button
                  id={`${prefix}-earlier`}
                  size="sm"
                  variant="quiet"
                  disabled={i === 0}
                  aria-label={t("composite.branch.moveEarlier", { number: i + 1 })}
                  onClick={() => moveCase(i, -1)}
                >
                  <ArrowUp aria-hidden />
                  {t("composite.branch.earlier")}
                </Button>
                <Button
                  id={`${prefix}-later`}
                  size="sm"
                  variant="quiet"
                  disabled={i === cases.length - 1}
                  aria-label={t("composite.branch.moveLater", { number: i + 1 })}
                  onClick={() => moveCase(i, 1)}
                >
                  <ArrowDown aria-hidden />
                  {t("composite.branch.later")}
                </Button>
                <Button
                  size="sm"
                  variant="quiet"
                  aria-label={t("composite.branch.remove", { number: i + 1 })}
                  onClick={() => apply(() => edits.removeBranchCase(text, id, i), { focus: "cd-case-add" })}
                >
                  <Trash2 aria-hidden />
                  {t("composite.remove")}
                </Button>
              </div>
            </fieldset>
          );
        })}
        <div>
          <Button
            id="cd-case-add"
            size="sm"
            variant="secondary"
            onClick={() => apply(() => edits.addBranchCase(text, id), { focus: `cd-case${cases.length}-title` })}
          >
            <Plus aria-hidden />
            {t("composite.branch.addCase")}
          </Button>
        </div>
        <Reads reads={reads} />
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.branch.otherwise")}</legend>
        <p className="text-muted">{t("composite.branch.otherwiseBody")}</p>
        <SideSteps steps={otherwise} side={{ id, taken: 0, label: t("composite.branch.otherwise") }} ctx={ctx} />
      </fieldset>
      <p className="text-muted">
        <Trans
          t={t}
          i18nKey="composite.branch.reads"
          components={{
            case: <Code text={`${own}.case`} />,
            when: <Code text="when" />,
            has: <Code text="has(steps.<key>)" />,
          }}
        />
      </p>
    </>
  );
}

/** A text that commits when it loses the focus; Enter is a new line. */
function CommitText({
  id,
  value,
  onCommit,
  ...described
}: {
  id: string;
  value: string;
  onCommit: (v: string) => void;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
}) {
  const [draft, setDraft] = useState(value);
  const [shownValue, setShownValue] = useState(value);
  if (shownValue !== value) {
    setShownValue(value);
    setDraft(value);
  }
  return (
    <Textarea
      id={id}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => draft !== value && onCommit(draft)}
      {...described}
    />
  );
}

/** A message: who it tells and what it says. Nothing to undo, and no inputs: its text reads them. */
function NotifyFields({
  notify,
  id,
  stepKey,
  reads,
  ctx,
}: {
  notify: Raw;
  id: string;
  stepKey: string;
  reads: string[];
  ctx: PanelContext;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const set = (field: edits.NotifyField, v: string | string[] | boolean) =>
    apply(() => edits.setNotifyField(text, id, field, v));
  const own = `steps.${stepKey || "<key>"}`;
  return (
    <>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.notify.who")}</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <div className="flex items-center gap-2">
            <Checkbox
              id="cd-notify-requester"
              checked={notify.requester === true}
              onChange={(v) => set("requester", v)}
            />
            <label htmlFor="cd-notify-requester">{t("composite.requester")}</label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="cd-notify-owner" checked={notify.owner === true} onChange={(v) => set("owner", v)} />
            <label htmlFor="cd-notify-owner">{t("composite.owner")}</label>
          </div>
          <Field
            label={t("composite.notify.groups")}
            htmlFor="cd-notify-groups"
            hint={t("composite.notify.groupsHint")}
          >
            <CommitInput
              id="cd-notify-groups"
              value={listText(notify.groups)}
              onCommit={(v) =>
                set(
                  "groups",
                  v
                    .split(",")
                    .map((g) => g.trim())
                    .filter(Boolean),
                )
              }
            />
          </Field>
          <Field label={t("composite.notify.people")} htmlFor="cd-notify-users" hint={t("composite.usersHint")}>
            <CommitInput
              id="cd-notify-users"
              value={listText(notify.users)}
              className="font-mono text-secondary"
              onCommit={(v) => set("users", writtenUsers(v))}
            />
          </Field>
        </div>
        <p className="text-muted">{t("composite.notify.channels")}</p>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.notify.says")}</legend>
        <Field
          label={t("composite.message")}
          htmlFor="cd-notify-message"
          hint={t("composite.notify.messageHint", { max: maxStepMessage })}
        >
          <CommitText id="cd-notify-message" value={str(notify.message)} onCommit={(v) => set("message", v)} />
        </Field>
        <Reads reads={reads} />
        <p className="text-muted">
          <Trans t={t} i18nKey="composite.notify.sentOnce" components={{ told: <Code text={`${own}.told`} /> }} />
        </p>
      </fieldset>
    </>
  );
}

/** A question's default as written: of the question's type, or as text for the step to say what is wrong. */
export function writtenDefault(type: string, text: string): string | number | boolean | undefined {
  const t = text.trim();
  if (!t) return undefined;
  if (type === "number" && /^-?\d+(\.\d+)?$/.test(t)) return Number(t);
  if (type === "boolean" && (t === "true" || t === "false")) return t === "true";
  return t;
}

/** A wait for a person: who it asks, what it says and asks, how long it waits and what happens then. */
function AskFields({ ask, id, stepKey, ctx }: { ask: Raw; id: string; stepKey: string; ctx: PanelContext }) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const set = (field: edits.AskField, v: string | string[] | boolean) =>
    apply(() => edits.setAskField(text, id, field, v));
  const form = Array.isArray(ask.form) ? (ask.form as unknown[]).map(obj) : [];
  const timeout = str(ask.timeout);
  const onTimeout = str(ask.on_timeout);
  const escalate = obj(ask.escalate_to);
  const reads = `steps.${stepKey || "<key>"}`;
  return (
    <>
      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.ask.who")}</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label={t("composite.ask.group")} htmlFor="cd-ask-group" hint={t("composite.ask.groupHint")}>
            <CommitInput id="cd-ask-group" value={str(ask.group)} onCommit={(v) => set("group", v)} />
          </Field>
          <Field
            label={t("composite.ask.approvers")}
            htmlFor="cd-ask-approvers"
            hint={t("composite.ask.approversHint")}
          >
            <CommitInput
              id="cd-ask-approvers"
              value={listText(ask.approvers)}
              className="font-mono text-secondary"
              onCommit={(v) => set("approvers", writtenUsers(v))}
            />
          </Field>
          <div className="flex items-center gap-2">
            <Checkbox id="cd-ask-requester" checked={ask.requester === true} onChange={(v) => set("requester", v)} />
            <label htmlFor="cd-ask-requester">{t("composite.requester")}</label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="cd-ask-owner" checked={ask.owner === true} onChange={(v) => set("owner", v)} />
            <label htmlFor="cd-ask-owner">{t("composite.owner")}</label>
          </div>
        </div>
      </fieldset>

      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.ask.answer")}</legend>
        <Field label={t("composite.message")} htmlFor="cd-ask-message" hint={t("composite.ask.messageHint")}>
          <CommitInput id="cd-ask-message" value={str(ask.message)} onCommit={(v) => set("message", v)} />
        </Field>
        {form.length === 0 && <p className="text-muted">{t("composite.ask.noQuestions")}</p>}
        {form.map((f, i) => (
          <AskQuestion key={i} index={i} question={f} id={id} stepKey={stepKey} ctx={ctx} />
        ))}
        <div>
          <Button
            id="cd-ask-add"
            size="sm"
            variant="secondary"
            onClick={() => apply(() => edits.addAskQuestion(text, id), { focus: `cd-ask-q${form.length}-key` })}
          >
            <Plus aria-hidden />
            {t("composite.ask.addQuestion")}
          </Button>
        </div>
        <p className="text-muted">
          <Trans
            t={t}
            i18nKey={form.length > 0 ? "composite.ask.readsForm" : "composite.ask.reads"}
            components={{
              answered: <Code text={`${reads}.answered`} />,
              by: <Code text={`${reads}.by`} />,
              comment: <Code text={`${reads}.comment`} />,
              answer: <Code text={`${reads}.answer.<key>`} />,
            }}
          />
        </p>
      </fieldset>

      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>{t("composite.ask.nobody")}</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label={t("composite.timeout")} htmlFor="cd-ask-timeout" hint={t("composite.ask.timeoutHint")}>
            <CommitInput
              id="cd-ask-timeout"
              value={timeout}
              className="font-mono text-secondary"
              placeholder={examples.askTimeout}
              onCommit={(v) => set("timeout", v)}
            />
          </Field>
          <Field label={t("composite.then")} htmlFor="cd-ask-on-timeout">
            <Select
              id="cd-ask-on-timeout"
              value={onTimeout || "fail"}
              options={onTimeoutOptions()}
              onChange={(v) => set("on_timeout", v)}
            />
          </Field>
          {onTimeout === "escalate" && (
            <>
              <Field label={t("composite.ask.escGroup")} htmlFor="cd-ask-esc-group">
                <CommitInput
                  id="cd-ask-esc-group"
                  value={str(escalate.group)}
                  onCommit={(v) => apply(() => edits.setAskEscalate(text, id, "group", v))}
                />
              </Field>
              <Field
                label={t("composite.ask.escApprovers")}
                htmlFor="cd-ask-esc-approvers"
                hint={t("composite.ask.escHint")}
              >
                <CommitInput
                  id="cd-ask-esc-approvers"
                  value={listText(escalate.approvers)}
                  className="font-mono text-secondary"
                  onCommit={(v) => apply(() => edits.setAskEscalate(text, id, "approvers", writtenUsers(v)))}
                />
              </Field>
            </>
          )}
        </div>
      </fieldset>
    </>
  );
}

/** One question of a wait's form. No secrets: an answer goes through Temporal's history. */
function AskQuestion({
  index,
  question,
  id,
  stepKey,
  ctx,
}: {
  index: number;
  question: Raw;
  id: string;
  stepKey: string;
  ctx: PanelContext;
}) {
  const { t } = useTranslation("admin-catalog");
  const { text, apply } = ctx;
  const prefix = `cd-ask-q${index}`;
  const type = str(question.type);
  const set = (field: edits.QuestionField, v: string | number | boolean | string[] | undefined) =>
    apply(() => edits.setAskQuestion(text, id, index, field, v));
  const key = str(question.key);
  const answer = `steps.${stepKey || "<key>"}.answer.${key}`;
  return (
    <fieldset className="flex flex-col gap-2 rounded-md border border-line p-2">
      <legend className="px-1 text-secondary font-medium">
        {t("composite.question.legend", { number: index + 1 })}
      </legend>
      <div className="grid gap-3 md:grid-cols-2">
        <Field
          label={t("composite.key")}
          htmlFor={`${prefix}-key`}
          hint={key ? t("composite.laterRead", { path: answer }) : undefined}
        >
          <CommitInput
            id={`${prefix}-key`}
            value={key}
            className="font-mono text-secondary"
            onCommit={(v) => set("key", v)}
          />
        </Field>
        <Field label={t("composite.question.label")} htmlFor={`${prefix}-label`}>
          <CommitInput id={`${prefix}-label`} value={str(question.label)} onCommit={(v) => set("label", v)} />
        </Field>
        <Field label={t("composite.question.type")} htmlFor={`${prefix}-type`}>
          <Select
            id={`${prefix}-type`}
            value={type}
            placeholder={t("composite.choose")}
            options={questionTypes()}
            onChange={(v) => v !== type && set("type", v)}
          />
        </Field>
        {type === "choice" && (
          <Field
            label={t("composite.question.options")}
            htmlFor={`${prefix}-options`}
            hint={t("composite.question.optionsHint")}
          >
            <CommitInput
              id={`${prefix}-options`}
              value={listText(question.options)}
              onCommit={(v) =>
                set(
                  "options",
                  v
                    .split(",")
                    .map((o) => o.trim())
                    .filter(Boolean),
                )
              }
            />
          </Field>
        )}
        <Field
          label={t("composite.question.default")}
          htmlFor={`${prefix}-default`}
          hint={t("composite.question.defaultHint")}
        >
          <CommitInput
            id={`${prefix}-default`}
            value={shown(question.default)}
            onCommit={(v) => set("default", writtenDefault(type, v))}
          />
        </Field>
        <div className="flex items-center gap-2 self-center">
          <Checkbox
            id={`${prefix}-required`}
            checked={question.required === true}
            onChange={(v) => set("required", v)}
          />
          <label htmlFor={`${prefix}-required`}>{t("composite.question.required")}</label>
        </div>
      </div>
      <div>
        <Button
          size="sm"
          variant="quiet"
          aria-label={t("composite.question.remove", { number: index + 1 })}
          onClick={() => apply(() => edits.removeAskQuestion(text, id, index), { focus: "cd-ask-add" })}
        >
          <Trash2 aria-hidden />
          {t("composite.remove")}
        </Button>
      </div>
    </fieldset>
  );
}

/** A name to add to a step's inputs or its undo's; the row added for it (rowId) is focused. */
function AddVariable({
  id,
  rowId,
  label,
  taken,
  onAdd,
}: {
  id: string;
  rowId: (name: string) => string;
  label: string;
  taken: string[];
  onAdd: (name: string) => void;
}) {
  const { t } = useTranslation("admin-catalog");
  const [name, setName] = useState("");
  const [error, setError] = useState<string>();
  const add = () => {
    const n = name.trim();
    if (!variablePattern.test(n)) return setError(t("composite.variable.format"));
    if (taken.includes(n)) return setError(t("composite.variable.there", { name: n }));
    setError(undefined);
    setName("");
    onAdd(n);
    requestAnimationFrame(() => document.getElementById(rowId(n))?.focus());
  };
  return (
    <div className="flex items-end gap-2">
      <Field label={label} htmlFor={id} error={error} className="max-w-64 flex-1">
        <Input
          id={id}
          value={name}
          className="font-mono text-secondary"
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
        />
      </Field>
      <Button size="sm" variant="secondary" onClick={add} aria-label={label}>
        {t("composite.add")}
      </Button>
    </div>
  );
}

const unbound = "none";
const written = "written";

/** One input: what it is bound to, picked or written out. */
function BindingRow({
  id,
  input,
  value,
  sources,
  onChange,
  onRemove,
  note,
}: {
  id: string;
  input: CompositeStepInput;
  value: unknown;
  sources: Source[];
  onChange: (v: edits.Binding) => void;
  onRemove?: () => void;
  note?: string;
}) {
  const { t } = useTranslation("admin-catalog");
  const [writing, setWriting] = useState(false);
  const focus = useFocusAfterSelect();
  const matched = typeof value === "string" && !writing ? sources.find((s) => s.value === value) : undefined;
  const mode = matched ? matched.value : value === undefined && !writing ? unbound : written;
  const options: Option[] = [
    { value: unbound, label: t("composite.binding.notBound") },
    { value: written, label: t("composite.binding.written") },
    ...sources.map((s) => ({ value: s.value, label: s.label, meta: s.value, group: s.group })),
  ];
  const missing = input.required && value === undefined;
  return (
    <div className="grid items-start gap-x-3 gap-y-1 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
      <div className="flex min-w-0 flex-col pt-1.5">
        <label htmlFor={id} className="text-secondary font-medium text-fg">
          {input.title}
          {input.title !== input.name && <span className="ml-1.5 font-mono text-caption text-faint">{input.name}</span>}
        </label>
        {(missing || note || input.description) && (
          <span id={`${id}-note`} className={cn("text-caption", missing ? "text-warn" : "text-muted")}>
            {missing ? t("composite.binding.required") : (note ?? input.description)}
          </span>
        )}
      </div>
      <div className="flex min-w-0 items-start gap-1">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <Select
            id={id}
            value={mode}
            options={options}
            aria-describedby={missing || note || input.description ? `${id}-note` : undefined}
            onCloseAutoFocus={focus.onCloseAutoFocus}
            onChange={(v) => {
              if (v === written) {
                setWriting(true);
                focus.later(`${id}-cel`);
                return;
              }
              setWriting(false);
              onChange(v === unbound ? undefined : v);
            }}
          />
          {mode === written && (
            <CommitInput
              id={`${id}-cel`}
              value={shown(value)}
              label={t("composite.binding.cel", { title: input.title })}
              placeholder={t("composite.binding.example")}
              className="font-mono text-secondary"
              onCommit={(t) => {
                const v = writtenBinding(t);
                if (v === undefined) setWriting(false);
                onChange(v);
              }}
            />
          )}
        </div>
        {onRemove && (
          <Button
            size="icon"
            variant="quiet"
            aria-label={t("composite.removeName", { name: input.name })}
            title={t("composite.removeName", { name: input.name })}
            onClick={onRemove}
          >
            <Trash2 aria-hidden />
          </Button>
        )}
      </div>
    </div>
  );
}

function AddStepDialog({
  open,
  onOpenChange,
  text,
  def,
  targets,
  loading,
  into,
  selected,
  onAdd,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  text: string;
  def: Raw;
  targets: CompositeStepTarget[];
  loading: boolean;
  /** Where it goes, when it was added from a step's or Done's details. */
  into?: Into | null;
  selected?: {
    key: string;
    name: string;
    j: number | null;
    step: Raw;
    side?: { taken: number; i: number };
    caught?: { list: string; i: number };
  };
  onAdd: (step: Raw, where: edits.Where) => void;
}) {
  const { t } = useTranslation(["admin-catalog", "canvas", "common"]);
  const [choice, setChoice] = useState("");
  const [awxName, setAwxName] = useState("");
  const [key, setKey] = useState("");
  const [keyTouched, setKeyTouched] = useState(false);
  const [title, setTitle] = useState("");
  const [where, setWhere] = useState<"after" | "with" | "end">("end");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const added = useRef(false);
  const focus = useFocusAfterSelect();
  // What it runs can't take the focus while it loads: it takes it then, unless the dialog has it.
  const wasLoading = useRef(loading);
  useEffect(() => {
    if (open && wasLoading.current && !loading) {
      const runs = document.getElementById("cd-new-runs");
      if (runs && !runs.closest('[role="dialog"]')?.contains(document.activeElement)) runs.focus();
    }
    wasLoading.current = loading;
  }, [open, loading]);
  const target =
    choice === byName
      ? awxName.trim()
        ? ({ kind: "awx", ref: awxName.trim() } as const)
        : undefined
      : parseTarget(choice, targets);
  const listed = findTarget(targets, target);
  const onCatch = Boolean(into && !("taken" in into));
  // A branch runs in no parallel group and on no side, and a side or a catch path lists its steps in order.
  const places = selected
    ? ([
        ...(target?.kind === "branch" && (selected.side || selected.caught)
          ? []
          : [["after", t("canvas:composite.after", { names: selected.name })] as const]),
        ...(target?.kind === "branch" ||
        selected.side ||
        selected.caught ||
        selected.step.branch !== undefined ||
        selected.step.catch !== undefined
          ? []
          : [["with", t("canvas:composite.sameTimeAs", { names: selected.name })] as const]),
        ["end", t("composite.dialog.atEnd")] as const,
      ] as const)
    : [];
  const place = places.some(([v]) => v === where) ? where : (places[0]?.[0] ?? "end");

  const reset = () => {
    setChoice("");
    setAwxName("");
    setKey("");
    setKeyTouched(false);
    setTitle("");
    setWhere(selected ? "after" : "end");
    setErrors({});
  };
  const suggest = (t: edits.Target | undefined) => {
    if (!keyTouched && t)
      setKey(
        edits.uniqueKey(
          text,
          findTarget(targets, t)?.title ??
            (t.kind === "rest"
              ? "rest_call"
              : t.kind === "ask"
                ? "person"
                : t.kind === "notify"
                  ? "message"
                  : t.kind === "wait" || t.kind === "poll" || t.kind === "signal" || t.kind === "branch"
                    ? t.kind
                    : t.ref),
        ),
      );
  };
  const submit = () => {
    const e: Record<string, string> = {};
    if (!target) e.runs = choice === byName ? t("composite.dialog.nameAwx") : t("composite.dialog.chooseRuns");
    if (!keyPattern.test(key)) e.key = t("composite.keyFormat");
    else if (edits.stepKeys(text).includes(key)) e.key = t("composite.keyTaken", { key });
    setErrors(e);
    if (Object.keys(e).length > 0 || !target) return;
    const step = newStep(key, title.trim() || listed?.title || "", target, listed?.inputs ?? [], def);
    const at: edits.Where = into
      ? whereOf(into)
      : selected && place === "after"
        ? { after: edits.stepId(selected.key) }
        : selected && place === "with"
          ? { with: edits.stepId(selected.key) }
          : "end";
    added.current = true;
    onAdd(step, at);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        title={t("composite.addStep")}
        description={t("composite.dialog.description")}
        onOpenAutoFocus={(e) => {
          reset();
          // What it runs first, rather than the close button.
          e.preventDefault();
          document.getElementById("cd-new-runs")?.focus();
        }}
        onCloseAutoFocus={(e) => {
          // The new step's details take the focus.
          if (added.current) {
            added.current = false;
            e.preventDefault();
          }
        }}
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <Field label={t("composite.dialog.whatRuns")} htmlFor="cd-new-runs" error={errors.runs}>
            <Select
              id="cd-new-runs"
              value={choice}
              loading={loading}
              placeholder={t("composite.chooseRuns")}
              options={targetOptions(targets, undefined, !into, onCatch)}
              onCloseAutoFocus={focus.onCloseAutoFocus}
              onChange={(v) => {
                if (v === byName) focus.later("cd-new-awx");
                setChoice(v);
                suggest(v === byName ? undefined : parseTarget(v, targets));
              }}
            />
          </Field>
          {choice === byName && (
            <Field label={t("composite.awxName")} htmlFor="cd-new-awx">
              <Input
                id="cd-new-awx"
                value={awxName}
                className="font-mono text-secondary"
                onChange={(e) => {
                  setAwxName(e.target.value);
                  suggest(e.target.value.trim() ? { kind: "awx", ref: e.target.value.trim() } : undefined);
                }}
              />
            </Field>
          )}
          <div className="grid gap-3">
            <Field
              label={t("composite.key")}
              htmlFor="cd-new-key"
              error={errors.key}
              hint={t("composite.dialog.keyHint")}
            >
              <Input
                id="cd-new-key"
                value={key}
                className="font-mono text-secondary"
                onChange={(e) => {
                  setKey(e.target.value);
                  setKeyTouched(true);
                }}
              />
            </Field>
            <Field label={t("composite.title")} htmlFor="cd-new-title">
              <Input
                id="cd-new-title"
                value={title}
                placeholder={listed?.title}
                onChange={(e) => setTitle(e.target.value)}
              />
            </Field>
          </div>
          {into ? (
            <p className="text-secondary text-muted">
              {"taken" in into
                ? t("composite.dialog.onSide", { label: into.label })
                : "catchOf" in into
                  ? t("composite.dialog.onCatch")
                  : t("composite.dialog.onHandler")}
            </p>
          ) : selected ? (
            <fieldset className="flex flex-col gap-1.5 text-body">
              <legend className="mb-1 text-secondary font-medium">{t("composite.dialog.whenRuns")}</legend>
              {places.map(([v, label]) => (
                <label
                  key={v}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-md border border-line px-2.5 py-2 hover:bg-sunken",
                    place === v && "border-accent bg-accent-soft hover:bg-accent-soft",
                  )}
                >
                  <input
                    type="radio"
                    name="cd-new-where"
                    value={v}
                    checked={place === v}
                    onChange={() => setWhere(v)}
                    className="accent-[var(--accent)]"
                  />
                  {label}
                </label>
              ))}
            </fieldset>
          ) : (
            <p className="text-secondary text-muted">{t("composite.dialog.atEndNote")}</p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="quiet" onClick={() => onOpenChange(false)}>
              {t("common:actions.cancel")}
            </Button>
            <Button type="submit" variant="primary">
              {t("composite.dialog.submit")}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
