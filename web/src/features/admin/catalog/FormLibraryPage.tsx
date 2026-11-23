// The form library (ADR-0043): value sources and validations any item's form
// names by key and version. A save makes the next version, and gives the
// items using an earlier one a draft naming it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Library, Plus, X } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm, keys, q } from "@/lib/query";
import { toText } from "@/lib/utils";
import { relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type Entry = components["schemas"]["FormLibraryEntry"];
type EntryWrite = components["schemas"]["FormLibraryEntryWrite"];
type Purpose = components["schemas"]["FormLibraryPurpose"];
type Result = components["schemas"]["FormLibraryTestResult"];
type Kind = "rest" | "cel";

const libraryKey = keys.catalogAdmin.formLibrary;

const tr = i18n.getFixedT(null, "admin-catalog");

const purposes = ["source", "validation"] as const satisfies readonly Purpose[];
const kinds = ["rest", "cel"] as const satisfies readonly Kind[];
const purposeLabel = (use: Purpose) => tr(`library.purpose.${use}`);
const versionState = (state: string) =>
  state === "draft" || state === "published" || state === "deprecated"
    ? tr(`item.state.${state}`).toLowerCase()
    : state;
const kindLabel = (kind: string) => (kind === "rest" || kind === "cel" ? tr(`library.kind.${kind}`) : kind);

// Code inside sentences, the same in every language.
const paramsExpr = "${params.<name>}";
const paramsRef = "params.<name>";
const searchExpr = "${search}";
const searchRef = "search";
const refusalShape = '{"<parameter>": "message"}';
const inputsExample = "inputs.environment";
const valueSourceExample = "value_source: {kind: library, name: cmdbServices, version: 2}";

/** Code inside a sentence: Trans leaves its text alone. */
const Expr = ({ text }: { text: string }) => <code className="font-mono">{text}</code>;

/** The parameters an entry's kind and purpose take, in the order the sheet shows them. */
const paramFields: Record<Purpose, Record<Kind, string[]>> = {
  source: {
    rest: ["url", "method", "body", "secret", "items", "value", "label", "meta", "parent"],
    cel: ["options", "value"],
  },
  validation: { rest: ["url", "method", "body", "secret", "valid", "message", "errors"], cel: ["check"] },
};

const paramLabel = (): Record<string, { label: string; hint?: string; code?: boolean; long?: boolean }> => ({
  url: { label: tr("library.param.url"), hint: tr("library.param.urlHint", { expr: paramsExpr }), code: true },
  method: { label: tr("library.param.method") },
  body: {
    label: tr("library.param.body"),
    hint: tr("library.param.bodyHint", { expr: paramsExpr }),
    code: true,
    long: true,
  },
  secret: { label: tr("library.param.secret"), hint: tr("library.param.secretHint") },
  items: { label: tr("library.param.items"), hint: tr("library.param.itemsHint"), code: true },
  value: { label: tr("library.param.value"), hint: tr("library.param.valueHint"), code: true },
  label: { label: tr("library.param.label"), hint: tr("library.param.labelHint"), code: true },
  meta: { label: tr("library.param.meta"), hint: tr("library.param.metaHint"), code: true },
  parent: { label: tr("library.param.parent"), hint: tr("library.param.parentHint"), code: true },
  valid: { label: tr("library.param.valid"), hint: tr("library.param.validHint"), code: true },
  message: { label: tr("library.param.message"), hint: tr("library.param.messageHint"), code: true },
  errors: { label: tr("library.param.errors"), hint: tr("library.param.errorsHint"), code: true },
  options: {
    label: tr("library.param.options"),
    hint: tr("library.param.optionsHint", { expr: paramsRef }),
    code: true,
    long: true,
  },
  check: {
    label: tr("library.param.check"),
    hint: tr("library.param.checkHint", { shape: refusalShape }),
    code: true,
    long: true,
  },
});

interface ParamRow {
  name: string;
  label: string;
  required: boolean;
  description: string;
}

const text = (v: unknown) => (v === undefined || v === null ? "" : toText(v));

/** A test parameter as typed: JSON numbers and booleans as such, anything else as text. */
function typed(v: string): unknown {
  const t = v.trim();
  if (t === "true" || t === "false") return t === "true";
  if (t !== "" && Number.isFinite(Number(t))) return Number(t);
  return v;
}

/** The rows sent, by their index on screen: rows with neither a name nor a label are left out. */
const sentRows = (rows: ParamRow[]) => rows.flatMap((r, i) => (r.name.trim() || r.label.trim() ? [i] : []));

/** The server's problems with the parameter it got n-th, put on that row on screen. */
function onScreen(errors: Record<string, string>, rows: number[]): Record<string, string> {
  return Object.fromEntries(
    Object.entries(errors).map(([at, why]) => [
      at.replace(/^(\/entry)?\/parameters\/(\d+)/, (all, entry: string | undefined, n: string) =>
        rows[Number(n)] === undefined ? all : `${entry ?? ""}/parameters/${rows[Number(n)]}`,
      ),
      why,
    ]),
  );
}

const none = "none";

/** The server's problems no field of the sheet shows. */
function unplaced(errors: Record<string, string>, shown: string[], parameters: number): string[] {
  return Object.entries(errors)
    .filter(([at]) => {
      if (["/key", "/name", "/description", "/use", "/kind", "/search", "/message"].includes(at)) return false;
      if (shown.some((p) => at === `/params/${p}`)) return false;
      const m = /^\/parameters\/(\d+)\/(name|label|description|required)$/.exec(at);
      return !(m && Number(m[1]) < parameters);
    })
    .map(([, why]) => why);
}

function UsedBy({ entry }: { entry: Entry }) {
  const { t } = useTranslation("admin-catalog");
  if (entry.used_by.length === 0) return <p className="text-secondary text-muted">{t("library.noUse")}</p>;
  return (
    <ul className="flex flex-col gap-1 text-secondary">
      {entry.used_by.map((u) => (
        <li key={`${u.version_id}-${u.entry_version}`} className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{u.name}</span>
          <span className="text-muted">
            {t("library.usedVersion", { number: u.number, state: versionState(u.state) })}
          </span>
          <Badge tone={u.entry_version === entry.version ? "ok" : "neutral"}>
            {t("library.usesVersion", { version: u.entry_version })}
          </Badge>
        </li>
      ))}
    </ul>
  );
}

function TestResult({ result, use }: { result: Result; use: Purpose }) {
  const { t } = useTranslation("admin-catalog");
  if (use === "validation")
    return result.valid ? (
      <p className="text-secondary text-ok">{t("library.passes")}</p>
    ) : (
      <div className="text-secondary">
        <p className="text-bad">{t("library.refuses")}</p>
        <ul className="list-disc pl-5">
          {(result.messages ?? []).map((m, i) => (
            <li key={i}>
              {m.field && <span className="font-mono">{t("library.fieldIs", { field: m.field })}</span>}
              {m.message}
            </li>
          ))}
        </ul>
      </div>
    );
  const options = result.options ?? [];
  return (
    <div className="flex flex-col gap-1 text-secondary">
      {result.value !== undefined && (
        <p>
          {t("library.valueIs")} <span className="font-mono">{JSON.stringify(result.value)}</span>
        </p>
      )}
      {result.options !== undefined && (
        <>
          <p>{t("library.options", { count: options.length })}</p>
          {options.length > 0 && (
            <ul className="list-disc pl-5">
              {options.slice(0, 10).map((o) => (
                <li key={o.value}>
                  {o.label} <span className="font-mono text-muted">{o.value}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
    </div>
  );
}

function EntrySheet({ existing, onClose }: { existing?: Entry; onClose: () => void }) {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const [key, setKey] = useState(existing?.key ?? "");
  const [name, setName] = useState(existing?.name ?? "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [use, setUse] = useState<Purpose>(existing?.use ?? "source");
  const [kind, setKind] = useState<Kind>(existing?.kind === "cel" ? "cel" : "rest");
  const [params, setParams] = useState<Record<string, string>>(
    Object.fromEntries(Object.entries(existing?.params ?? {}).map(([k, v]) => [k, text(v)])),
  );
  const [search, setSearch] = useState(existing?.search ?? false);
  const [message, setMessage] = useState(existing?.message ?? "");
  const [parameters, setParameters] = useState<ParamRow[]>(
    (existing?.parameters ?? []).map((p) => ({
      name: p.name,
      label: p.label,
      required: Boolean(p.required),
      description: p.description ?? "",
    })),
  );
  const [tryWith, setTryWith] = useState<Record<string, string>>({});
  const [tryProject, setTryProject] = useState(none);
  const [tryEnvironment, setTryEnvironment] = useState(none);
  const projects = useQuery(adm.projectNames());
  const environments = useQuery(q.environments());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const shown = paramFields[use][kind];
  const body = (): EntryWrite => {
    // What the sheet doesn't show, such as cache_seconds, stays as it was while the kind does.
    const kept = existing && existing.use === use && existing.kind === kind;
    const p: Record<string, unknown> = kept ? { ...existing.params } : {};
    for (const k of shown) {
      delete p[k];
      if (params[k]?.trim()) p[k] = params[k];
    }
    return {
      name,
      description,
      use,
      kind,
      params: p,
      ...(use === "source" && search ? { search: true } : {}),
      ...(use === "validation" && message.trim() ? { message: message.trim() } : {}),
      parameters: sentRows(parameters)
        .map((i) => parameters[i]!)
        .map((r) => ({
          name: r.name.trim(),
          label: r.label.trim(),
          ...(r.required ? { required: true } : {}),
          ...(r.description.trim() ? { description: r.description.trim() } : {}),
        })),
    };
  };
  const save = useMutation({
    mutationFn: (_rows: number[]) =>
      existing
        ? call(api.PUT("/admin/form-library/{key}", { params: { path: { key: existing.key } }, body: body() }))
        : call(api.POST("/admin/form-library", { body: { ...body(), key } })),
    onSuccess: (e) => {
      void qc.invalidateQueries({ queryKey: libraryKey });
      void qc.invalidateQueries({ queryKey: keys.catalogAdmin.all });
      const drafts = e.drafts ?? [];
      const made = drafts.filter((d) => !d.problem);
      const stuck = drafts.filter((d) => d.problem);
      toast.success(t("library.saved", { name: e.name, version: e.version }), {
        description:
          [
            made.length
              ? t("library.draftsNaming", {
                  drafts: made
                    .map((d) => t(d.created ? "library.draftNew" : "library.draft", { name: d.name, number: d.number }))
                    .join(", "),
                })
              : "",
            ...stuck.map((d) => t("library.draftProblem", { name: d.name, problem: d.problem })),
          ]
            .filter(Boolean)
            .join(" ") || undefined,
      });
      onClose();
    },
    onError: (e, rows) => {
      setErrors(onScreen(fieldErrors(e), rows));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("library.notSaved"));
    },
  });
  const named = new Set(parameters.map((r) => r.name.trim()).filter(Boolean));
  const test = useMutation({
    mutationFn: (_rows: number[]) =>
      call(
        api.POST("/admin/form-library/test", {
          body: {
            entry: { ...body(), ...(existing ? { key: existing.key } : key ? { key } : {}) },
            params: Object.fromEntries(
              Object.entries(tryWith)
                .filter(([k, v]) => named.has(k) && v.trim())
                .map(([k, v]) => [k, typed(v)]),
            ),
            ...(tryProject !== none ? { project_id: tryProject } : {}),
            ...(tryEnvironment !== none ? { environment: tryEnvironment } : {}),
          },
        }),
      ),
    onError: (e, rows) => {
      if (isApiError(e) && e.fieldErrors.length) setErrors(onScreen(fieldErrors(e), rows));
    },
  });
  const testFailure =
    test.error &&
    (isApiError(test.error) ? (test.error.problem.detail ?? test.error.problem.title) : t("library.testFailed"));
  const setParam = (k: string, v: string) => setParams((p) => ({ ...p, [k]: v }));
  const setRow = (i: number, r: ParamRow) => setParameters((xs) => xs.map((x, j) => (j === i ? r : x)));
  const err = (at: string) => errors[at] ?? errors[`/entry${at}`];
  const elsewhere = unplaced(
    Object.fromEntries(Object.entries(errors).map(([k, v]) => [k.replace(/^\/entry/, ""), v])),
    shown,
    parameters.length,
  );
  const next = existing ? Math.max(...existing.versions) + 1 : 1;
  const specs = paramLabel();
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? t("library.change", { name: existing.name }) : t("library.new")}
      description={existing ? t("library.changeLead", { version: next }) : t("library.newLead")}
      submitLabel={save.isPending ? t("library.saving") : existing ? t("library.saveVersion") : t("library.create")}
      submitDisabled={!name.trim() || (!existing && !key)}
      onSubmit={() => {
        setErrors({});
        save.mutate(sentRows(parameters));
      }}
    >
      {elsewhere.length > 0 && (
        <div role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-secondary text-bad">
          {elsewhere.map((why) => (
            <p key={why}>{why}</p>
          ))}
        </div>
      )}
      <Field label={t("library.key")} htmlFor="fl-key" hint={t("library.keyHint")} error={err("/key")}>
        <Input
          id="fl-key"
          className="font-mono"
          value={key}
          disabled={Boolean(existing)}
          onChange={(e) => setKey(e.target.value)}
        />
      </Field>
      <Field label={t("library.name")} htmlFor="fl-name" error={err("/name")}>
        <Input id="fl-name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label={t("library.description")} htmlFor="fl-description" error={err("/description")}>
        <Input
          id="fl-description"
          value={description}
          maxLength={500}
          onChange={(e) => setDescription(e.target.value)}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field
          label={t("library.purposeLabel")}
          htmlFor="fl-use"
          hint={existing ? t("library.purposeHint") : undefined}
          error={err("/use")}
        >
          <Select
            id="fl-use"
            value={use}
            disabled={Boolean(existing)}
            onChange={(v) => setUse(v as Purpose)}
            options={purposes.map((value) => ({ value, label: purposeLabel(value) }))}
          />
        </Field>
        <Field label={t("library.kindLabel")} htmlFor="fl-kind" error={err("/kind")}>
          <Select
            id="fl-kind"
            value={kind}
            onChange={(v) => setKind(v as Kind)}
            options={kinds.map((value) => ({ value, label: kindLabel(value) }))}
          />
        </Field>
      </div>
      <fieldset className="flex flex-col gap-3">
        <legend className="mb-1 text-secondary font-medium">
          {kind === "rest" ? t("library.theCall") : t("library.theExpression")}
        </legend>
        {shown.map((k) => {
          const spec = specs[k]!;
          const id = `fl-param-${k}`;
          const hint = kind === "cel" && k === "value" ? t("library.param.celValueHint") : spec.hint;
          if (k === "method")
            return (
              <Field key={k} label={spec.label} htmlFor={id} error={err(`/params/${k}`)}>
                <Select
                  id={id}
                  value={params.method || "GET"}
                  onChange={(v) => setParam("method", v)}
                  options={[
                    { value: "GET", label: "GET" },
                    { value: "POST", label: "POST" },
                  ]}
                />
              </Field>
            );
          if (k === "body" && (params.method || "GET") !== "POST") return null;
          return (
            <Field key={k} label={spec.label} htmlFor={id} hint={hint} error={err(`/params/${k}`)}>
              {spec.long ? (
                <Textarea
                  id={id}
                  rows={3}
                  className={spec.code ? "font-mono" : undefined}
                  value={params[k] ?? ""}
                  onChange={(e) => setParam(k, e.target.value)}
                />
              ) : (
                <Input
                  id={id}
                  className={spec.code ? "font-mono" : undefined}
                  value={params[k] ?? ""}
                  onChange={(e) => setParam(k, e.target.value)}
                />
              )}
            </Field>
          );
        })}
        {err("/params") && <p className="text-secondary text-bad">{err("/params")}</p>}
        {use === "source" && (
          <label htmlFor="fl-search" className="flex items-center gap-2 text-body">
            <Checkbox id="fl-search" checked={search} onChange={setSearch} />
            {t("library.searches", { expr: kind === "rest" ? searchExpr : searchRef })}
          </label>
        )}
        {err("/search") && <p className="text-secondary text-bad">{err("/search")}</p>}
        {use === "validation" && (
          <Field
            label={t("library.message")}
            htmlFor="fl-message"
            hint={t("library.messageHint")}
            error={err("/message")}
          >
            <Input id="fl-message" value={message} maxLength={500} onChange={(e) => setMessage(e.target.value)} />
          </Field>
        )}
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">{t("library.parameters")}</legend>
        <p className="text-secondary text-muted">
          <Trans t={t} i18nKey="library.parametersLead" components={{ expr: <Expr text={inputsExample} /> }} />
        </p>
        {parameters.map((r, i) => (
          <div key={i} className="border-border flex flex-col gap-3 rounded-md border p-3">
            <div className="grid grid-cols-2 gap-3">
              <Field label={t("library.name")} htmlFor={`fl-prm-${i}-name`} error={err(`/parameters/${i}/name`)}>
                <Input
                  id={`fl-prm-${i}-name`}
                  className="font-mono"
                  value={r.name}
                  onChange={(e) => setRow(i, { ...r, name: e.target.value })}
                />
              </Field>
              <Field label={t("library.label")} htmlFor={`fl-prm-${i}-label`} error={err(`/parameters/${i}/label`)}>
                <Input
                  id={`fl-prm-${i}-label`}
                  value={r.label}
                  onChange={(e) => setRow(i, { ...r, label: e.target.value })}
                />
              </Field>
            </div>
            <Field label={t("library.description")} htmlFor={`fl-prm-${i}-description`}>
              <Input
                id={`fl-prm-${i}-description`}
                value={r.description}
                onChange={(e) => setRow(i, { ...r, description: e.target.value })}
              />
            </Field>
            <div className="flex items-center gap-4">
              <label htmlFor={`fl-prm-${i}-required`} className="flex items-center gap-2 text-body">
                <Checkbox
                  id={`fl-prm-${i}-required`}
                  checked={r.required}
                  onChange={(v) => setRow(i, { ...r, required: v })}
                />
                {t("library.required")}
              </label>
              <Button
                type="button"
                size="sm"
                variant="quiet"
                className="ml-auto"
                aria-label={t("library.removeParameter", { name: r.name || i + 1 })}
                onClick={() => setParameters((xs) => xs.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" /> {t("library.remove")}
              </Button>
            </div>
          </div>
        ))}
        <div>
          <Button
            type="button"
            variant="secondary"
            onClick={() => setParameters((xs) => [...xs, { name: "", label: "", required: false, description: "" }])}
          >
            <Plus className="size-3.5" /> {t("library.addParameter")}
          </Button>
        </div>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">{t("library.test")}</legend>
        <p className="text-secondary text-muted">{t("library.testLead")}</p>
        <div className="grid grid-cols-2 gap-3">
          <Field
            label={t("library.project")}
            htmlFor="fl-try-project"
            hint={t("library.projectHint")}
            error={err("/project_id")}
          >
            <Select
              id="fl-try-project"
              value={tryProject}
              loading={projects.isPending}
              onChange={setTryProject}
              options={[
                { value: none, label: t("library.none") },
                ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
              ]}
            />
          </Field>
          <Field label={t("library.environment")} htmlFor="fl-try-environment" error={err("/environment")}>
            <Select
              id="fl-try-environment"
              value={tryEnvironment}
              loading={environments.isPending}
              onChange={setTryEnvironment}
              options={[
                { value: none, label: t("library.none") },
                ...(environments.data?.items ?? []).map((e) => ({ value: e.key, label: e.name })),
              ]}
            />
          </Field>
        </div>
        {[...named].map((n) => {
          const r = parameters.find((x) => x.name.trim() === n)!;
          return (
            <Field key={n} label={r.label.trim() || n} htmlFor={`fl-try-${n}`}>
              <Input
                id={`fl-try-${n}`}
                value={tryWith[n] ?? ""}
                onChange={(e) => setTryWith((w) => ({ ...w, [n]: e.target.value }))}
              />
            </Field>
          );
        })}
        <div>
          <Button
            type="button"
            variant="secondary"
            disabled={test.isPending}
            onClick={() => test.mutate(sentRows(parameters))}
          >
            {test.isPending ? t("library.testing") : t("library.runTest")}
          </Button>
        </div>
        <div aria-live="polite">
          {test.data && !test.isPending && <TestResult result={test.data} use={use} />}
          {testFailure && !test.isPending && !(isApiError(test.error) && test.error.fieldErrors.length) && (
            <p className="text-secondary text-bad">{testFailure}</p>
          )}
        </div>
      </fieldset>
      {existing && (
        <section aria-labelledby="fl-used-by" className="flex flex-col gap-2">
          <h3 id="fl-used-by" className="text-secondary font-medium">
            {t("library.usedBy")}
          </h3>
          <UsedBy entry={existing} />
        </section>
      )}
    </Sheet>
  );
}

/** The items whose published or deprecated versions name the entry: deleting it would break them. */
const holders = (e: Entry) => [...new Set(e.used_by.filter((u) => u.state !== "draft").map((u) => u.name))];

const columns = (): Column<Entry>[] => [
  {
    id: "name",
    header: tr("library.col.name"),
    sortValue: (e) => e.name,
    cell: (e) => (
      <>
        <div className="font-medium">{e.name}</div>
        <div className="font-mono text-caption text-muted">{e.key}</div>
      </>
    ),
  },
  {
    id: "use",
    header: tr("library.col.purpose"),
    sortValue: (e) => e.use,
    cell: (e) => <Badge tone={e.use === "validation" ? "warn" : "accent"}>{purposeLabel(e.use)}</Badge>,
  },
  {
    id: "kind",
    header: tr("library.col.kind"),
    sortValue: (e) => e.kind,
    cell: (e) => <span className="text-secondary text-muted">{kindLabel(e.kind)}</span>,
  },
  {
    id: "version",
    header: tr("library.col.version"),
    sortValue: (e) => e.version,
    cell: (e) => <span className="text-secondary text-muted">{e.version}</span>,
  },
  {
    id: "used",
    header: tr("library.col.used"),
    sortValue: (e) => new Set(e.used_by.map((u) => u.item_id)).size,
    cell: (e) => {
      const items = [...new Set(e.used_by.map((u) => u.name))];
      return (
        <span className="text-secondary text-muted">{items.length ? items.join(", ") : tr("library.nothing")}</span>
      );
    },
  },
  {
    id: "changed",
    header: tr("library.col.changed"),
    sortValue: (e) => e.updated_at,
    cell: (e) => <span className="text-secondary text-muted">{relative(e.updated_at)}</span>,
  },
];

export function FormLibraryPage() {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const entries = useQuery(q.formLibrary());
  const [editing, setEditing] = useState<{ entry?: Entry } | null>(null);
  const [removing, setRemoving] = useState<Entry | null>(null);
  const remove = useMutation({
    mutationFn: (e: Entry) => call(api.DELETE("/admin/form-library/{key}", { params: { path: { key: e.key } } })),
    onSuccess: (_, e) => {
      void qc.invalidateQueries({ queryKey: libraryKey });
      toast.success(t("library.deleted", { name: e.name }));
    },
    onError: (e) => failed(e, t("library.notDeleted")),
  });
  const list = entries.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing({})}>
      {t("library.newEntry")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("library.title")}
        description={<Trans t={t} i18nKey="library.lead" components={{ expr: <Expr text={valueSourceExample} /> }} />}
      />
      <Panel title={t("library.entries")} flush actions={list?.length ? add : undefined}>
        <DataTable
          label={t("library.tableLabel")}
          columns={columns()}
          rows={list}
          rowKey={(e) => e.key}
          minWidth={820}
          loading={entries.isPending}
          error={entries.isError && <ErrorState error={entries.error} onRetry={() => void entries.refetch()} />}
          empty={<EmptyState icon={Library} title={t("library.empty")} body={t("library.emptyBody")} action={add} />}
          rowMenuLabel={(e) => t("library.actionsFor", { name: e.name })}
          rowMenu={(e) => (
            <>
              <MenuItem onSelect={() => setEditing({ entry: e })}>{t("library.changeOrTest")}</MenuItem>
              <MenuSeparator />
              <MenuItem danger disabled={holders(e).length > 0} onSelect={() => setRemoving(e)}>
                {holders(e).length > 0 ? (
                  <>
                    <span>{t("library.deleteHeld")}</span>{" "}
                    <span>{t("library.holders", { count: holders(e).length })}</span>
                  </>
                ) : (
                  t("library.delete")
                )}
              </MenuItem>
            </>
          )}
        />
      </Panel>
      {editing && <EntrySheet existing={editing.entry} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("library.deleteTitle", { name: removing.name })}
          description={t("library.deleteBody")}
          consequences={t("library.deleteConsequences")}
          confirmLabel={t("library.deleteConfirm")}
          pending={remove.isPending}
          onConfirm={() => {
            const e = removing;
            setRemoving(null);
            remove.mutate(e);
          }}
        />
      )}
    </Page>
  );
}
