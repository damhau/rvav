import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { LoaderCircle, Upload } from "lucide-react";
import { useId, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Panel } from "@/components/ui/panel";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { singleSource } from "@/forms/defaults";
import { uploadImage } from "@/forms/image";
import { isApiError } from "@/lib/api/client";
import type {
  FieldStates,
  FieldValues,
  FormLibraryEntry,
  FormValidation,
  UiColumn,
  UiWidget,
  ValueSource,
} from "@/lib/api/types";
import { i18n } from "@/lib/i18n";
import { q } from "@/lib/query";
import { cn, localInput, toText, withOffset, zoneLabel } from "@/lib/utils";
import { focusLater, type Selection } from "./FieldList";
import {
  addColumn,
  builtinSources,
  choiceWidgets,
  columnWidgets,
  isColumnRequired,
  isDecimal,
  isRequired,
  layoutOf,
  moveToSection,
  namePattern,
  newValidationKey,
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
  setValidations,
  setWidget,
  shownWidgets,
  updateColumn,
  updateProp,
  updateSection,
  updateUi,
  widgetChoices,
  widgetLabel,
  type Def,
  type FormScope,
  type Problem,
  type Problems,
} from "./model";
const mono = "font-mono text-secondary";
const tr = i18n.getFixedT(null, "admin-catalog");

/** Code inside a sentence: Trans leaves its text alone. */
const Mono = ({ text, className = mono }: { text: string; className?: string }) => (
  <span className={className}>{text}</span>
);
const inputsExample = "inputs.environment";

/** The first options a source gave, as its test shows them. */
const firstLabels = (options: { label: string }[]) =>
  options
    .slice(0, 5)
    .map((o) => o.label)
    .join(", ") + (options.length > 5 ? ", …" : "");

const paramId = (id: string, name: string) => `${id}-prm-${name}`;
const paramAt = (name: string) => `params/${name}`;
const ruleAt = (key: string) => `rules/${key}`;

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

function optionsText(options: { value: string; label: string; parent?: string }[] | undefined) {
  return (options ?? [])
    .map((o) =>
      o.parent !== undefined
        ? `${o.value} | ${o.label} | ${o.parent}`
        : o.label && o.label !== o.value
          ? `${o.value} | ${o.label}`
          : o.value,
    )
    .join("\n");
}

function parseOptions(text: string) {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean)
    .map((l) => {
      const [value = "", label, parent] = l.split("|").map((x) => x.trim());
      return { value, label: label || value, ...(parent ? { parent } : {}) };
    });
}

export interface SourceTest {
  run: (field: string) => Promise<FieldValues>;
  /** The inputs the preview has, which the test sends. */
  values: Record<string, unknown>;
  saved: boolean;
}

function TestSource({ name, dependsOn, test }: { name: string; dependsOn: string[]; test: SourceTest }) {
  const { t } = useTranslation("admin-catalog");
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
        title={test.saved ? undefined : t("designer.props.waitSaved")}
        aria-describedby={result ? id : undefined}
      >
        {running ? t("designer.props.testing") : t("designer.props.test")}
      </Button>
      {result && (
        <div id={id} role="status" className="rounded-md border border-line bg-sunken p-2 text-secondary">
          {result.with && <p className="mb-1 text-faint">{t("designer.props.with", { values: result.with })}</p>}
          {result.error && <p className="text-bad">{result.error}</p>}
          {result.ok && (
            <>
              {result.ok.value !== undefined && (
                <p>
                  <Trans
                    t={t}
                    i18nKey="designer.props.value"
                    components={{ value: <Mono text={JSON.stringify(result.ok.value)} /> }}
                  />
                </p>
              )}
              {result.ok.options && (
                <p>
                  {result.ok.options.length > 0
                    ? t("designer.props.optionsWith", {
                        count: result.ok.options.length,
                        labels: firstLabels(result.ok.options),
                      })
                    : t("designer.props.optionsFound", { count: 0 })}
                </p>
              )}
            </>
          )}
        </div>
      )}
    </div>
  );
}

type LibraryUse = "source" | "validation";

/** What a form names of the form library: an entry, its version, and an expression per parameter. */
interface LibraryBinding {
  name?: string;
  version?: number;
  params?: Record<string, unknown>;
}

/** An entry's parameters bound to the form's inputs of the same name. */
function boundByName(entry: FormLibraryEntry | undefined, inputs: string[], kept: Record<string, unknown> = {}) {
  const out: Record<string, unknown> = {};
  for (const p of entry?.parameters ?? []) {
    if (typeof kept[p.name] === "string") out[p.name] = kept[p.name];
    else if (inputs.includes(p.name)) out[p.name] = `inputs.${p.name}`;
  }
  return out;
}

/** The first entry of the library for the use, bound to the form's inputs. */
function firstEntry(entries: FormLibraryEntry[] | undefined, use: LibraryUse, inputs: string[]): LibraryBinding {
  const e = entries?.find((x) => x.use === use);
  return e ? { name: e.key, version: e.version, params: boundByName(e, inputs) } : {};
}

/** The entry of the form library a field's source or a validation names, and its parameters' expressions. */
function LibraryRef({
  id,
  use,
  value,
  inputs,
  onChange,
  problems,
}: {
  id: string;
  use: LibraryUse;
  value: LibraryBinding;
  /** The form's inputs, which a parameter of the same name is bound to. */
  inputs: string[];
  onChange: (b: LibraryBinding) => void;
  /** The problems at a place in the reference: `name`, `version`, `params/x`. */
  problems: (at: string) => Problem[];
}) {
  const { t } = useTranslation("admin-catalog");
  const catalogue = useQuery(q.formLibrary());
  const entries = (catalogue.data?.items ?? []).filter((e) => e.use === use);
  const latest = entries.find((e) => e.key === value.name);
  const older = Boolean(latest && value.version && value.version !== latest.version);
  const pinned = useQuery({ ...q.formLibraryEntry(value.name ?? "", value.version), enabled: older });
  const entry = older ? pinned.data : latest;
  const params = value.params ?? {};
  const known = new Set(entry?.parameters.map((p) => p.name));
  const named =
    value.name && !latest ? [{ value: value.name, label: value.name }] : ([] as { value: string; label: string }[]);
  const setParam = (k: string, v: string) => {
    const next = { ...params };
    if (v) next[k] = v;
    else delete next[k];
    onChange({ ...value, params: next });
  };
  if (catalogue.isSuccess && entries.length === 0 && !value.name)
    return (
      <p className="text-secondary text-muted">
        <Trans
          t={t}
          i18nKey={use === "source" ? "designer.props.noLibrarySource" : "designer.props.noLibraryValidation"}
          components={{
            link: <Link to="/admin/form-library" className="text-accent underline-offset-2 hover:underline" />,
          }}
        />
      </p>
    );
  return (
    <>
      <Field label={t("designer.props.entry")} htmlFor={`${id}-name`}>
        <Select
          id={`${id}-name`}
          value={value.name ?? ""}
          loading={catalogue.isPending}
          placeholder={t(use === "source" ? "designer.props.chooseSource" : "designer.props.chooseValidation")}
          onChange={(key) => {
            const e = entries.find((x) => x.key === key);
            onChange({ name: key, version: e?.version, params: boundByName(e, inputs, params) });
          }}
          options={[...named, ...entries.map((e) => ({ value: e.key, label: `${e.name} (${e.key})` }))]}
          aria-invalid={problems("name").length > 0 || undefined}
          aria-describedby={problems("name").length ? `${id}-name-problems` : undefined}
        />
      </Field>
      <ProblemList id={`${id}-name-problems`} list={problems("name")} />
      {latest && (
        <Field
          label={t("designer.props.version")}
          htmlFor={`${id}-version`}
          hint={older ? t("designer.props.olderHint", { version: latest.version }) : undefined}
        >
          <Select
            id={`${id}-version`}
            value={String(value.version ?? latest.version)}
            onChange={(v) => onChange({ ...value, version: Number(v) })}
            options={[...latest.versions].reverse().map((v) => ({
              value: String(v),
              label: v === latest.version ? t("designer.props.versionLatest", { version: v }) : String(v),
            }))}
          />
        </Field>
      )}
      <ProblemList id={`${id}-version-problems`} list={problems("version")} />
      {entry?.description && <p className="text-secondary text-muted">{entry.description}</p>}
      {entry && entry.parameters.length > 0 && (
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-1 text-secondary font-medium">{t("designer.props.parameters")}</legend>
          <p className="text-caption text-faint">
            <Trans
              t={t}
              i18nKey="designer.props.parametersLead"
              components={{ expr: <Mono text={inputsExample} className="font-mono" /> }}
            />
          </p>
          {entry.parameters.map((p) => {
            const pid = paramId(id, p.name);
            const found = problems(paramAt(p.name));
            return (
              <Field
                key={p.name}
                label={p.required ? p.label || p.name : t("designer.props.optional", { label: p.label || p.name })}
                htmlFor={pid}
                hint={p.description}
              >
                <CommitInput
                  id={pid}
                  value={toText(params[p.name] ?? "")}
                  onCommit={(v) => setParam(p.name, v)}
                  className={mono}
                  invalid={found.length > 0}
                  describedBy={found.length ? `${pid}-problems` : undefined}
                />
                <ProblemList id={`${pid}-problems`} list={found} />
              </Field>
            );
          })}
        </fieldset>
      )}
      <ProblemList
        id={`${id}-params-problems`}
        list={Object.keys(params)
          .filter((k) => !known.has(k))
          .flatMap((k) => problems(`params/${k}`))
          .concat(problems("params").filter((p) => p.at.endsWith("params")))}
      />
    </>
  );
}

/** The parameters of a value source the designer sets. */
type SourceParam =
  | "url"
  | "body"
  | "secret"
  | "items"
  | "value"
  | "label"
  | "meta"
  | "parent"
  | "options"
  | "item"
  | "deployment"
  | "cluster"
  | "network";

/** The parameters of a validation the designer sets. */
type CheckParam = "check" | "url" | "body" | "secret" | "valid" | "message" | "errors";

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
  const { t } = useTranslation("admin-catalog");
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
  const param = (k: SourceParam, label: string, hint?: string, code = true) => {
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
  const library = useQuery(q.formLibrary());
  // A field that takes one value gets it from the source: a default, or a computed field's value.
  const one = singleSource({ ...ui, value_source: { kind: "cel" } });
  const gives = ui.widget === "computed" ? "value" : "default";
  return (
    <fieldset className="flex flex-col gap-2.5 border-t border-line pt-3">
      <legend className="sr-only">{t("designer.props.valueSource")}</legend>
      {own ? (
        <>
          <p className="text-secondary text-muted">
            <Trans t={t} i18nKey="designer.props.ownLead" components={{ source: <Mono text={own} /> }} />
          </p>
          <ProblemList
            id="prop-src-problems"
            list={problems.filter((x) => ["value_source", "value_source/kind", "value_source/name"].includes(x.at))}
          />
          {(src?.kind !== "builtin" || src.name !== own) && (
            <Button size="sm" variant="secondary" onClick={() => setSource({ kind: "builtin", name: own })}>
              {t("designer.props.takeFrom", { source: own })}
            </Button>
          )}
          <BuiltinParams source={own} param={param} />
        </>
      ) : (
        <>
          <Field label={t("designer.props.valueSource")} htmlFor="prop-src-kind">
            <Select
              id="prop-src-kind"
              value={src?.kind ?? "none"}
              onChange={(k) =>
                setSource(
                  k === "none"
                    ? undefined
                    : k === "builtin"
                      ? { kind: "builtin", name: builtinSources[0] }
                      : k === "library"
                        ? { kind: "library", ...firstEntry(library.data?.items, "source", others) }
                        : k === "rest"
                          ? {
                              kind: "rest",
                              params: one
                                ? { url: "https://", value: "$.id" }
                                : { url: "https://", items: "$[*]", value: "$.id", label: "$.name" },
                            }
                          : { kind: "cel", params: one ? { value: '""' } : { options: '["a", "b"]' } },
                )
              }
              options={sourceKinds.map((k) => ({ value: k, label: t(`designer.props.kind.${k}`) }))}
            />
          </Field>
          <ProblemList
            id="prop-src-problems"
            list={problems
              .filter((p) => p.at === "value_source")
              .concat(
                at("kind"),
                at("params").filter((p) => p.at === "value_source/params"),
              )}
          />
          {src?.kind === "builtin" && (
            <>
              <Field label={t("designer.props.source")} htmlFor="prop-src-name">
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
      {src?.kind === "library" && (
        <LibraryRef
          id="prop-src-lib"
          use="source"
          value={src}
          inputs={others}
          onChange={(b) => setSource({ ...src, ...b })}
          problems={at}
        />
      )}
      {one && (src?.kind === "rest" || src?.kind === "cel" || src?.kind === "library") && (
        <p className="text-secondary text-muted">
          {gives === "value" ? t("designer.props.givesValue") : t("designer.props.givesDefault")}
        </p>
      )}
      {src?.kind === "rest" && (
        <>
          {param("url", t("designer.props.url"), t("designer.props.urlHint"))}
          <Field label={t("designer.props.method")} htmlFor="prop-src-method">
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
          {params.method === "POST" && param("body", t("designer.props.body"))}
          {param("secret", t("designer.props.secret"), t("designer.props.secretHint"), false)}
          {one ? (
            <>
              {param("items", t("designer.props.item"), t("designer.props.itemHint"))}
              {param(
                "value",
                t("designer.props.valueInItem"),
                gives === "value"
                  ? t("designer.props.valueInItemHintValue")
                  : t("designer.props.valueInItemHintDefault"),
              )}
            </>
          ) : (
            <>
              {param("items", t("designer.props.items"))}
              {param("value", t("designer.props.valueIn"))}
              {param("label", t("designer.props.labelIn"))}
              {param("meta", t("designer.props.metaIn"))}
            </>
          )}
          {ui.widget === "tree" && param("parent", t("designer.props.parentIn"), t("designer.props.parentHint"))}
        </>
      )}
      {src?.kind === "cel" &&
        one &&
        param(
          "value",
          t("designer.props.celValue"),
          gives === "value" ? t("designer.props.celValueHintValue") : t("designer.props.celValueHintDefault"),
        )}
      {src?.kind === "cel" && !one && (
        <>
          {param(
            "options",
            t("designer.props.options"),
            ui.widget === "tree" ? t("designer.props.optionsTreeCel") : t("designer.props.optionsCel"),
          )}
          {param("value", t("designer.props.celValue"), t("designer.props.valueInstead"))}
        </>
      )}
      {ui.widget === "search" && (src?.kind === "rest" || src?.kind === "cel") && (
        <div className="flex flex-col gap-1">
          <label htmlFor="prop-src-search" className="flex min-h-6 items-center gap-2 text-secondary">
            <Checkbox
              id="prop-src-search"
              checked={src.search === true}
              onChange={(on) => {
                const { search: _, ...rest } = src;
                setSource(on ? { ...rest, search: true } : rest);
              }}
              aria-describedby="prop-src-search-hint"
            />
            {t("designer.props.searches")}
          </label>
          <p id="prop-src-search-hint" className="text-caption text-faint">
            {src.kind === "rest" ? t("designer.props.searchRest") : t("designer.props.searchCel")}
          </p>
          <ProblemList id="prop-src-search-problems" list={at("search")} />
        </div>
      )}
      {src && others.length > 0 && (
        <fieldset className="flex flex-col gap-1">
          <legend className="mb-1 text-secondary font-medium">{t("designer.props.dependsOn")}</legend>
          <p className="text-caption text-faint">{t("designer.props.dependsLead")}</p>
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

const sourceKinds = ["none", "builtin", "rest", "cel", "library"] as const;
const checkKinds = ["cel", "rest", "library"] as const;

function ValidationEditor({
  index,
  validation: v,
  inputs,
  onChange,
  onRemove,
  problems,
}: {
  index: number;
  validation: FormValidation;
  inputs: string[];
  onChange: (v: FormValidation) => void;
  onRemove: () => void;
  problems: Problem[];
}) {
  const { t } = useTranslation("admin-catalog");
  const id = `val-${index}`;
  const entries = useQuery(q.formLibrary());
  const params = (v.params ?? {}) as Record<string, unknown>;
  const at = (p: string) => problems.filter((x) => x.at === p || x.at.startsWith(`${p}/`));
  const own = (k: "key" | "title" | "message", label: string, hint?: string, code = false) => {
    const fid = `${id}-${k}`;
    const found = at(k);
    return (
      <Field label={label} htmlFor={fid} hint={hint}>
        <CommitInput
          id={fid}
          value={v[k] ?? ""}
          onCommit={(text) => {
            const next = { ...v, [k]: text };
            if (!text && k !== "key") delete next[k];
            onChange(next);
          }}
          className={code ? mono : undefined}
          invalid={found.length > 0}
          describedBy={found.length ? `${fid}-problems` : undefined}
        />
        <ProblemList id={`${fid}-problems`} list={found} />
      </Field>
    );
  };
  const setParam = (k: string, text: string) => {
    const next = { ...params };
    if (text) next[k] = text;
    else delete next[k];
    onChange({ ...v, params: next });
  };
  const param = (k: CheckParam, label: string, hint?: string, code = true) => {
    const pid = `${id}-params-${k}`;
    const found = at(`params/${k}`);
    return (
      <Field label={label} htmlFor={pid} hint={hint}>
        <CommitInput
          id={pid}
          value={typeof params[k] === "string" ? params[k] : ""}
          onCommit={(text) => setParam(k, text)}
          className={code ? mono : undefined}
          invalid={found.length > 0}
          describedBy={found.length ? `${pid}-problems` : undefined}
        />
        <ProblemList id={`${pid}-problems`} list={found} />
      </Field>
    );
  };
  const setKind = (kind: string) => {
    const { name: _n, version: _v, params: _p, ...kept } = v;
    if (kind === "library")
      onChange({ ...kept, kind: "library", ...firstEntry(entries.data?.items, "validation", inputs) });
    else
      onChange({
        ...kept,
        kind: kind as FormValidation["kind"],
        params: kind === "rest" ? { url: "https://" } : { check: "true" },
      });
  };
  const on = v.fields ?? [];
  const title = v.title || v.key || t("designer.props.validationN", { number: index + 1 });
  return (
    <fieldset className="flex flex-col gap-2.5 rounded-md border border-line p-3">
      <legend className="px-1 text-secondary font-semibold">{title}</legend>
      <ProblemList id={`${id}-problems`} list={problems.filter((p) => p.at === "")} />
      {own("key", t("designer.props.key"), t("designer.props.keyHint"), true)}
      {own("title", t("designer.props.title"), t("designer.props.titleHint"))}
      <Field label={t("designer.props.kindLabel")} htmlFor={`${id}-kind`}>
        <Select
          id={`${id}-kind`}
          value={v.kind}
          onChange={setKind}
          options={checkKinds.map((k) => ({ value: k, label: t(`designer.props.kind.${k}`) }))}
        />
      </Field>
      <ProblemList id={`${id}-kind-problems`} list={at("kind").concat(at("name").filter(() => v.kind !== "library"))} />
      {v.kind === "cel" && param("check", t("designer.props.check"), t("designer.props.checkHint"))}
      {v.kind === "rest" && (
        <>
          {param("url", t("designer.props.url"), t("designer.props.restUrlHint"))}
          <Field label={t("designer.props.method")} htmlFor={`${id}-params-method`}>
            <Select
              id={`${id}-params-method`}
              value={typeof params.method === "string" ? params.method : "GET"}
              onChange={(m) => setParam("method", m === "GET" ? "" : m)}
              options={[
                { value: "GET", label: "GET" },
                { value: "POST", label: "POST" },
              ]}
            />
          </Field>
          {params.method === "POST" && param("body", t("designer.props.body"))}
          {param("secret", t("designer.props.secret"), t("designer.props.secretHint"), false)}
          {param("valid", t("designer.props.valid"), t("designer.props.validHint"))}
          {param("message", t("designer.props.message"), t("designer.props.messageHint"))}
          {param("errors", t("designer.props.errors"), t("designer.props.errorsHint"))}
          <p className="text-secondary text-muted">{t("designer.props.non2xx")}</p>
        </>
      )}
      {v.kind === "library" && (
        <LibraryRef
          id={`${id}-lib`}
          use="validation"
          value={v}
          inputs={inputs}
          onChange={(b) => onChange({ ...v, ...b })}
          problems={at}
        />
      )}
      {own("message", t("designer.props.refusal"), t("designer.props.refusalHint"))}
      {inputs.length > 0 && (
        <fieldset className="flex flex-col gap-1">
          <legend className="mb-1 text-secondary font-medium">{t("designer.props.showsOn")}</legend>
          <p className="text-caption text-faint">{t("designer.props.showsOnLead")}</p>
          {inputs.map((f) => (
            <label key={f} htmlFor={`${id}-on-${f}`} className="flex min-h-6 items-center gap-2 text-secondary">
              <Checkbox
                id={`${id}-on-${f}`}
                checked={on.includes(f)}
                onChange={(yes) => {
                  const next = yes ? [...on, f] : on.filter((x) => x !== f);
                  const { fields: _, ...rest } = v;
                  onChange(next.length ? { ...rest, fields: next } : rest);
                }}
              />
              <span className="font-mono">{f}</span>
            </label>
          ))}
          <ProblemList id={`${id}-fields-problems`} list={at("fields")} />
        </fieldset>
      )}
      <Button id={`${id}-remove`} size="sm" variant="quiet" className="self-start" onClick={onRemove}>
        {t("designer.props.remove", { title })}
      </Button>
    </fieldset>
  );
}

/** The form's validations (ADR-0043): checks of the whole form, on submit and on a wizard's review step. */
function ValidationsProperties({
  def,
  onChange,
  problems,
}: {
  def: Def;
  onChange: (d: Def) => void;
  problems: Record<number, Problem[]>;
}) {
  const { t } = useTranslation("admin-catalog");
  const list = def.ui_schema.validations ?? [];
  const inputs = Object.keys(props(def));
  const entries = useQuery(q.formLibrary());
  const put = (next: FormValidation[]) => onChange(setValidations(def, next));
  return (
    <div className="flex flex-col gap-3">
      <p className="text-secondary text-muted">{t("designer.props.validationsLead")}</p>
      {list.length === 0 && <p className="text-secondary text-faint">{t("designer.props.noValidation")}</p>}
      {list.map((v, i) => (
        <ValidationEditor
          key={i}
          index={i}
          validation={v}
          inputs={inputs}
          problems={problems[i] ?? []}
          onChange={(x) => put(list.map((y, j) => (j === i ? x : y)))}
          onRemove={() => {
            put(list.filter((_, j) => j !== i));
            focusLater(`val-${i}-remove`, `val-${i - 1}-remove`, "designer-add-validation");
          }}
        />
      ))}
      <div className="flex flex-wrap gap-2">
        <Button
          id="designer-add-validation"
          size="sm"
          onClick={() => {
            put([...list, { key: newValidationKey(def), kind: "cel", params: { check: "true" } }]);
            focusLater(`val-${list.length}-key`);
          }}
        >
          {t("designer.props.addValidation")}
        </Button>
        {entries.data?.items.some((e) => e.use === "validation") && (
          <Button
            size="sm"
            variant="secondary"
            onClick={() => {
              put([
                ...list,
                {
                  key: newValidationKey(def),
                  kind: "library",
                  ...firstEntry(entries.data.items, "validation", inputs),
                },
              ]);
              focusLater(`val-${list.length}-lib-name`);
            }}
          >
            {t("designer.props.addFromLibrary")}
          </Button>
        )}
      </div>
    </div>
  );
}

/** The parameters of a built-in source, by source. */
function BuiltinParams({
  source,
  param,
}: {
  source?: string;
  param: (k: SourceParam, label: string, hint?: string) => React.ReactNode;
}) {
  const { t } = useTranslation("admin-catalog");
  switch (source) {
    case "rvav.projects":
    case "rvav.users":
      return null;
    case "rvav.deployments":
      return param("item", t("designer.props.catalogItem"), t("designer.props.catalogItemHint"));
    case "rvav.vms":
      return param("deployment", t("designer.props.deployment"), t("designer.props.deploymentHint"));
    default:
      return (
        <>
          {param("cluster", t("designer.props.cluster"), t("designer.props.clusterHint"))}
          {param("network", t("designer.props.network"), t("designer.props.networkHint"))}
        </>
      );
  }
}

const rules = () =>
  [
    { key: "visible", label: tr("designer.props.rule.visible"), hint: tr("designer.props.ruleHint.visible") },
    { key: "required", label: tr("designer.props.rule.required"), hint: tr("designer.props.ruleHint.required") },
    { key: "read_only", label: tr("designer.props.rule.read_only"), hint: "" },
    { key: "default", label: tr("designer.props.rule.default"), hint: tr("designer.props.ruleHint.default") },
    { key: "value", label: tr("designer.props.rule.value"), hint: tr("designer.props.ruleHint.value") },
  ] as const;

/** What the preview's error for a field says failed, and what the field was left with. */
function failedRule(error: string, gives: "value" | "default"): string {
  if (error.startsWith("default: ")) return tr("designer.props.failedDefault", { error: error.slice(9) });
  if (error.startsWith("value source: "))
    return tr(gives === "value" ? "designer.props.failedSourceValue" : "designer.props.failedSourceDefault", {
      error: error.slice(14),
    });
  return tr("designer.props.failedRule", { error });
}

function FieldProperties({
  def,
  name,
  onChange,
  onSelect,
  problems,
  ruleError,
  test,
  scope,
}: {
  def: Def;
  name: string;
  onChange: (d: Def) => void;
  onSelect: (s: Selection) => void;
  problems: Problem[];
  /** A rule that failed in the preview. */
  ruleError?: string;
  test: SourceTest;
  scope: FormScope;
}) {
  const { t } = useTranslation("admin-catalog");
  const ui = def.ui_schema.fields[name];
  const prop = props(def)[name];
  const [nameError, setNameError] = useState("");
  if (!ui) return <p className="text-secondary text-muted">{t("designer.props.noUi")}</p>;
  const at = (p: string) => problems.filter((x) => x.at === p);
  const section = def.ui_schema.layout.sections.find((s) => s.fields.includes(name));
  const textual = ["text", "textarea", "secret"].includes(ui.widget);
  const placeholder = textual || ui.widget === "search";
  const numeric = prop?.type === "integer" || prop?.type === "number";
  const initial = def.initial_values[name];
  const general = problems.filter((p) => p.at === "" || p.at.startsWith("schema"));
  const counted = isCounted(ui.widget) ? ui.widget : undefined;
  return (
    <div className="flex flex-col gap-3">
      <ProblemList id="prop-problems" list={general} />
      <Field
        label={t("designer.props.name")}
        htmlFor="prop-name"
        hint={t(scope === "action" ? "designer.props.nameHintAction" : "designer.props.nameHintItem", { name })}
        error={nameError || undefined}
      >
        <CommitInput
          id="prop-name"
          value={name}
          className={mono}
          onCommit={(to) => {
            if (!namePattern.test(to)) return setNameError(t("designer.props.namePattern"));
            if (to in def.ui_schema.fields || to in props(def))
              return setNameError(t("designer.props.taken", { name: to }));
            setNameError("");
            onChange(renameField(def, name, to));
            onSelect({ kind: "field", name: to });
            focusLater("prop-name");
          }}
        />
      </Field>
      <Field label={t("designer.props.label")} htmlFor="prop-label">
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
        <Field label={t("designer.props.widget")} htmlFor="prop-widget">
          <Select
            id="prop-widget"
            value={ui.widget}
            onChange={(w) => onChange(setWidget(def, name, w as UiWidget))}
            options={widgetChoices(scope)}
          />
        </Field>
        <Field label={t(`designer.part.${layoutOf(def)}.one`)} htmlFor="prop-section">
          <Select
            id="prop-section"
            value={section?.key ?? ""}
            placeholder={t(`designer.part.${layoutOf(def)}.none`)}
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
          {t("designer.props.required")}
        </label>
      )}
      {(ui.widget === "image" || ui.widget === "link") && (
        <Field
          label={t("designer.props.address")}
          htmlFor="prop-url"
          hint={ui.widget === "image" ? t("designer.props.addressImageHint") : t("designer.props.addressLinkHint")}
        >
          <CommitInput
            id="prop-url"
            className={mono}
            value={ui.url ?? ""}
            invalid={at("url").length > 0}
            describedBy={at("url").length ? "prop-url-problems" : undefined}
            onCommit={(v) => onChange(updateUi(def, name, { url: v.trim() || undefined }))}
          />
          <ProblemList id="prop-url-problems" list={at("url")} />
        </Field>
      )}
      {ui.widget === "image" && <ImageUpload key={name} onUploaded={(url) => onChange(updateUi(def, name, { url }))} />}
      {ui.widget === "image" && (
        <Field label={t("designer.props.alt")} htmlFor="prop-alt" hint={t("designer.props.altHint")}>
          <Input
            id="prop-alt"
            value={ui.alt ?? ""}
            onChange={(e) => onChange(updateUi(def, name, { alt: e.target.value || undefined }))}
            aria-invalid={at("alt").length > 0 || undefined}
            aria-describedby={at("alt").length ? "prop-alt-problems" : undefined}
          />
          <ProblemList id="prop-alt-problems" list={at("alt")} />
        </Field>
      )}
      {ui.widget === "file" && <FileLimits def={def} name={name} onChange={onChange} problems={problems} />}
      <Field label={helpLabel(ui.widget)} htmlFor="prop-help">
        <Textarea
          id="prop-help"
          value={ui.help ?? ""}
          className="min-h-14"
          onChange={(e) => onChange(updateUi(def, name, { help: e.target.value }))}
        />
      </Field>
      {placeholder && (
        <Field label={t("designer.props.placeholder")} htmlFor="prop-placeholder">
          <Input
            id="prop-placeholder"
            value={ui.placeholder ?? ""}
            onChange={(e) => onChange(updateUi(def, name, { placeholder: e.target.value }))}
          />
        </Field>
      )}
      {textual && prop && (
        <div className="grid grid-cols-2 gap-2">
          <Field label={t("designer.props.maxLength")} htmlFor="prop-maxlength">
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
          <Field label={t("designer.props.pattern")} htmlFor="prop-pattern">
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
            {t("designer.props.decimals")}
          </label>
          <div className="grid grid-cols-3 gap-2">
            {numberBounds.map((k) => (
              <Field key={k} label={t(`designer.props.bound.${k}`)} htmlFor={`prop-${k}`}>
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
      {counted && prop && (
        <div className="grid grid-cols-2 gap-2">
          {itemBounds.map((k) => (
            <Field key={k} label={t(`designer.props.${k}.${counted}`)} htmlFor={`prop-${k}`}>
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
      {columnWidgets.includes(ui.widget) && (
        <ColumnsEditor def={def} name={name} onChange={onChange} problems={problems} />
      )}
      {ui.widget === "tags" && !ui.value_source && (
        <Field
          label={t("designer.props.suggestions")}
          htmlFor="prop-options"
          hint={t("designer.props.suggestionsHint")}
        >
          <OptionsInput
            value={optionsText(ui.options)}
            onCommit={(t) => onChange(updateUi(def, name, { options: parseOptions(t) }))}
          />
        </Field>
      )}
      {choiceWidgets.includes(ui.widget) && !ui.value_source && (
        <Field
          label={t("designer.props.options")}
          htmlFor="prop-options"
          hint={ui.widget === "tree" ? t("designer.props.optionsTreeHint") : t("designer.props.optionsHint")}
        >
          <OptionsInput
            value={optionsText(ui.options)}
            onCommit={(t) => onChange(updateUi(def, name, { options: parseOptions(t) }))}
          />
        </Field>
      )}
      {prop && ui.widget !== "computed" && ui.widget !== "file" && (
        <Field label={t("designer.props.initial")} htmlFor="prop-initial">
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
            <p className="text-secondary text-faint">{t("designer.props.initialYaml")}</p>
          )}
          <ProblemList id="prop-initial-problems" list={at("initial")} />
        </Field>
      )}
      {!shownWidgets.includes(ui.widget) && ui.widget !== "file" && (
        <SourceEditor name={name} def={def} onChange={onChange} problems={problems} test={test} />
      )}
      <fieldset className="flex flex-col gap-2.5 border-t border-line pt-3">
        <legend className="sr-only">{t("designer.props.rules")}</legend>
        <p className="text-secondary font-medium">
          <Trans
            t={t}
            i18nKey="designer.props.rulesTitle"
            components={{ muted: <span className="font-normal text-faint" /> }}
          />
        </p>
        {ruleError && (
          <p role="note" className="text-secondary text-warn">
            {failedRule(ruleError, ui.widget === "computed" ? "value" : "default")}
          </p>
        )}
        {rules()
          .filter(
            (r) =>
              r.key !== "default" ||
              !(ui.widget === "computed" || ui.widget === "file" || shownWidgets.includes(ui.widget)),
          )
          .map((r) => {
            const id = `prop-rule-${r.key}`;
            const list = at(ruleAt(r.key));
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
      {prop && scope === "item" && (
        <fieldset className="flex flex-col gap-1.5 border-t border-line pt-3 text-secondary">
          <legend className="sr-only">{t("designer.props.handling")}</legend>
          {handlingFlags
            .filter((k) => ui.widget !== "file" || k === "expose_to_inventory")
            .map((k) => (
              <label key={k} htmlFor={`prop-${k}`} className="flex items-start gap-2">
                <Checkbox
                  id={`prop-${k}`}
                  checked={Boolean(ui[k])}
                  onChange={(on) => onChange(updateUi(def, name, { [k]: on || undefined }))}
                />
                {t(`designer.props.flag.${k}`)}
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
          {t("designer.props.removeField", { label: ui.label || name })}
        </Button>
      </div>
    </div>
  );
}

/** Uploads a picture for an image field, which then shows it as upload:<id>. */
function ImageUpload({ onUploaded }: { onUploaded: (url: string) => void }) {
  const { t } = useTranslation("admin-catalog");
  const input = useRef<HTMLInputElement>(null);
  const [error, setError] = useState<string>();
  const upload = useMutation({
    mutationFn: uploadImage,
    onSuccess: (img) => {
      setError(undefined);
      onUploaded(img.url);
    },
    onError: (e, f) =>
      setError(
        isApiError(e) ? (e.problem.detail ?? e.problem.title) : t("designer.props.uploadFailed", { name: f.name }),
      ),
  });
  return (
    <div className="flex flex-col gap-1">
      <input
        ref={input}
        type="file"
        hidden
        tabIndex={-1}
        accept="image/png,image/jpeg,image/gif,image/webp"
        onChange={(e) => {
          const f = e.target.files?.[0];
          e.target.value = "";
          if (!f) return;
          if (f.size > 2 * 1024 * 1024) setError(t("designer.props.tooLarge", { name: f.name }));
          else upload.mutate(f);
        }}
      />
      <Button
        size="sm"
        className="self-start"
        disabled={upload.isPending}
        aria-describedby={error ? "prop-image-upload-error" : "prop-image-upload-hint"}
        onClick={() => input.current?.click()}
      >
        {upload.isPending ? <LoaderCircle className="animate-spin" /> : <Upload />}
        {t("designer.props.upload")}
      </Button>
      {error ? (
        <p id="prop-image-upload-error" className="text-secondary text-bad">
          {error}
        </p>
      ) : (
        <p id="prop-image-upload-hint" className="text-secondary text-muted">
          {t("designer.props.uploadHint")}
        </p>
      )}
    </div>
  );
}

/** What a file field takes: its types, which it must name to be published, and its largest file. */
function FileLimits({
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
  const { t } = useTranslation("admin-catalog");
  const ui = def.ui_schema.fields[name];
  if (!ui) return null;
  const types = problems.filter((p) => p.at === "accept" || p.at.startsWith("accept/"));
  const size = problems.filter((p) => p.at === "max_size_mb");
  return (
    <>
      <Field label={t("designer.props.accept")} htmlFor="prop-accept" hint={t("designer.props.acceptHint")}>
        <CommitInput
          id="prop-accept"
          className={mono}
          value={(ui.accept ?? []).join(", ")}
          invalid={types.length > 0}
          describedBy={types.length ? "prop-accept-problems" : undefined}
          onCommit={(v) => {
            const list = v
              .split(",")
              .map((t) => t.trim())
              .filter(Boolean);
            onChange(updateUi(def, name, { accept: list.length ? list : undefined }));
          }}
        />
        <ProblemList id="prop-accept-problems" list={types} />
      </Field>
      <Field label={t("designer.props.maxSize")} htmlFor="prop-max-size" hint={t("designer.props.maxSizeHint")}>
        <Input
          id="prop-max-size"
          type="number"
          min={1}
          max={25}
          step={1}
          placeholder="10"
          value={ui.max_size_mb ?? ""}
          aria-invalid={size.length > 0 || undefined}
          aria-describedby={size.length ? "prop-max-size-problems" : undefined}
          onChange={(e) =>
            onChange(updateUi(def, name, { max_size_mb: e.target.value ? Number(e.target.value) : undefined }))
          }
        />
        <ProblemList id="prop-max-size-problems" list={size} />
      </Field>
    </>
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

/** What a field's help is, where it isn't help under the field. */
const helpLabel = (w: UiWidget) =>
  tr(w === "info" || w === "image" ? `designer.props.help.${w}` : "designer.props.help.default");

/** The list fields whose fewest and most items the designer sets. */
const countedWidgets = ["datagrid", "tags", "dual_list"] as const;
const isCounted = (w: UiWidget): w is (typeof countedWidgets)[number] =>
  (countedWidgets as readonly UiWidget[]).includes(w);
const itemBounds = ["minItems", "maxItems"] as const;
const numberBounds = ["minimum", "maximum", "multipleOf"] as const;
const dateBounds = ["formatMinimum", "formatMaximum"] as const;
const handlingFlags = ["secret", "expose_to_inventory", "approver_editable"] as const;

const cellWidgets = ["text", "number", "toggle", "select"] as const;

/** A table's columns, or an object's sub-fields: each one's key, label, widget, choices and whether it's required. */
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
  const { t } = useTranslation("admin-catalog");
  const columns = def.ui_schema.fields[name]?.columns ?? [];
  const [keyError, setKeyError] = useState<{ index: number; message: string }>();
  const object = def.ui_schema.fields[name]?.widget === "object";
  return (
    <fieldset className="flex flex-col gap-2 border-t border-line pt-3">
      <legend className="text-secondary font-medium">
        {object ? t("designer.props.subFields") : t("designer.props.columns")}
      </legend>
      <ProblemList id="prop-columns-problems" list={problems.filter((p) => p.at.startsWith("columns"))} />
      {columns.map((c, i) => {
        const at = `prop-column-${i}`;
        const error = keyError?.index === i ? keyError.message : undefined;
        return (
          <div key={i} className="flex flex-col gap-2 rounded-md border border-line p-2">
            <div className="grid grid-cols-2 gap-2">
              <Field label={t("designer.props.key")} htmlFor={`${at}-key`} error={error}>
                <CommitInput
                  id={`${at}-key`}
                  value={c.key}
                  className={mono}
                  onCommit={(to) => {
                    if (to === c.key) return;
                    if (!namePattern.test(to))
                      return setKeyError({ index: i, message: t("designer.props.namePattern") });
                    if (columns.some((x) => x.key === to))
                      return setKeyError({ index: i, message: t("designer.props.taken", { name: to }) });
                    setKeyError(undefined);
                    onChange(updateColumn(def, name, i, { key: to }));
                  }}
                />
              </Field>
              <Field label={t("designer.props.label")} htmlFor={`${at}-label`}>
                <Input
                  id={`${at}-label`}
                  value={c.label}
                  onChange={(e) => onChange(updateColumn(def, name, i, { label: e.target.value }))}
                />
              </Field>
            </div>
            <div className="grid grid-cols-2 items-end gap-2">
              <Field label={t("designer.props.widget")} htmlFor={`${at}-widget`}>
                <Select
                  id={`${at}-widget`}
                  value={c.widget}
                  onChange={(w) => onChange(updateColumn(def, name, i, { widget: w as UiColumn["widget"] }))}
                  options={cellWidgets.map((w) => ({ value: w, label: widgetLabel(w) }))}
                />
              </Field>
              <label htmlFor={`${at}-required`} className="flex h-8 items-center gap-2 text-body">
                <Checkbox
                  id={`${at}-required`}
                  checked={isColumnRequired(def, name, c.key)}
                  onChange={(on) => onChange(updateColumn(def, name, i, { required: on }))}
                />
                {t("designer.props.required")}
              </label>
            </div>
            {c.widget === "select" && (
              <Field
                label={t("designer.props.choices")}
                htmlFor={`${at}-options`}
                hint={t("designer.props.optionsHint")}
              >
                <ColumnOptions
                  id={`${at}-options`}
                  value={optionsText(c.options)}
                  onCommit={(t) => onChange(updateColumn(def, name, i, { options: parseOptions(t) }))}
                />
              </Field>
            )}
            <div>
              <Button size="sm" variant="quiet" onClick={() => onChange(removeColumn(def, name, i))}>
                {t(object ? "designer.props.removeSubField" : "designer.props.removeColumn", {
                  name: c.label || c.key,
                })}
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
          {object ? t("designer.props.addSubField") : t("designer.props.addColumn")}
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
  const { t } = useTranslation("admin-catalog");
  const prop = props(def)[name] ?? {};
  return (
    <div className="grid grid-cols-2 gap-2">
      {dateBounds.map((k) => {
        const v = typeof prop[k] === "string" ? prop[k] : "";
        return (
          <Field key={k} label={t(`designer.props.${k}`)} htmlFor={`prop-${k}`} hint={time ? zoneLabel() : undefined}>
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
  const { t } = useTranslation("admin-catalog");
  const part = layoutOf(def);
  if (!s) return null;
  return (
    <div className="flex flex-col gap-3">
      <ProblemList id="section-problems" list={problems} />
      <Field label={t("designer.props.title")} htmlFor="section-title">
        <Input
          id="section-title"
          value={s.title}
          onChange={(e) => onChange(updateSection(def, s.key, { title: e.target.value }))}
        />
      </Field>
      <Field label={t("designer.props.key")} htmlFor="section-key" error={keyError || undefined}>
        <CommitInput
          id="section-key"
          className={mono}
          value={s.key}
          onCommit={(k) => {
            if (!k.trim()) return setKeyError(t(`designer.part.${part}.giveKey`));
            if (def.ui_schema.layout.sections.some((x) => x.key === k))
              return setKeyError(t(`designer.part.${part}.keyTaken`, { key: k }));
            setKeyError("");
            onChange(updateSection(def, s.key, { key: k }));
            onSelect({ kind: "section", key: k });
            focusLater("section-key");
          }}
        />
      </Field>
      <Field label={t("designer.props.description")} htmlFor="section-description">
        <Input
          id="section-description"
          value={s.description ?? ""}
          onChange={(e) => onChange(updateSection(def, s.key, { description: e.target.value }))}
        />
      </Field>
      <Field label={t("designer.props.columns")} htmlFor="section-columns">
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
          title={s.fields.length > 0 ? t("designer.props.moveFieldsFirst") : undefined}
          onClick={() => {
            onChange(removeSection(def, s.key));
            onSelect(null);
            focusLater("designer-add-section");
          }}
        >
          {t(`designer.part.${part}.remove`)}
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
  scope = "item",
}: {
  def: Def;
  selection: Selection;
  onSelect: (s: Selection) => void;
  onChange: (d: Def) => void;
  problems: Problems;
  /** The preview's rule results. */
  states?: FieldStates;
  test: SourceTest;
  scope?: FormScope;
}) {
  const sectionIndex =
    selection?.kind === "section" ? def.ui_schema.layout.sections.findIndex((s) => s.key === selection.key) : -1;
  const { t } = useTranslation("admin-catalog");
  const part = layoutOf(def);
  const title =
    selection?.kind === "section"
      ? t(`designer.part.${part}.one`)
      : selection?.kind === "validations"
        ? t("designer.fields.validations")
        : t("designer.props.properties");
  return (
    <Panel title={title}>
      {!selection && <p className="text-secondary text-muted">{t(`designer.part.${part}.choose`)}</p>}
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
          scope={scope}
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
      {selection?.kind === "validations" && (
        <ValidationsProperties def={def} onChange={onChange} problems={problems.validations} />
      )}
    </Panel>
  );
}
