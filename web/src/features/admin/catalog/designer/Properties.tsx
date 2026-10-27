import { useId, useState } from "react";
import { Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { isApiError } from "@/lib/api/client";
import type { FieldValues, UiWidget, ValueSource } from "@/lib/api/types";
import { cn } from "@/lib/utils";
import type { Selection } from "./FieldList";
import {
  builtinSources,
  choiceWidgets,
  isRequired,
  moveToSection,
  namePattern,
  props,
  removeField,
  removeSection,
  renameField,
  setInitial,
  setRequired,
  setRule,
  setWidget,
  updateProp,
  updateSection,
  updateUi,
  widgetLabels,
  type Def,
  type Problem,
  type Problems,
} from "./model";

const widgetOptions = Object.entries(widgetLabels).map(([value, label]) => ({ value, label }));
const mono = "font-mono text-xs";

/** A problem the server found, under the control it is about. */
function ProblemList({ id, list }: { id: string; list: Problem[] }) {
  if (list.length === 0) return null;
  return (
    <ul id={id} className="flex flex-col gap-0.5 text-xs text-bad">
      {list.map((p, i) => (
        <li key={i}>{p.message}</li>
      ))}
    </ul>
  );
}

/** A text input written to the definition when it loses focus (or on Enter). */
function CommitInput({
  id,
  value,
  onCommit,
  className,
  invalid,
  describedBy,
}: {
  id: string;
  value: string;
  onCommit: (v: string) => void;
  className?: string;
  invalid?: boolean;
  describedBy?: string;
}) {
  const [draft, setDraft] = useState(value);
  const [shown, setShown] = useState(value);
  if (shown !== value) {
    setShown(value);
    setDraft(value);
  }
  return (
    <Input
      id={id}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => draft !== value && onCommit(draft)}
      onKeyDown={(e) => e.key === "Enter" && draft !== value && onCommit(draft)}
      className={className}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
    />
  );
}

function optionsText(options: { value: string; label: string }[] | undefined) {
  return (options ?? []).map((o) => (o.label && o.label !== o.value ? `${o.value} | ${o.label}` : o.value)).join("\n");
}

function parseOptions(text: string) {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) => {
      const [value = "", label] = l.split("|").map((x) => x.trim());
      return { value, label: label || value };
    });
}

export interface SourceTest {
  run: (field: string) => Promise<FieldValues>;
  /** The inputs the preview has, which the test sends. */
  values: Record<string, unknown>;
  saved: boolean;
}

function TestSource({ name, dependsOn, test }: { name: string; dependsOn: string[]; test: SourceTest }) {
  const [result, setResult] = useState<{ ok?: FieldValues; error?: string; with?: string } | null>(null);
  const [running, setRunning] = useState(false);
  const id = useId();
  const run = async () => {
    setRunning(true);
    const used = dependsOn.map((d) => `${d} = ${JSON.stringify(test.values[d] ?? null)}`).join(", ");
    try {
      setResult({ ok: await test.run(name), with: used });
    } catch (e) {
      setResult({ error: isApiError(e) ? (e.problem.detail ?? e.problem.title) : String(e), with: used });
    } finally {
      setRunning(false);
    }
  };
  return (
    <div className="flex flex-col gap-1.5">
      <Button
        size="sm"
        onClick={() => void run()}
        disabled={!test.saved || running}
        title={test.saved ? undefined : "Wait until the draft is saved"}
        aria-describedby={result ? id : undefined}
      >
        {running ? "Testing…" : "Test the source"}
      </Button>
      {result && (
        <div id={id} role="status" className="rounded-md border border-line bg-sunken p-2 text-xs">
          {result.with && <p className="mb-1 text-faint">With {result.with}, as the preview's user</p>}
          {result.error && <p className="text-bad">{result.error}</p>}
          {result.ok && (
            <>
              {result.ok.value !== undefined && (
                <p>
                  Value: <span className={mono}>{JSON.stringify(result.ok.value)}</span>
                </p>
              )}
              {result.ok.options && (
                <p>
                  {result.ok.options.length} option{result.ok.options.length === 1 ? "" : "s"}
                  {result.ok.options.length > 0 && ": "}
                  {result.ok.options
                    .slice(0, 5)
                    .map((o) => o.label)
                    .join(", ")}
                  {result.ok.options.length > 5 && ", …"}
                </p>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function SourceEditor({
  name,
  def,
  onChange,
  problems,
  test,
}: {
  name: string;
  def: Def;
  onChange: (d: Def) => void;
  problems: Problem[];
  test: SourceTest;
}) {
  const ui = def.ui_schema.fields[name]!;
  const src = ui.value_source;
  const params = (src?.params ?? {}) as Record<string, unknown>;
  const at = (p: string) =>
    problems.filter((x) => x.at === `value_source/${p}` || x.at.startsWith(`value_source/${p}/`));
  const setSource = (s: ValueSource | undefined) => onChange(updateUi(def, name, { value_source: s }));
  const setParam = (k: string, v: string) => {
    const next = { ...params };
    if (v) next[k] = v;
    else delete next[k];
    setSource({ ...src!, params: next });
  };
  const param = (k: string, label: string, hint?: string, code = true) => {
    const id = `prop-src-${k}`;
    const list = at(`params/${k}`);
    return (
      <Field label={label} htmlFor={id} hint={hint}>
        <CommitInput
          id={id}
          value={typeof params[k] === "string" ? params[k] : ""}
          onCommit={(v) => setParam(k, v)}
          className={code ? mono : undefined}
          invalid={list.length > 0}
          describedBy={list.length ? `${id}-problems` : undefined}
        />
        <ProblemList id={`${id}-problems`} list={list} />
      </Field>
    );
  };
  const others = Object.keys(def.ui_schema.fields).filter((f) => f !== name);
  const deps = ui.depends_on ?? [];
  return (
    <fieldset className="flex flex-col gap-2.5 border-t border-line pt-3">
      <legend className="sr-only">Value source</legend>
      <Field label="Value source" htmlFor="prop-src-kind">
        <Select
          id="prop-src-kind"
          value={src?.kind ?? "none"}
          onChange={(k) =>
            setSource(
              k === "none"
                ? undefined
                : k === "builtin"
                  ? { kind: "builtin", name: builtinSources[0] }
                  : k === "rest"
                    ? { kind: "rest", params: { url: "https://", items: "$[*]", value: "$.id", label: "$.name" } }
                    : { kind: "cel", params: { options: '["a", "b"]' } },
            )
          }
          options={[
            { value: "none", label: "None (static options or free input)" },
            { value: "builtin", label: "Built in" },
            { value: "rest", label: "REST service" },
            { value: "cel", label: "CEL expression" },
          ]}
        />
      </Field>
      <ProblemList
        id="prop-src-problems"
        list={at("kind").concat(at("params").filter((p) => p.at === "value_source/params"))}
      />
      {src?.kind === "builtin" && (
        <>
          <Field label="Source" htmlFor="prop-src-name">
            <Select
              id="prop-src-name"
              value={src.name ?? ""}
              onChange={(v) => setSource({ ...src, name: v })}
              options={builtinSources.map((s) => ({ value: s, label: s }))}
            />
          </Field>
          <ProblemList id="prop-src-name-problems" list={at("name")} />
          {param("cluster", "Cluster", "For datastores: ${inputs.cluster}")}
          {param("network", "Network", "For subnets and addresses: ${inputs.network}")}
        </>
      )}
      {src?.kind === "rest" && (
        <>
          {param("url", "URL", "A host of RVAV_REST_SOURCE_HOSTS; ${inputs.x} is escaped.")}
          <Field label="Method" htmlFor="prop-src-method">
            <Select
              id="prop-src-method"
              value={typeof params.method === "string" ? params.method : "GET"}
              onChange={(v) => setParam("method", v === "GET" ? "" : v)}
              options={[
                { value: "GET", label: "GET" },
                { value: "POST", label: "POST" },
              ]}
            />
          </Field>
          {params.method === "POST" && param("body", "Body (JSON)")}
          {param("secret", "Secret", "A stored secret's name, sent as bearer, basic or header.", false)}
          {param("items", "Items (JSONPath)")}
          {param("value", "Value (JSONPath in an item)")}
          {param("label", "Label (JSONPath in an item)")}
          {param("meta", "Detail (JSONPath in an item)")}
        </>
      )}
      {src?.kind === "cel" && (
        <>
          {param("options", "Options", "A list of strings or of {value, label, meta}.")}
          {param("value", "Value", "A computed value instead of options.")}
        </>
      )}
      {src && others.length > 0 && (
        <fieldset className="flex flex-col gap-1">
          <legend className="mb-1 text-xs font-medium">Depends on</legend>
          <p className="text-[11px] text-faint">A change to these inputs fetches the values again.</p>
          {others.map((o) => (
            <label key={o} htmlFor={`prop-dep-${o}`} className="flex items-center gap-2 text-xs">
              <Checkbox
                id={`prop-dep-${o}`}
                checked={deps.includes(o)}
                onChange={(on) =>
                  onChange(updateUi(def, name, { depends_on: on ? [...deps, o] : deps.filter((d) => d !== o) }))
                }
              />
              <span className="font-mono">{o}</span>
            </label>
          ))}
        </fieldset>
      )}
      {src && <TestSource name={name} dependsOn={deps} test={test} />}
    </fieldset>
  );
}

const rules = [
  { key: "visible", label: "Shown when", hint: 'For example environment == "prd". Empty: always.' },
  { key: "required", label: "Required when", hint: "Empty: as the Required box says." },
  { key: "read_only", label: "Read only when", hint: "" },
  { key: "value", label: "Computed value", hint: "The field shows this and can't be changed." },
] as const;

function FieldProperties({
  def,
  name,
  onChange,
  onSelect,
  problems,
  test,
}: {
  def: Def;
  name: string;
  onChange: (d: Def) => void;
  onSelect: (s: Selection) => void;
  problems: Problem[];
  test: SourceTest;
}) {
  const ui = def.ui_schema.fields[name];
  const prop = props(def)[name];
  const [nameError, setNameError] = useState("");
  if (!ui) return <p className="text-xs text-muted">This input has no UI field: add it in the YAML.</p>;
  const at = (p: string) => problems.filter((x) => x.at === p);
  const section = def.ui_schema.layout.sections.find((s) => s.fields.includes(name));
  const textual = ["text", "textarea", "secret"].includes(ui.widget);
  const numeric = prop?.type === "integer" || prop?.type === "number";
  const initial = def.initial_values[name];
  const general = problems.filter((p) => p.at === "" || p.at.startsWith("schema"));
  return (
    <div className="flex flex-col gap-3">
      <ProblemList id="prop-problems" list={general} />
      <Field
        label="Name"
        htmlFor="prop-name"
        hint={
          nameError || "The input's key: inputs." + name + " in rules and the blueprint, which a rename doesn't change."
        }
      >
        <CommitInput
          id="prop-name"
          value={name}
          className={mono}
          invalid={Boolean(nameError)}
          onCommit={(to) => {
            if (!namePattern.test(to)) return setNameError("A lower-case letter, then letters, digits and _.");
            if (to in def.ui_schema.fields || to in props(def)) return setNameError(`"${to}" is taken.`);
            setNameError("");
            onChange(renameField(def, name, to));
            onSelect({ kind: "field", name: to });
          }}
        />
      </Field>
      <Field label="Label" htmlFor="prop-label">
        <Input
          id="prop-label"
          value={ui.label}
          onChange={(e) => onChange(updateUi(def, name, { label: e.target.value }))}
          aria-invalid={at("label").length > 0 || undefined}
          aria-describedby={at("label").length ? "prop-label-problems" : undefined}
        />
        <ProblemList id="prop-label-problems" list={at("label")} />
      </Field>
      <div className="grid grid-cols-2 gap-2">
        <Field label="Widget" htmlFor="prop-widget">
          <Select
            id="prop-widget"
            value={ui.widget}
            onChange={(w) => onChange(setWidget(def, name, w as UiWidget))}
            options={widgetOptions}
          />
        </Field>
        <Field label="Section" htmlFor="prop-section">
          <Select
            id="prop-section"
            value={section?.key ?? ""}
            placeholder="None"
            onChange={(k) => onChange(moveToSection(def, name, k))}
            options={def.ui_schema.layout.sections.map((s) => ({ value: s.key, label: s.title || s.key }))}
          />
        </Field>
      </div>
      {prop && (
        <label htmlFor="prop-required" className="flex items-center gap-2 text-[13px]">
          <Checkbox
            id="prop-required"
            checked={isRequired(def, name)}
            onChange={(on) => onChange(setRequired(def, name, on))}
          />
          Required
        </label>
      )}
      <Field label={ui.widget === "info" ? "Text" : "Help"} htmlFor="prop-help">
        <Textarea
          id="prop-help"
          value={ui.help ?? ""}
          className="min-h-14"
          onChange={(e) => onChange(updateUi(def, name, { help: e.target.value }))}
        />
      </Field>
      {textual && (
        <Field label="Placeholder" htmlFor="prop-placeholder">
          <Input
            id="prop-placeholder"
            value={ui.placeholder ?? ""}
            onChange={(e) => onChange(updateUi(def, name, { placeholder: e.target.value }))}
          />
        </Field>
      )}
      {textual && prop && (
        <div className="grid grid-cols-2 gap-2">
          <Field label="At most (characters)" htmlFor="prop-maxlength">
            <Input
              id="prop-maxlength"
              type="number"
              min={1}
              value={typeof prop.maxLength === "number" ? prop.maxLength : ""}
              onChange={(e) =>
                onChange(updateProp(def, name, { maxLength: e.target.value ? Number(e.target.value) : undefined }))
              }
            />
          </Field>
          <Field label="Pattern" htmlFor="prop-pattern">
            <CommitInput
              id="prop-pattern"
              className={mono}
              value={typeof prop.pattern === "string" ? prop.pattern : ""}
              onCommit={(v) => onChange(updateProp(def, name, { pattern: v || undefined }))}
            />
          </Field>
        </div>
      )}
      {numeric && (
        <div className="grid grid-cols-2 gap-2">
          {(["minimum", "maximum"] as const).map((k) => (
            <Field key={k} label={k === "minimum" ? "Minimum" : "Maximum"} htmlFor={`prop-${k}`}>
              <Input
                id={`prop-${k}`}
                type="number"
                value={typeof prop[k] === "number" ? prop[k] : ""}
                onChange={(e) =>
                  onChange(updateProp(def, name, { [k]: e.target.value ? Number(e.target.value) : undefined }))
                }
              />
            </Field>
          ))}
        </div>
      )}
      {choiceWidgets.includes(ui.widget) && !ui.value_source && (
        <Field label="Options" htmlFor="prop-options" hint="One per line: value | label.">
          <OptionsInput
            value={optionsText(ui.options)}
            onCommit={(t) => onChange(updateUi(def, name, { options: parseOptions(t) }))}
          />
        </Field>
      )}
      {prop && ui.widget !== "computed" && (
        <Field label="Initial value" htmlFor="prop-initial">
          {prop.type === "boolean" ? (
            <Checkbox
              id="prop-initial"
              checked={initial === true}
              onChange={(on) => onChange(setInitial(def, name, on ? true : undefined))}
            />
          ) : prop.type === "string" || numeric ? (
            <CommitInput
              id="prop-initial"
              value={typeof initial === "string" || typeof initial === "number" ? String(initial) : ""}
              invalid={at("initial").length > 0}
              onCommit={(v) => onChange(setInitial(def, name, v === "" ? undefined : numeric ? Number(v) : v))}
            />
          ) : (
            <p className="text-xs text-faint">Set lists and objects in the YAML.</p>
          )}
          <ProblemList id="prop-initial-problems" list={at("initial")} />
        </Field>
      )}
      {ui.widget !== "info" && (
        <SourceEditor name={name} def={def} onChange={onChange} problems={problems} test={test} />
      )}
      <fieldset className="flex flex-col gap-2.5 border-t border-line pt-3">
        <legend className="sr-only">Rules</legend>
        <p className="text-xs font-medium">
          Rules <span className="font-normal text-faint">(CEL over inputs, environment, project and user)</span>
        </p>
        {rules.map((r) => {
          const id = `prop-rule-${r.key}`;
          const list = at(`rules/${r.key}`);
          return (
            <Field key={r.key} label={r.label} htmlFor={id} hint={r.hint || undefined}>
              <CommitInput
                id={id}
                className={mono}
                value={ui.rules?.[r.key] ?? ""}
                invalid={list.length > 0}
                describedBy={list.length ? `${id}-problems` : undefined}
                onCommit={(v) => onChange(setRule(def, name, r.key, v))}
              />
              <ProblemList id={`${id}-problems`} list={list} />
            </Field>
          );
        })}
      </fieldset>
      {prop && (
        <fieldset className="flex flex-col gap-1.5 border-t border-line pt-3 text-xs">
          <legend className="sr-only">Handling</legend>
          {(
            [
              ["secret", "Secret: stored encrypted, never shown again, passed to AWX only at launch"],
              ["expose_to_inventory", "A host variable in the AWX inventory"],
              ["approver_editable", "Approvers may change it"],
            ] as const
          ).map(([k, label]) => (
            <label key={k} htmlFor={`prop-${k}`} className="flex items-start gap-2">
              <Checkbox
                id={`prop-${k}`}
                checked={Boolean(ui[k])}
                onChange={(on) => onChange(updateUi(def, name, { [k]: on || undefined }))}
              />
              {label}
            </label>
          ))}
        </fieldset>
      )}
      <div className="border-t border-line pt-3">
        <Button
          size="sm"
          variant="danger"
          onClick={() => {
            onChange(removeField(def, name));
            onSelect(null);
          }}
        >
          Remove {ui.label || name}
        </Button>
      </div>
    </div>
  );
}

function OptionsInput({ value, onCommit }: { value: string; onCommit: (v: string) => void }) {
  const [draft, setDraft] = useState(value);
  const [shown, setShown] = useState(value);
  if (shown !== value) {
    setShown(value);
    setDraft(value);
  }
  return (
    <Textarea
      id="prop-options"
      className={cn("min-h-20", mono)}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => draft !== value && onCommit(draft)}
    />
  );
}

function SectionProperties({
  def,
  sectionKey,
  onChange,
  onSelect,
  problems,
}: {
  def: Def;
  sectionKey: string;
  onChange: (d: Def) => void;
  onSelect: (s: Selection) => void;
  problems: Problem[];
}) {
  const s = def.ui_schema.layout.sections.find((x) => x.key === sectionKey);
  if (!s) return null;
  return (
    <div className="flex flex-col gap-3">
      <ProblemList id="section-problems" list={problems} />
      <Field label="Title" htmlFor="section-title">
        <Input
          id="section-title"
          value={s.title}
          onChange={(e) => onChange(updateSection(def, s.key, { title: e.target.value }))}
        />
      </Field>
      <Field label="Key" htmlFor="section-key">
        <CommitInput
          id="section-key"
          className={mono}
          value={s.key}
          onCommit={(k) => {
            if (!k || def.ui_schema.layout.sections.some((x) => x.key === k)) return;
            onChange(updateSection(def, s.key, { key: k }));
            onSelect({ kind: "section", key: k });
          }}
        />
      </Field>
      <Field label="Description" htmlFor="section-description">
        <Input
          id="section-description"
          value={s.description ?? ""}
          onChange={(e) => onChange(updateSection(def, s.key, { description: e.target.value }))}
        />
      </Field>
      <Field label="Columns" htmlFor="section-columns">
        <Select
          id="section-columns"
          value={String(s.columns ?? 2)}
          onChange={(v) => onChange(updateSection(def, s.key, { columns: Number(v) }))}
          options={["1", "2", "3", "4"].map((v) => ({ value: v, label: v }))}
        />
      </Field>
      <div className="border-t border-line pt-3">
        <Button
          size="sm"
          variant="danger"
          disabled={s.fields.length > 0}
          title={s.fields.length > 0 ? "Move or remove its fields first" : undefined}
          onClick={() => {
            onChange(removeSection(def, s.key));
            onSelect(null);
          }}
        >
          Remove section
        </Button>
      </div>
    </div>
  );
}

export function Properties({
  def,
  selection,
  onSelect,
  onChange,
  problems,
  test,
}: {
  def: Def;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onChange: (d: Def) => void;
  problems: Problems;
  test: SourceTest;
}) {
  const sectionIndex =
    selection?.kind === "section" ? def.ui_schema.layout.sections.findIndex((s) => s.key === selection.key) : -1;
  return (
    <Panel title={selection?.kind === "section" ? "Section" : "Properties"}>
      {!selection && <p className="text-xs text-muted">Choose a field or a section to change it.</p>}
      {selection?.kind === "field" && (
        <FieldProperties
          key={selection.name}
          def={def}
          name={selection.name}
          onChange={onChange}
          onSelect={onSelect}
          problems={problems.fields[selection.name] ?? []}
          test={test}
        />
      )}
      {selection?.kind === "section" && (
        <SectionProperties
          key={selection.key}
          def={def}
          sectionKey={selection.key}
          onChange={onChange}
          onSelect={onSelect}
          problems={problems.sections[sectionIndex] ?? []}
        />
      )}
    </Panel>
  );
}
