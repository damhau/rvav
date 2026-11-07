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
// inputs nor an undo: who it asks, what, and for how long (ADR-0033).
import { useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Plus, Redo2, Trash2, Undo2 } from "lucide-react";
import { type ReactNode, type RefObject, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "sonner";
import { type CanvasNode, NodeDetails, within } from "@/components/canvas";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { type Option, Select } from "@/components/ui/select";
import type { CatalogEntitlements, CompositeStepInput, CompositeStepTarget, FieldError } from "@/lib/api/types";
import { adm, q } from "@/lib/query";
import { cn } from "@/lib/utils";
import DefinitionGraph from "../DefinitionGraph";
import { parseText } from "../definitionYaml";
import { CommitInput } from "../designer/Properties";
import type { EditorHistory } from "../YamlEditor";
import * as edits from "./edits";
import {
  bindingSources,
  designerProblems,
  findTarget,
  flatten,
  formInputsOf,
  listedTarget,
  openEarlier,
  restOutputs,
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
const panel = "flex flex-col gap-3 rounded-md border border-line bg-surface p-3 text-xs";
const legend = "mb-1.5 text-[11px] font-medium tracking-wide text-muted uppercase";

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

export interface Design {
  /** The editor's text now. */
  text: string;
  /** Writes an edit into the editor, as one step of its history. */
  onEdit: (next: string) => void;
  history: EditorHistory;
  undo: () => void;
  redo: () => void;
  /** The server's problems with a text (its last refusal), shown on their steps while the text is that one. */
  serverProblems?: { text: string; errors: FieldError[] };
}

const kindGroup: Record<StepTarget["kind"], string> = {
  item: "Items",
  awx: "AWX templates",
  workflow: "Custom workflows",
  rest: "REST APIs",
  ask: "People",
};
const byName = "awx-by-name";
const restValue = targetValue({ kind: "rest", ref: "" });
const askValue = targetValue({ kind: "ask", ref: "" });
const restMethods = ["GET", "POST", "PUT", "PATCH", "DELETE"];

/** What a step can run, for a picker: grouped, the step's own first if it isn't listed. */
function targetOptions(targets: CompositeStepTarget[], current?: StepTarget): Option[] {
  const out: Option[] = targets.map((t) => ({
    value: targetValue(listedTarget(t)),
    label: t.title,
    meta: t.awx_workflow ? `${t.ref} (workflow)` : t.ref,
    group: kindGroup[t.kind],
  }));
  if (current?.ref && !findTarget(targets, current))
    out.unshift({
      value: targetValue(current),
      label: current.ref,
      meta: current.kind === "item" ? "not published" : current.kind === "awx" ? "not imported" : "not registered",
      group: kindGroup[current.kind],
    });
  out.push({ value: byName, label: "Another AWX template, by its name", group: kindGroup.awx });
  out.push({ value: restValue, label: "A REST call", group: kindGroup.rest });
  out.push({ value: askValue, label: "A person", group: kindGroup.ask });
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
  if (kind === "rest" || kind === "ask") return { kind, ref: "" };
  if (kind !== "item" && kind !== "workflow") return undefined;
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
  const { text, onEdit, history } = design;
  const targetsQuery = useQuery(q.compositeStepTargets());
  const targets = useMemo(() => targetsQuery.data?.items ?? [], [targetsQuery.data]);
  const def = useMemo(() => parseDefinition(text), [text]);
  const [selected, setSelected] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  /** What to focus once the selected node's details are drawn: they come with the graph, which follows the text a moment later. */
  const focusNext = useRef<string | null>(null);

  const server = design.serverProblems;
  const problemsFor = useCallback(
    (t: string) => {
      const d = parseDefinition(t);
      return [
        ...(d && targetsQuery.data ? designerProblems(d, targetsQuery.data.items, entitlements) : []),
        // The steps' only: the rest are on their lines in the editor.
        ...(server && server.text === t ? server.errors.filter((e) => within(e.field, edits.blueprintPrefix)) : []),
      ];
    },
    [targetsQuery.data, server, entitlements],
  );

  /** Makes an edit, then selects a node and focuses an element of its details. */
  const apply = (make: () => string, then?: { select?: string | null; focus?: string }) => {
    try {
      onEdit(make());
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return;
    }
    if (then?.select !== undefined) setSelected(then.select);
    if (then?.focus) focusNext.current = then.focus;
  };

  // Ctrl+Z and Ctrl+Shift+Z (or Ctrl+Y) outside the text fields, which have their own.
  const box = useRef<HTMLDivElement>(null);
  const { undo, redo } = design;
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
      if ((e.target as HTMLElement).closest("input, textarea, [contenteditable=true]")) return;
      const k = e.key.toLowerCase();
      if (k === "z" && !e.shiftKey) {
        e.preventDefault();
        undo();
      } else if ((k === "z" && e.shiftKey) || k === "y") {
        e.preventDefault();
        redo();
      }
    };
    el.addEventListener("keydown", onKey);
    return () => el.removeEventListener("keydown", onKey);
  }, [undo, redo]);

  // Focus stays in the designer when what had it goes (an undo that removes
  // the step whose details had it): on the selected step's heading or node,
  // else the graph's Tab stop, so Ctrl+Z and Ctrl+Y go on working.
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    let last: HTMLElement | null = null;
    const onFocus = (e: FocusEvent) => {
      last = e.target as HTMLElement;
    };
    const keep = new MutationObserver(() => {
      if (!last || last.isConnected) return;
      last = null;
      if (document.activeElement && document.activeElement !== document.body) return;
      const to =
        el.querySelector<HTMLElement>("#cd-heading") ??
        el.querySelector<HTMLElement>(".react-flow__node[aria-pressed=true]") ??
        el.querySelector<HTMLElement>(".react-flow__node[tabindex='0']") ??
        el;
      to.focus();
    });
    el.addEventListener("focusin", onFocus);
    keep.observe(el, { childList: true, subtree: true });
    return () => {
      el.removeEventListener("focusin", onFocus);
      keep.disconnect();
    };
  }, []);

  const ctx: PanelContext = { text, def: def ?? {}, targets, targetsQuery, apply, focusNext, setSelected };
  const renderDetails = (node: CanvasNode): ReactNode => {
    if (!def)
      return (
        <div className="flex flex-col gap-2">
          <p role="note" className="text-xs text-warn">
            The YAML doesn't parse: fix it in the editor to change the steps here.
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
      <Button size="sm" disabled={!def} onClick={() => setAdding(true)}>
        <Plus aria-hidden />
        Add a step
      </Button>
      <Button size="sm" variant="ghost" disabled={!history.canUndo} onClick={design.undo} aria-keyshortcuts="Control+Z">
        <Undo2 aria-hidden />
        Undo
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={!history.canRedo}
        onClick={design.redo}
        aria-keyshortcuts="Control+Shift+Z Control+Y"
      >
        <Redo2 aria-hidden />
        Redo
      </Button>
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
  apply: (make: () => string, then?: { select?: string | null; focus?: string }) => void;
  focusNext: RefObject<string | null>;
  setSelected: (id: string | null) => void;
}

/** Focuses what an edit asked for, once its details are drawn. */
function useFocusNext(ref: RefObject<string | null>) {
  useEffect(() => {
    const id = ref.current;
    if (!id) return;
    const el = document.getElementById(id);
    if (el) {
      ref.current = null;
      el.focus();
    }
  });
}

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

function PanelHeading({ badge, children }: { badge?: string; children: ReactNode }) {
  return (
    <h4 id="cd-heading" tabIndex={-1} className="flex items-baseline gap-1.5 text-[13px] font-medium outline-none">
      {badge && <span className="text-[10px] tracking-wide text-faint uppercase">{badge}</span>}
      {children}
    </h4>
  );
}

function InputsPanel({ ctx }: { ctx: PanelContext }) {
  useFocusNext(ctx.focusNext);
  const inputs = formInputsOf(ctx.def);
  return (
    <section aria-label="Selected: Request" className={panel}>
      <PanelHeading>Request</PanelHeading>
      <p className="text-muted">
        Steps bind to the form's inputs as <code className="font-mono">inputs.&lt;name&gt;</code>, and to the request's{" "}
        <code className="font-mono">environment</code> and <code className="font-mono">project</code>. The form is
        changed in the Form designer tab.
      </p>
      {inputs.length > 0 ? (
        <ul aria-label="The form's inputs" className="flex flex-col gap-0.5">
          {inputs.map((i) => (
            <li key={i.name}>
              <code className="font-mono">inputs.{i.name}</code> <span className="text-muted">{i.title}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-muted">The form has no inputs yet.</p>
      )}
    </section>
  );
}

function EndPanel({ ctx }: { ctx: PanelContext }) {
  useFocusNext(ctx.focusNext);
  const on = obj(ctx.def.blueprint).rollback_on_failure === true;
  return (
    <section aria-label="Selected: Done" className={panel}>
      <PanelHeading>Done</PanelHeading>
      <div className="flex items-start gap-2 text-[13px]">
        <Checkbox
          id="cd-rollback"
          checked={on}
          onChange={(v) => ctx.apply(() => edits.setRollback(ctx.text, v))}
          className="mt-0.5"
        />
        <div>
          <label htmlFor="cd-rollback">Undo what was built when a step fails</label>
          <p className="text-xs text-muted">
            The VMs built are deleted and each step done is undone by its undo, if it has one, the last first.
          </p>
        </div>
      </div>
    </section>
  );
}

function StepPanel({ node, ctx }: { node: CanvasNode; ctx: PanelContext }) {
  const { text, def, targets, apply } = ctx;
  useFocusNext(ctx.focusNext);
  const [byNameOpen, setByNameOpen] = useState(false);
  const runsFocus = useFocusAfterSelect();
  const [extra, setExtra] = useState<string[]>([]);
  const [undoExtra, setUndoExtra] = useState<string[]>([]);
  const found = edits.readStep(text, node.id);
  if (!found) return <NodeDetails node={node} />;
  const { step, g, j } = found;
  const id = node.id;
  const key = str(step.key);
  const target = targetOf(step);
  const listed = findTarget(targets, target);
  const planned = listed?.planned ?? false;
  const slots = Array.isArray(obj(def.blueprint).steps) ? (obj(def.blueprint).steps as unknown[]).length : 0;
  const sources = bindingSources(def, { g, planned }, targets);
  const open = planned ? [] : openEarlier(def, g, targets);
  const withMap = obj(step.with);
  const rest = target?.kind === "rest";
  const ask = target?.kind === "ask";
  const inputs = listed?.inputs ?? [];
  const others = [...new Set([...Object.keys(withMap), ...extra])].filter((k) => !inputs.some((i) => i.name === k));
  const undo = step.undo === undefined ? undefined : obj(step.undo);
  const title = node.title;
  const flat = flatten(obj(def.blueprint));
  const at = flat.findIndex((s) => s.g === g && s.j === j);

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
    <section aria-label={`Selected: ${title}`} className={panel}>
      <div className="flex flex-wrap items-center gap-2">
        <PanelHeading badge={node.badge}>{title}</PanelHeading>
        <div className="ml-auto flex flex-wrap gap-1">
          <Button id="cd-earlier" size="sm" variant="ghost" disabled={j === null && g === 0} onClick={() => move(-1)}>
            <ArrowUp aria-hidden />
            {j === null ? "Move earlier" : "Run before the others"}
          </Button>
          <Button
            id="cd-later"
            size="sm"
            variant="ghost"
            disabled={j === null && g === slots - 1}
            onClick={() => move(1)}
          >
            <ArrowDown aria-hidden />
            {j === null ? "Move later" : "Run after the others"}
          </Button>
          {j === null && g > 0 && (
            <Button
              id="cd-join"
              size="sm"
              variant="ghost"
              onClick={() => apply(() => edits.joinPrevious(text, id), { focus: "cd-later" })}
            >
              Run with the step before
            </Button>
          )}
          <Button id="cd-remove" size="sm" variant="danger" onClick={remove}>
            <Trash2 aria-hidden />
            Remove step
          </Button>
        </div>
      </div>
      {node.problems.length > 0 && (
        <ul aria-label="Problems with this step" className="flex flex-col gap-0.5 text-bad">
          {node.problems.map((p, i) => (
            <li key={i}>{p}</li>
          ))}
        </ul>
      )}
      <div className="grid gap-3 md:grid-cols-2">
        <Field label="Key" htmlFor="cd-key" hint={key ? `Later steps read its outputs as steps.${key}.` : undefined}>
          <CommitInput
            id="cd-key"
            value={key}
            className="font-mono text-xs"
            onCommit={(v) => {
              const next = v.trim();
              if (!keyPattern.test(next)) {
                toast.error("A key of lower-case letters, digits and underscores.");
                return;
              }
              if (next !== key && flat.some((s) => s.key === next)) {
                toast.error(`Another step has the key ${next}.`);
                return;
              }
              apply(() => edits.setStepField(text, id, "key", next), { select: edits.stepId(next), focus: "cd-key" });
            }}
          />
        </Field>
        <Field label="Title" htmlFor="cd-title">
          <CommitInput
            id="cd-title"
            value={str(step.title)}
            placeholder={listed?.title}
            onCommit={(v) => apply(() => edits.setStepField(text, id, "title", v))}
          />
        </Field>
        <Field
          label="Runs"
          htmlFor="cd-runs"
          className="md:col-span-2"
          hint={
            ctx.targetsQuery.isError
              ? "What steps can run couldn't be loaded."
              : planned
                ? "A VM item: its VMs are planned with the request, from the form's inputs."
                : listed?.summary
          }
        >
          <Select
            id="cd-runs"
            value={runsValue}
            loading={ctx.targetsQuery.isPending}
            placeholder="Choose what it runs"
            options={targetOptions(targets, target)}
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
          <Field label="AWX template's name" htmlFor="cd-awx-name" className="md:col-span-2">
            <CommitInput
              id="cd-awx-name"
              value=""
              className="font-mono text-xs"
              onCommit={(v) => {
                if (!v.trim()) return;
                setByNameOpen(false);
                apply(() => edits.setTarget(text, id, { kind: "awx", ref: v.trim() }), { focus: "cd-runs" });
              }}
            />
          </Field>
        )}
        <Field
          label="When"
          htmlFor="cd-when"
          className="md:col-span-2"
          hint={
            planned
              ? "A VM step has no condition: its VMs are planned when the request is submitted."
              : "A CEL condition: the step is skipped when it is false. Empty: it always runs."
          }
        >
          <CommitInput
            id="cd-when"
            value={str(step.when)}
            className="font-mono text-xs"
            placeholder={planned ? undefined : 'inputs.environment == "prd"'}
            disabled={planned && !str(step.when)}
            onCommit={(v) => apply(() => edits.setStepField(text, id, "when", v))}
          />
        </Field>
      </div>

      {rest && (
        <fieldset className="flex flex-col gap-2">
          <legend className={legend}>REST call</legend>
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

      {!rest && !ask && (
        <fieldset className="flex flex-col gap-2">
          <legend className={legend}>Inputs</legend>
          {target?.kind === "item" && !listed && (
            <p className="text-muted">Its inputs are known once the item it runs is published.</p>
          )}
          {inputs.length === 0 && others.length === 0 && listed && <p className="text-muted">It takes no inputs.</p>}
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
              note={target?.kind === "item" && listed ? "Not an input of its form." : undefined}
            />
          ))}
          {open.length > 0 && (
            <p className="text-muted">
              A custom workflow gives what it returns: write{" "}
              {open.map((k, i) => (
                <span key={k}>
                  {i > 0 && ", "}
                  <code className="font-mono">steps.{k}.&lt;name&gt;</code>
                </span>
              ))}{" "}
              as a CEL expression.
            </p>
          )}
          {(target?.kind !== "item" || !listed) && (
            <AddVariable
              id="cd-add-input"
              rowId={(n) => `cd-in-${n}`}
              label={target?.kind === "awx" ? "Add a variable" : "Add an input"}
              taken={[...inputs.map((i) => i.name), ...others]}
              onAdd={(n) => setExtra((x) => [...x, n])}
            />
          )}
        </fieldset>
      )}

      {!planned && !ask && (
        <fieldset className="flex flex-col gap-2">
          <legend className={legend}>Undo</legend>
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
            reads={[...sources.map((s) => s.value), ...(key ? restOutputs.map((o) => `steps.${key}.${o.name}`) : [])]}
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
    { value: "none", label: "Nothing" },
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
      <Field
        label="Undone by"
        htmlFor="cd-undo"
        hint="What runs when a later step fails and the request's changes are undone."
      >
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
        <Field label="AWX template's name" htmlFor="cd-undo-name">
          <CommitInput
            id="cd-undo-name"
            value=""
            className="font-mono text-xs"
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
            label="Add a variable to the undo"
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

/** A REST call's fields: a step's, or an undo's, whose answer nothing reads. */
function RestCallFields({
  prefix,
  call,
  stepKey,
  undo,
  reads,
  onChange,
}: {
  prefix: string;
  call: Raw;
  stepKey?: string;
  undo?: boolean;
  /** What its URL and body can read at that point. */
  reads: string[];
  onChange: (field: edits.RestField, value: string | number[]) => void;
}) {
  const secrets = useQuery(adm.secrets());
  const method = str(call.method);
  const secret = str(call.secret);
  const accept = Array.isArray(call.accept) ? call.accept.join(", ") : "";
  const stored = secrets.data?.items ?? [];
  const secretOptions: Option[] = [
    { value: "none", label: "No secret" },
    ...stored.map((s) => ({ value: s.name, label: s.name, meta: s.kind })),
    ...(secret && !stored.some((s) => s.name === secret)
      ? [{ value: secret, label: secret, meta: secrets.isPending ? undefined : "not stored" }]
      : []),
  ];
  return (
    <>
      <div className="grid gap-3 md:grid-cols-[8rem_minmax(0,1fr)]">
        <Field label={undo ? "Undo's method" : "Method"} htmlFor={`${prefix}-method`}>
          <Select
            id={`${prefix}-method`}
            value={method}
            placeholder="Choose"
            options={restMethods.map((m) => ({ value: m, label: m }))}
            onChange={(v) => onChange("method", v)}
          />
        </Field>
        <Field
          label={undo ? "Undo's URL" : "URL"}
          htmlFor={`${prefix}-url`}
          hint="The host is fixed, and on the allowed hosts. ${...} fills its path or query, escaped."
        >
          <CommitInput
            id={`${prefix}-url`}
            value={str(call.url)}
            className="font-mono text-xs"
            placeholder="https://cmdb.corp.example/api/ci/${steps.vm.name}"
            onCommit={(v) => onChange("url", v)}
          />
        </Field>
        <Field
          label={undo ? "Undo's body" : "Body"}
          htmlFor={`${prefix}-body`}
          className="md:col-span-2"
          hint={method === "GET" ? "A GET has no body." : "A CEL expression, sent as JSON. Empty: no body."}
        >
          <CommitInput
            id={`${prefix}-body`}
            value={str(call.body)}
            className="font-mono text-xs"
            placeholder={method === "GET" ? undefined : "{'name': steps.vm.fqdn, 'owner': inputs.owner}"}
            disabled={method === "GET" && !str(call.body)}
            onCommit={(v) => onChange("body", v)}
          />
        </Field>
        <Field
          label={undo ? "Undo's secret" : "Secret"}
          htmlFor={`${prefix}-secret`}
          className="md:col-span-2"
          hint={
            secrets.isError
              ? "The stored secrets couldn't be loaded."
              : "Sent as a bearer token, basic credentials or a header, as stored."
          }
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
          label={undo ? "Undo's accepted statuses" : "Accepted statuses"}
          htmlFor={`${prefix}-accept`}
          className={undo ? "md:col-span-2" : undefined}
          hint="Others fail the step. Empty: any 2xx."
        >
          <CommitInput
            id={`${prefix}-accept`}
            value={accept}
            className="font-mono text-xs"
            placeholder={undo ? "200, 204, 404" : "200, 201"}
            onCommit={(v) => {
              const statuses = writtenStatuses(v);
              if (statuses) onChange("accept", statuses);
              else toast.error("HTTP statuses, as numbers: 200, 201.");
            }}
          />
        </Field>
        {!undo && (
          <Field
            label="Keep"
            htmlFor={`${prefix}-keep`}
            hint={
              <>
                A JSONPath into the answer. Empty: all of it. Later steps read{" "}
                <code className="font-mono">steps.{stepKey || "<key>"}.json</code> and{" "}
                <code className="font-mono">.status</code>.
              </>
            }
          >
            <CommitInput
              id={`${prefix}-keep`}
              value={str(call.keep)}
              className="font-mono text-xs"
              placeholder="$.result"
              onCommit={(v) => onChange("keep", v)}
            />
          </Field>
        )}
      </div>
      <p className="text-muted">
        It can read{" "}
        {reads.map((r, i) => (
          <span key={r}>
            {i > 0 && ", "}
            <code className="font-mono">{r}</code>
          </span>
        ))}
        .
      </p>
    </>
  );
}

/** People, written as user IDs on one line. */
const writtenUsers = (text: string) => text.split(/[\s,]+/).filter(Boolean);
const listText = (v: unknown) => (Array.isArray(v) ? v.map(String).join(", ") : "");

const questionTypes: Option[] = [
  { value: "string", label: "Text" },
  { value: "number", label: "A number" },
  { value: "boolean", label: "Yes or no" },
  { value: "choice", label: "A choice" },
];
const onTimeoutOptions: Option[] = [
  { value: "fail", label: "The step fails" },
  { value: "continue", label: "It goes on with no answer" },
  { value: "escalate", label: "It escalates" },
];

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
        <legend className={legend}>Who is asked</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="OIDC group" htmlFor="cd-ask-group" hint="Its members, but for the requester.">
            <CommitInput id="cd-ask-group" value={str(ask.group)} onCommit={(v) => set("group", v)} />
          </Field>
          <Field
            label="Approvers"
            htmlFor="cd-ask-approvers"
            hint="User IDs, separated by commas. Nobody answers their own request unless it asks the requester."
          >
            <CommitInput
              id="cd-ask-approvers"
              value={listText(ask.approvers)}
              className="font-mono text-xs"
              onCommit={(v) => set("approvers", writtenUsers(v))}
            />
          </Field>
          <div className="flex items-center gap-2">
            <Checkbox id="cd-ask-requester" checked={ask.requester === true} onChange={(v) => set("requester", v)} />
            <label htmlFor="cd-ask-requester">The requester</label>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="cd-ask-owner" checked={ask.owner === true} onChange={(v) => set("owner", v)} />
            <label htmlFor="cd-ask-owner">The owner</label>
          </div>
        </div>
      </fieldset>

      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>What they answer</legend>
        <Field
          label="Message"
          htmlFor="cd-ask-message"
          hint="Fixed text, for those asked: nothing of the request is filled in."
        >
          <CommitInput id="cd-ask-message" value={str(ask.message)} onCommit={(v) => set("message", v)} />
        </Field>
        {form.length === 0 && (
          <p className="text-muted">No questions: they approve or reject, and a reject fails it.</p>
        )}
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
            Add a question
          </Button>
        </div>
        <p className="text-muted">
          Later steps read <code className="font-mono">{reads}.answered</code>,{" "}
          <code className="font-mono">{reads}.by</code>, <code className="font-mono">{reads}.comment</code>
          {form.length > 0 && (
            <>
              {" "}
              and <code className="font-mono">{reads}.answer.&lt;key&gt;</code>
            </>
          )}
          .
        </p>
      </fieldset>

      <fieldset className="flex flex-col gap-2">
        <legend className={legend}>If nobody answers</legend>
        <div className="grid gap-3 md:grid-cols-2">
          <Field
            label="Timeout"
            htmlFor="cd-ask-timeout"
            hint="A duration such as 48h. Empty: it waits until answered."
          >
            <CommitInput
              id="cd-ask-timeout"
              value={timeout}
              className="font-mono text-xs"
              placeholder="48h"
              onCommit={(v) => set("timeout", v)}
            />
          </Field>
          <Field label="Then" htmlFor="cd-ask-on-timeout">
            <Select
              id="cd-ask-on-timeout"
              value={onTimeout || "fail"}
              options={onTimeoutOptions}
              onChange={(v) => set("on_timeout", v)}
            />
          </Field>
          {onTimeout === "escalate" && (
            <>
              <Field label="Escalate to OIDC group" htmlFor="cd-ask-esc-group">
                <CommitInput
                  id="cd-ask-esc-group"
                  value={str(escalate.group)}
                  onCommit={(v) => apply(() => edits.setAskEscalate(text, id, "group", v))}
                />
              </Field>
              <Field
                label="Escalate to approvers"
                htmlFor="cd-ask-esc-approvers"
                hint="They get one more timeout; then the step fails."
              >
                <CommitInput
                  id="cd-ask-esc-approvers"
                  value={listText(escalate.approvers)}
                  className="font-mono text-xs"
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
  const { text, apply } = ctx;
  const prefix = `cd-ask-q${index}`;
  const type = str(question.type);
  const set = (field: edits.QuestionField, v: string | number | boolean | string[] | undefined) =>
    apply(() => edits.setAskQuestion(text, id, index, field, v));
  const key = str(question.key);
  return (
    <fieldset className="flex flex-col gap-2 rounded-md border border-line p-2">
      <legend className="px-1 text-xs font-medium">Question {index + 1}</legend>
      <div className="grid gap-3 md:grid-cols-2">
        <Field
          label="Key"
          htmlFor={`${prefix}-key`}
          hint={key ? `Later steps read steps.${stepKey || "<key>"}.answer.${key}.` : undefined}
        >
          <CommitInput id={`${prefix}-key`} value={key} className="font-mono text-xs" onCommit={(v) => set("key", v)} />
        </Field>
        <Field label="Question" htmlFor={`${prefix}-label`}>
          <CommitInput id={`${prefix}-label`} value={str(question.label)} onCommit={(v) => set("label", v)} />
        </Field>
        <Field label="Type" htmlFor={`${prefix}-type`}>
          <Select
            id={`${prefix}-type`}
            value={type}
            placeholder="Choose"
            options={questionTypes}
            onChange={(v) => v !== type && set("type", v)}
          />
        </Field>
        {type === "choice" && (
          <Field label="Options" htmlFor={`${prefix}-options`} hint="What it may be, separated by commas.">
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
        <Field label="Default" htmlFor={`${prefix}-default`} hint="What it reads if left blank, or if nobody answers.">
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
          <label htmlFor={`${prefix}-required`}>An answer is required</label>
        </div>
      </div>
      <div>
        <Button
          size="sm"
          variant="ghost"
          aria-label={`Remove question ${index + 1}`}
          onClick={() => apply(() => edits.removeAskQuestion(text, id, index), { focus: "cd-ask-add" })}
        >
          <Trash2 aria-hidden />
          Remove
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
  const [name, setName] = useState("");
  const [error, setError] = useState<string>();
  const add = () => {
    const n = name.trim();
    if (!variablePattern.test(n)) return setError("Letters, digits and underscores, not starting with a digit.");
    if (taken.includes(n)) return setError(`${n} is there already.`);
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
          className="font-mono text-xs"
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
        Add
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
  const [writing, setWriting] = useState(false);
  const focus = useFocusAfterSelect();
  const matched = typeof value === "string" && !writing ? sources.find((s) => s.value === value) : undefined;
  const mode = matched ? matched.value : value === undefined && !writing ? unbound : written;
  const options: Option[] = [
    { value: unbound, label: "Not bound" },
    { value: written, label: "A value or CEL expression" },
    ...sources.map((s) => ({ value: s.value, label: s.label, meta: s.value, group: s.group })),
  ];
  const missing = input.required && value === undefined;
  return (
    <div className="grid items-start gap-x-3 gap-y-1 sm:grid-cols-[minmax(0,2fr)_minmax(0,3fr)]">
      <div className="flex min-w-0 flex-col pt-1.5">
        <label htmlFor={id} className="text-xs font-medium text-fg">
          {input.title}
          {input.title !== input.name && <span className="ml-1.5 font-mono text-[11px] text-faint">{input.name}</span>}
        </label>
        {(missing || note || input.description) && (
          <span id={`${id}-note`} className={cn("text-[11px]", missing ? "text-warn" : "text-muted")}>
            {missing ? "Required, and not bound." : (note ?? input.description)}
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
              label={`${input.title}: value or CEL expression`}
              placeholder={'"a value" or inputs.name + "_x"'}
              className="font-mono text-xs"
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
            variant="ghost"
            aria-label={`Remove ${input.name}`}
            title={`Remove ${input.name}`}
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
  selected,
  onAdd,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  text: string;
  def: Raw;
  targets: CompositeStepTarget[];
  loading: boolean;
  selected?: { key: string; name: string };
  onAdd: (step: Raw, where: edits.Where) => void;
}) {
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
          findTarget(targets, t)?.title ?? (t.kind === "rest" ? "rest_call" : t.kind === "ask" ? "person" : t.ref),
        ),
      );
  };
  const submit = () => {
    const e: Record<string, string> = {};
    if (!target) e.runs = choice === byName ? "Name the AWX template." : "Choose what it runs.";
    if (!keyPattern.test(key)) e.key = "A key of lower-case letters, digits and underscores.";
    else if (edits.stepKeys(text).includes(key)) e.key = `Another step has the key ${key}.`;
    setErrors(e);
    if (Object.keys(e).length > 0 || !target) return;
    const step = newStep(key, title.trim() || listed?.title || "", target, listed?.inputs ?? [], def);
    const at: edits.Where =
      selected && where === "after"
        ? { after: edits.stepId(selected.key) }
        : selected && where === "with"
          ? { with: edits.stepId(selected.key) }
          : "end";
    added.current = true;
    onAdd(step, at);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        title="Add a step"
        description="It runs one of the catalog's items, an AWX template or a custom workflow, calls a REST API, or waits for a person. Inputs named as the form's are bound to them."
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
          <Field label="What it runs" htmlFor="cd-new-runs" error={errors.runs}>
            <Select
              id="cd-new-runs"
              value={choice}
              loading={loading}
              placeholder="Choose what it runs"
              options={targetOptions(targets)}
              onCloseAutoFocus={focus.onCloseAutoFocus}
              onChange={(v) => {
                if (v === byName) focus.later("cd-new-awx");
                setChoice(v);
                suggest(v === byName ? undefined : parseTarget(v, targets));
              }}
            />
          </Field>
          {choice === byName && (
            <Field label="AWX template's name" htmlFor="cd-new-awx">
              <Input
                id="cd-new-awx"
                value={awxName}
                className="font-mono text-xs"
                onChange={(e) => {
                  setAwxName(e.target.value);
                  suggest(e.target.value.trim() ? { kind: "awx", ref: e.target.value.trim() } : undefined);
                }}
              />
            </Field>
          )}
          <div className="grid gap-3">
            <Field
              label="Key"
              htmlFor="cd-new-key"
              error={errors.key}
              hint="Later steps read its outputs as steps.<key>."
            >
              <Input
                id="cd-new-key"
                value={key}
                className="font-mono text-xs"
                onChange={(e) => {
                  setKey(e.target.value);
                  setKeyTouched(true);
                }}
              />
            </Field>
            <Field label="Title" htmlFor="cd-new-title">
              <Input
                id="cd-new-title"
                value={title}
                placeholder={listed?.title}
                onChange={(e) => setTitle(e.target.value)}
              />
            </Field>
          </div>
          {selected ? (
            <fieldset className="flex flex-col gap-1.5 text-[13px]">
              <legend className="mb-1 text-xs font-medium">When it runs</legend>
              {(
                [
                  ["after", `After ${selected.name}`],
                  ["with", `At the same time as ${selected.name}`],
                  ["end", "At the end"],
                ] as const
              ).map(([v, label]) => (
                <label
                  key={v}
                  className={cn(
                    "flex cursor-pointer items-center gap-2 rounded-md border border-line px-2.5 py-2 hover:bg-sunken",
                    where === v && "border-accent bg-accent-soft hover:bg-accent-soft",
                  )}
                >
                  <input
                    type="radio"
                    name="cd-new-where"
                    value={v}
                    checked={where === v}
                    onChange={() => setWhere(v)}
                    className="accent-[var(--accent)]"
                  />
                  {label}
                </label>
              ))}
            </fieldset>
          ) : (
            <p className="text-xs text-muted">
              It runs at the end. Select a step first to add one after it or beside it.
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" variant="primary">
              Add step
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
