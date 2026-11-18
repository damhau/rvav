import { useId, useState } from "react";
import { Panel } from "@/components/ui/panel";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { isApiError } from "@/lib/api/client";
import type { FieldStates, FieldValues, UiColumn, UiWidget, ValueSource } from "@/lib/api/types";
import { cn, localInput, withOffset, zoneLabel } from "@/lib/utils";
import { focusLater, type Selection } from "./FieldList";
import {
  addColumn,
  builtinSources,
  choiceWidgets,
  isColumnRequired,
  isDecimal,
  isRequired,
  moveToSection,
  namePattern,
  ownSources,
  props,
  removeColumn,
  removeField,
  removeSection,
  renameField,
  setDecimal,
  setInitial,
  setRequired,
  setRule,
  setWidget,
  updateColumn,
  updateProp,
  updateSection,
  updateUi,
  widgetLabels,
  type Def,
  type Problem,
  type Problems,
} from "./model";

const widgetOptions = Object.entries(widgetLabels).map(([value, label]) => ({ value, label }));
const mono = "font-mono text-secondary";

/** A problem the server found, under the control it is about. */
function ProblemList({ id, list }: { id: string; list: Problem[] }) {
  if (list.length === 0) return null;
  return (
    <ul id={id} className="flex flex-col gap-0.5 text-secondary text-bad">
      {list.map((p, i) => (
        <li key={i}>{p.message}</li>
      ))}
    </ul>
  );
}

/** A text input written to the definition when it loses focus (or on Enter). */
export function CommitInput({
  id,
  value,
  onCommit,
  className,
  invalid,
  describedBy,
  label,
  placeholder,
  disabled,
  "aria-describedby": noteIds,
  "aria-invalid": markedInvalid,
}: {
  id: string;
  value: string;
  onCommit: (v: string) => void;
  className?: string;
  invalid?: boolean;
  describedBy?: string;
  /** Its name, when no label element names it. */
  label?: string;
  placeholder?: string;
  disabled?: boolean;
  /** Set by its Field: the field's help or problem. */
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
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
      aria-invalid={invalid || markedInvalid || undefined}
      aria-describedby={[describedBy, noteIds].filter(Boolean).join(" ") || undefined}
      aria-label={label}
      placeholder={placeholder}
      disabled={disabled}
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
        <div id={id} role="status" className="rounded-md border border-line bg-sunken p-2 text-secondary">
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
  const own = ownSources[ui.widget];
  const src: ValueSource | undefined = ui.value_source ?? (own ? { kind: "builtin", name: own } : undefined);
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
      {own ? (
        <>
          <p className="text-secondary text-muted">
            Its choices come from <span className={mono}>{own}</span>: only what the requester may see, which the server
            checks on submit.
          </p>
          <ProblemList
            id="prop-src-problems"
            list={problems.filter((x) => ["value_source", "value_source/kind", "value_source/name"].includes(x.at))}
          />
          {(src?.kind !== "builtin" || src.name !== own) && (
            <Button size="sm" variant="secondary" onClick={() => setSource({ kind: "builtin", name: own })}>
              Take them from {own}
            </Button>
          )}
          <BuiltinParams source={own} param={param} />
        </>
      ) : (
        <>
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
              <BuiltinParams source={src.name} param={param} />
            </>
          )}
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
          <legend className="mb-1 text-secondary font-medium">Depends on</legend>
          <p className="text-caption text-faint">A change to these inputs fetches the values again.</p>
          {others.map((o) => (
            <label key={o} htmlFor={`prop-dep-${o}`} className="flex min-h-6 items-center gap-2 text-secondary">
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

/** The parameters of a built-in source, by source. */
function BuiltinParams({
  source,
  param,
}: {
  source?: string;
  param: (k: string, label: string, hint?: string) => React.ReactNode;
}) {
  switch (source) {
    case "rvav.projects":
    case "rvav.users":
      return null;
    case "rvav.deployments":
      return param("item", "Catalog item", "Only this item's deployments: its slug.");
    case "rvav.vms":
      return param("deployment", "Deployment", "Only this deployment's VMs: ${inputs.deployment}");
    default:
      return (
        <>
          {param("cluster", "Cluster", "For datastores: ${inputs.cluster}")}
          {param("network", "Network", "For subnets and addresses: ${inputs.network}")}
        </>
      );
  }
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
  ruleError,
  test,
}: {
  def: Def;
  name: string;
  onChange: (d: Def) => void;
  onSelect: (s: Selection) => void;
  problems: Problem[];
  /** A rule that failed in the preview. */
  ruleError?: string;
  test: SourceTest;
}) {
  const ui = def.ui_schema.fields[name];
  const prop = props(def)[name];
  const [nameError, setNameError] = useState("");
  if (!ui) return <p className="text-secondary text-muted">This input has no UI field: add it in the YAML.</p>;
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
        hint={"The input's key: inputs." + name + " in rules and the blueprint, which a rename doesn't change."}
        error={nameError || undefined}
      >
        <CommitInput
          id="prop-name"
          value={name}
          className={mono}
          onCommit={(to) => {
            if (!namePattern.test(to)) return setNameError("A lower-case letter, then letters, digits and _.");
            if (to in def.ui_schema.fields || to in props(def)) return setNameError(`"${to}" is taken.`);
            setNameError("");
            onChange(renameField(def, name, to));
            onSelect({ kind: "field", name: to });
            focusLater("prop-name");
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
        <label htmlFor="prop-required" className="flex items-center gap-2 text-body">
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
        <>
          <label htmlFor="prop-decimal" className="flex items-center gap-2 text-body">
            <Checkbox
              id="prop-decimal"
              checked={isDecimal(def, name)}
              onChange={(on) => onChange(setDecimal(def, name, on))}
            />
            Allow decimals
          </label>
          <div className="grid grid-cols-3 gap-2">
            {(
              [
                ["minimum", "Minimum"],
                ["maximum", "Maximum"],
                ["multipleOf", "Step"],
              ] as const
            ).map(([k, label]) => (
              <Field key={k} label={label} htmlFor={`prop-${k}`}>
                <Input
                  id={`prop-${k}`}
                  type="number"
                  step={isDecimal(def, name) ? "any" : 1}
                  min={k === "multipleOf" ? 0 : undefined}
                  value={typeof prop[k] === "number" ? prop[k] : ""}
                  onChange={(e) =>
                    onChange(updateProp(def, name, { [k]: e.target.value ? Number(e.target.value) : undefined }))
                  }
                />
              </Field>
            ))}
          </div>
        </>
      )}
      {(ui.widget === "date" || ui.widget === "datetime") && prop && (
        <DateBounds def={def} name={name} time={ui.widget === "datetime"} onChange={onChange} />
      )}
      {(ui.widget === "datagrid" || ui.widget === "tags") && prop && (
        <div className="grid grid-cols-2 gap-2">
          {(
            [
              ["minItems", ui.widget === "datagrid" ? "Fewest rows" : "Fewest tags"],
              ["maxItems", ui.widget === "datagrid" ? "Most rows" : "Most tags"],
            ] as const
          ).map(([k, label]) => (
            <Field key={k} label={label} htmlFor={`prop-${k}`}>
              <Input
                id={`prop-${k}`}
                type="number"
                min={0}
                step={1}
                value={typeof prop[k] === "number" ? prop[k] : ""}
                onChange={(e) =>
                  onChange(updateProp(def, name, { [k]: e.target.value ? Number(e.target.value) : undefined }))
                }
              />
            </Field>
          ))}
        </div>
      )}
      {ui.widget === "datagrid" && <ColumnsEditor def={def} name={name} onChange={onChange} problems={problems} />}
      {ui.widget === "tags" && !ui.value_source && (
        <Field
          label="Suggestions"
          htmlFor="prop-options"
          hint="One per line: value | label. Requesters can type others too."
        >
          <OptionsInput
            value={optionsText(ui.options)}
            onCommit={(t) => onChange(updateUi(def, name, { options: parseOptions(t) }))}
          />
        </Field>
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
            <p className="text-secondary text-faint">Set lists and objects in the YAML.</p>
          )}
          <ProblemList id="prop-initial-problems" list={at("initial")} />
        </Field>
      )}
      {ui.widget !== "info" && (
        <SourceEditor name={name} def={def} onChange={onChange} problems={problems} test={test} />
      )}
      <fieldset className="flex flex-col gap-2.5 border-t border-line pt-3">
        <legend className="sr-only">Rules</legend>
        <p className="text-secondary font-medium">
          Rules <span className="font-normal text-faint">(CEL over inputs, environment, project and user)</span>
        </p>
        {ruleError && (
          <p role="note" className="text-secondary text-warn">
            In the preview, a rule failed and its default was used: {ruleError}
          </p>
        )}
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
        <fieldset className="flex flex-col gap-1.5 border-t border-line pt-3 text-secondary">
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
            // The next field (or the one before) is selected, and focus goes to it in the list.
            const fields = section?.fields ?? [];
            const i = fields.indexOf(name);
            const neighbour = fields[i + 1] ?? fields[i - 1];
            onChange(removeField(def, name));
            onSelect(neighbour ? { kind: "field", name: neighbour } : null);
            focusLater(neighbour ? `designer-field-${neighbour}` : "designer-add-field");
          }}
        >
          Remove {ui.label || name}
        </Button>
      </div>
    </div>
  );
}

function OptionsInput({
  value,
  onCommit,
  ...described
}: {
  value: string;
  onCommit: (v: string) => void;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
}) {
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
      {...described}
    />
  );
}

const columnWidgets = [
  { value: "text", label: "Text" },
  { value: "number", label: "Number" },
  { value: "toggle", label: "Toggle" },
  { value: "select", label: "Dropdown" },
];

/** A table's columns: each one's key, label, widget, choices and whether every row needs it. */
function ColumnsEditor({
  def,
  name,
  onChange,
  problems,
}: {
  def: Def;
  name: string;
  onChange: (d: Def) => void;
  problems: Problem[];
}) {
  const columns = def.ui_schema.fields[name]?.columns ?? [];
  const [keyError, setKeyError] = useState<{ index: number; message: string }>();
  return (
    <fieldset className="flex flex-col gap-2 border-t border-line pt-3">
      <legend className="text-secondary font-medium">Columns</legend>
      <ProblemList id="prop-columns-problems" list={problems.filter((p) => p.at.startsWith("columns"))} />
      {columns.map((c, i) => {
        const at = `prop-column-${i}`;
        const error = keyError?.index === i ? keyError.message : undefined;
        return (
          <div key={i} className="flex flex-col gap-2 rounded-md border border-line p-2">
            <div className="grid grid-cols-2 gap-2">
              <Field label="Key" htmlFor={`${at}-key`} error={error}>
                <CommitInput
                  id={`${at}-key`}
                  value={c.key}
                  className={mono}
                  onCommit={(to) => {
                    if (to === c.key) return;
                    if (!namePattern.test(to))
                      return setKeyError({ index: i, message: "A lower-case letter, then letters, digits and _." });
                    if (columns.some((x) => x.key === to))
                      return setKeyError({ index: i, message: `"${to}" is taken.` });
                    setKeyError(undefined);
                    onChange(updateColumn(def, name, i, { key: to }));
                  }}
                />
              </Field>
              <Field label="Label" htmlFor={`${at}-label`}>
                <Input
                  id={`${at}-label`}
                  value={c.label}
                  onChange={(e) => onChange(updateColumn(def, name, i, { label: e.target.value }))}
                />
              </Field>
            </div>
            <div className="grid grid-cols-2 items-end gap-2">
              <Field label="Widget" htmlFor={`${at}-widget`}>
                <Select
                  id={`${at}-widget`}
                  value={c.widget}
                  onChange={(w) => onChange(updateColumn(def, name, i, { widget: w as UiColumn["widget"] }))}
                  options={columnWidgets}
                />
              </Field>
              <label htmlFor={`${at}-required`} className="flex h-8 items-center gap-2 text-body">
                <Checkbox
                  id={`${at}-required`}
                  checked={isColumnRequired(def, name, c.key)}
                  onChange={(on) => onChange(updateColumn(def, name, i, { required: on }))}
                />
                Required
              </label>
            </div>
            {c.widget === "select" && (
              <Field label="Choices" htmlFor={`${at}-options`} hint="One per line: value | label.">
                <ColumnOptions
                  id={`${at}-options`}
                  value={optionsText(c.options)}
                  onCommit={(t) => onChange(updateColumn(def, name, i, { options: parseOptions(t) }))}
                />
              </Field>
            )}
            <div>
              <Button size="sm" variant="quiet" onClick={() => onChange(removeColumn(def, name, i))}>
                Remove column {c.label || c.key}
              </Button>
            </div>
          </div>
        );
      })}
      <div>
        <Button
          size="sm"
          onClick={() => {
            onChange(addColumn(def, name));
            focusLater(`prop-column-${columns.length}-key`);
          }}
        >
          Add a column
        </Button>
      </div>
    </fieldset>
  );
}

function ColumnOptions({ id, value, onCommit }: { id: string; value: string; onCommit: (v: string) => void }) {
  const [draft, setDraft] = useState(value);
  const [shown, setShown] = useState(value);
  if (shown !== value) {
    setShown(value);
    setDraft(value);
  }
  return (
    <Textarea
      id={id}
      className={cn("min-h-16", mono)}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => draft !== value && onCommit(draft)}
    />
  );
}

/** The earliest and latest a date or date-time input takes: `formatMinimum` and `formatMaximum`. */
function DateBounds({
  def,
  name,
  time,
  onChange,
}: {
  def: Def;
  name: string;
  time: boolean;
  onChange: (d: Def) => void;
}) {
  const prop = props(def)[name] ?? {};
  return (
    <div className="grid grid-cols-2 gap-2">
      {(
        [
          ["formatMinimum", "Earliest"],
          ["formatMaximum", "Latest"],
        ] as const
      ).map(([k, label]) => {
        const v = typeof prop[k] === "string" ? prop[k] : "";
        return (
          <Field key={k} label={label} htmlFor={`prop-${k}`} hint={time ? zoneLabel() : undefined}>
            <Input
              id={`prop-${k}`}
              type={time ? "datetime-local" : "date"}
              value={time ? localInput(v) : v}
              onChange={(e) => {
                const next = e.target.value ? (time ? withOffset(e.target.value) : e.target.value) : undefined;
                onChange(updateProp(def, name, { [k]: next }));
              }}
            />
          </Field>
        );
      })}
    </div>
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
  const [keyError, setKeyError] = useState("");
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
      <Field label="Key" htmlFor="section-key" error={keyError || undefined}>
        <CommitInput
          id="section-key"
          className={mono}
          value={s.key}
          onCommit={(k) => {
            if (!k.trim()) return setKeyError("Give the section a key.");
            if (def.ui_schema.layout.sections.some((x) => x.key === k))
              return setKeyError(`Another section has the key "${k}".`);
            setKeyError("");
            onChange(updateSection(def, s.key, { key: k }));
            onSelect({ kind: "section", key: k });
            focusLater("section-key");
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
            focusLater("designer-add-section");
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
  states,
  test,
}: {
  def: Def;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onChange: (d: Def) => void;
  problems: Problems;
  /** The preview's rule results. */
  states?: FieldStates;
  test: SourceTest;
}) {
  const sectionIndex =
    selection?.kind === "section" ? def.ui_schema.layout.sections.findIndex((s) => s.key === selection.key) : -1;
  return (
    <Panel title={selection?.kind === "section" ? "Section" : "Properties"}>
      {!selection && <p className="text-secondary text-muted">Choose a field or a section to change it.</p>}
      {selection?.kind === "field" && (
        <FieldProperties
          key={selection.name}
          def={def}
          name={selection.name}
          onChange={onChange}
          onSelect={onSelect}
          problems={problems.fields[selection.name] ?? []}
          ruleError={states?.[selection.name]?.error}
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
