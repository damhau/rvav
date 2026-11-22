// The form library (ADR-0043): value sources and validations any item's form
// names by key and version. A save makes the next version, and gives the
// items using an earlier one a draft naming it.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Library, Plus, X } from "lucide-react";
import { useState } from "react";
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

type Entry = components["schemas"]["FormLibraryEntry"];
type EntryWrite = components["schemas"]["FormLibraryEntryWrite"];
type Purpose = components["schemas"]["FormLibraryPurpose"];
type Result = components["schemas"]["FormLibraryTestResult"];
type Kind = "rest" | "cel";

const libraryKey = keys.catalogAdmin.formLibrary;

const purposeLabel: Record<Purpose, string> = { source: "Value source", validation: "Validation" };
const kindLabel: Record<Kind, string> = { rest: "REST call", cel: "CEL expression" };

/** The parameters an entry's kind and purpose take, in the order the sheet shows them. */
const paramFields: Record<Purpose, Record<Kind, string[]>> = {
  source: {
    rest: ["url", "method", "body", "secret", "items", "value", "label", "meta", "parent"],
    cel: ["options", "value"],
  },
  validation: { rest: ["url", "method", "body", "secret", "valid", "message", "errors"], cel: ["check"] },
};

const paramLabel: Record<string, { label: string; hint?: string; code?: boolean; long?: boolean }> = {
  url: {
    label: "URL",
    hint: "On a host REST value sources may call. Read the parameters as ${params.<name>}.",
    code: true,
  },
  method: { label: "Method" },
  body: { label: "Body", hint: "Sent with POST. Read the parameters as ${params.<name>}.", code: true, long: true },
  secret: { label: "Secret", hint: "A stored secret's name: rvav sends it as a bearer token." },
  items: { label: "Items", hint: "JSONPath to the list in the answer, such as $.items[*].", code: true },
  value: { label: "Value", hint: "JSONPath in each item to its value, such as $.id.", code: true },
  label: { label: "Label", hint: "JSONPath in each item to what requesters see.", code: true },
  meta: { label: "Detail", hint: "JSONPath in each item to a detail shown under it.", code: true },
  parent: {
    label: "Parent",
    hint: "JSONPath in each item to its parent's value, for a tree. Empty for a flat list.",
    code: true,
  },
  valid: { label: "Valid at", hint: "JSONPath to true or false in the answer. $.valid when empty.", code: true },
  message: { label: "Message at", hint: "JSONPath to the refusal's message. $.message when empty.", code: true },
  errors: {
    label: "Errors at",
    hint: "JSONPath to messages by input, or a list of {field, message}. $.errors when empty.",
    code: true,
  },
  options: {
    label: "Options",
    hint: "A list of strings or of {value, label, meta}, from params.<name>, environment, project and user.",
    code: true,
    long: true,
  },
  check: {
    label: "Check",
    hint: 'True, "" or {} when the form passes; false, a message, or {"<parameter>": "message"} when it doesn\'t.',
    code: true,
    long: true,
  },
};

const celValueHint = "One value, for a field that takes one rather than a choice.";

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
  if (entry.used_by.length === 0) return <p className="text-secondary text-muted">No item's form names it yet.</p>;
  return (
    <ul className="flex flex-col gap-1 text-secondary">
      {entry.used_by.map((u) => (
        <li key={`${u.version_id}-${u.entry_version}`} className="flex flex-wrap items-center gap-2">
          <span className="font-medium">{u.name}</span>
          <span className="text-muted">
            version {u.number}, {u.state}
          </span>
          <Badge tone={u.entry_version === entry.version ? "ok" : "neutral"}>uses version {u.entry_version}</Badge>
        </li>
      ))}
    </ul>
  );
}

function TestResult({ result, use }: { result: Result; use: Purpose }) {
  if (use === "validation")
    return result.valid ? (
      <p className="text-secondary text-ok">It passes.</p>
    ) : (
      <div className="text-secondary">
        <p className="text-bad">It refuses:</p>
        <ul className="list-disc pl-5">
          {(result.messages ?? []).map((m, i) => (
            <li key={i}>
              {m.field && <span className="font-mono">{m.field}: </span>}
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
          Value: <span className="font-mono">{JSON.stringify(result.value)}</span>
        </p>
      )}
      {result.options !== undefined && (
        <>
          <p>{options.length === 1 ? "1 option" : `${options.length} options`}</p>
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
      toast.success(`${e.name} saved as version ${e.version}`, {
        description:
          [
            made.length
              ? `Drafts naming it: ${made.map((d) => `${d.name} ${d.number}${d.created ? " (new)" : ""}`).join(", ")}.`
              : "",
            ...stuck.map((d) => `${d.name}: ${d.problem}`),
          ]
            .filter(Boolean)
            .join(" ") || undefined,
      });
      onClose();
    },
    onError: (e, rows) => {
      setErrors(onScreen(fieldErrors(e), rows));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The entry wasn't saved");
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
    (isApiError(test.error) ? (test.error.problem.detail ?? test.error.problem.title) : "The test failed.");
  const setParam = (k: string, v: string) => setParams((p) => ({ ...p, [k]: v }));
  const setRow = (i: number, r: ParamRow) => setParameters((xs) => xs.map((x, j) => (j === i ? r : x)));
  const err = (at: string) => errors[at] ?? errors[`/entry${at}`];
  const elsewhere = unplaced(
    Object.fromEntries(Object.entries(errors).map(([k, v]) => [k.replace(/^\/entry/, ""), v])),
    shown,
    parameters.length,
  );
  const next = existing ? Math.max(...existing.versions) + 1 : 1;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? `Change ${existing.name}` : "New form library entry"}
      description={
        existing
          ? `Saving makes version ${next}, and gives each item that uses an earlier version a draft naming it, to review and publish.`
          : "A value source or a validation any item's form can name, giving its parameters from its inputs."
      }
      submitLabel={save.isPending ? "Saving…" : existing ? "Save as a new version" : "Add"}
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
      <Field
        label="Key"
        htmlFor="fl-key"
        hint="Forms name the entry by its key, which doesn't change."
        error={err("/key")}
      >
        <Input
          id="fl-key"
          className="font-mono"
          value={key}
          disabled={Boolean(existing)}
          onChange={(e) => setKey(e.target.value)}
        />
      </Field>
      <Field label="Name" htmlFor="fl-name" error={err("/name")}>
        <Input id="fl-name" value={name} maxLength={100} onChange={(e) => setName(e.target.value)} />
      </Field>
      <Field label="Description" htmlFor="fl-description" error={err("/description")}>
        <Input
          id="fl-description"
          value={description}
          maxLength={500}
          onChange={(e) => setDescription(e.target.value)}
        />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field
          label="Purpose"
          htmlFor="fl-use"
          hint={existing ? "Forms use it as one: it doesn't change." : undefined}
          error={err("/use")}
        >
          <Select
            id="fl-use"
            value={use}
            disabled={Boolean(existing)}
            onChange={(v) => setUse(v as Purpose)}
            options={Object.entries(purposeLabel).map(([value, label]) => ({ value, label }))}
          />
        </Field>
        <Field label="Kind" htmlFor="fl-kind" error={err("/kind")}>
          <Select
            id="fl-kind"
            value={kind}
            onChange={(v) => setKind(v as Kind)}
            options={Object.entries(kindLabel).map(([value, label]) => ({ value, label }))}
          />
        </Field>
      </div>
      <fieldset className="flex flex-col gap-3">
        <legend className="mb-1 text-secondary font-medium">{kind === "rest" ? "The call" : "The expression"}</legend>
        {shown.map((k) => {
          const spec = paramLabel[k]!;
          const id = `fl-param-${k}`;
          const hint = kind === "cel" && k === "value" ? celValueHint : spec.hint;
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
            Searches by what the requester types (reads {kind === "rest" ? "${search}" : "search"})
          </label>
        )}
        {err("/search") && <p className="text-secondary text-bad">{err("/search")}</p>}
        {use === "validation" && (
          <Field
            label="Message"
            htmlFor="fl-message"
            hint="Shown when it refuses without saying why. A form's own message comes first."
            error={err("/message")}
          >
            <Input id="fl-message" value={message} maxLength={500} onChange={(e) => setMessage(e.target.value)} />
          </Field>
        )}
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">Parameters</legend>
        <p className="text-secondary text-muted">
          What a form gives the entry, each as a CEL expression over its inputs, such as{" "}
          <code className="font-mono">inputs.environment</code>.
        </p>
        {parameters.map((r, i) => (
          <div key={i} className="border-border flex flex-col gap-3 rounded-md border p-3">
            <div className="grid grid-cols-2 gap-3">
              <Field label="Name" htmlFor={`fl-prm-${i}-name`} error={err(`/parameters/${i}/name`)}>
                <Input
                  id={`fl-prm-${i}-name`}
                  className="font-mono"
                  value={r.name}
                  onChange={(e) => setRow(i, { ...r, name: e.target.value })}
                />
              </Field>
              <Field label="Label" htmlFor={`fl-prm-${i}-label`} error={err(`/parameters/${i}/label`)}>
                <Input
                  id={`fl-prm-${i}-label`}
                  value={r.label}
                  onChange={(e) => setRow(i, { ...r, label: e.target.value })}
                />
              </Field>
            </div>
            <Field label="Description" htmlFor={`fl-prm-${i}-description`}>
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
                Required: without it, the entry doesn't run
              </label>
              <Button
                type="button"
                size="sm"
                variant="quiet"
                className="ml-auto"
                aria-label={`Remove parameter ${r.name || i + 1}`}
                onClick={() => setParameters((xs) => xs.filter((_, j) => j !== i))}
              >
                <X className="size-3.5" /> Remove
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
            <Plus className="size-3.5" /> Add parameter
          </Button>
        </div>
      </fieldset>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-secondary font-medium">Test</legend>
        <p className="text-secondary text-muted">
          Runs the entry as written, unsaved changes included, with these parameter values, as a form of this project
          and environment would.
        </p>
        <div className="grid grid-cols-2 gap-3">
          <Field
            label="Project"
            htmlFor="fl-try-project"
            hint="For project, and a REST call's secret of the project."
            error={err("/project_id")}
          >
            <Select
              id="fl-try-project"
              value={tryProject}
              loading={projects.isPending}
              onChange={setTryProject}
              options={[
                { value: none, label: "None" },
                ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
              ]}
            />
          </Field>
          <Field label="Environment" htmlFor="fl-try-environment" error={err("/environment")}>
            <Select
              id="fl-try-environment"
              value={tryEnvironment}
              loading={environments.isPending}
              onChange={setTryEnvironment}
              options={[
                { value: none, label: "None" },
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
                onChange={(e) => setTryWith((t) => ({ ...t, [n]: e.target.value }))}
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
            {test.isPending ? "Testing…" : "Run test"}
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
            Used by
          </h3>
          <UsedBy entry={existing} />
        </section>
      )}
    </Sheet>
  );
}

/** The items whose published or deprecated versions name the entry: deleting it would break them. */
const holders = (e: Entry) => [...new Set(e.used_by.filter((u) => u.state !== "draft").map((u) => u.name))];

const columns: Column<Entry>[] = [
  {
    id: "name",
    header: "Name",
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
    header: "Purpose",
    sortValue: (e) => e.use,
    cell: (e) => <Badge tone={e.use === "validation" ? "warn" : "accent"}>{purposeLabel[e.use]}</Badge>,
  },
  {
    id: "kind",
    header: "Kind",
    sortValue: (e) => e.kind,
    cell: (e) => <span className="text-secondary text-muted">{kindLabel[e.kind as Kind] ?? e.kind}</span>,
  },
  {
    id: "version",
    header: "Latest version",
    sortValue: (e) => e.version,
    cell: (e) => <span className="text-secondary text-muted">{e.version}</span>,
  },
  {
    id: "used",
    header: "Used by",
    sortValue: (e) => new Set(e.used_by.map((u) => u.item_id)).size,
    cell: (e) => {
      const items = [...new Set(e.used_by.map((u) => u.name))];
      return <span className="text-secondary text-muted">{items.length ? items.join(", ") : "Nothing"}</span>;
    },
  },
  {
    id: "changed",
    header: "Changed",
    sortValue: (e) => e.updated_at,
    cell: (e) => <span className="text-secondary text-muted">{relative(e.updated_at)}</span>,
  },
];

export function FormLibraryPage() {
  const qc = useQueryClient();
  const entries = useQuery(q.formLibrary());
  const [editing, setEditing] = useState<{ entry?: Entry } | null>(null);
  const [removing, setRemoving] = useState<Entry | null>(null);
  const remove = useMutation({
    mutationFn: (e: Entry) => call(api.DELETE("/admin/form-library/{key}", { params: { path: { key: e.key } } })),
    onSuccess: (_, e) => {
      void qc.invalidateQueries({ queryKey: libraryKey });
      toast.success(`${e.name} deleted`);
    },
    onError: (e) => failed(e, "The entry wasn't deleted"),
  });
  const list = entries.data?.items;
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing({})}>
      New entry
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Form library"
        description={
          <>
            Value sources and validations any item's form names by key and version, as a field's{" "}
            <code className="font-mono">value_source: {"{kind: library, name: cmdbServices, version: 2}"}</code> or one
            of its validations. A form gives an entry's parameters as CEL over its inputs.
          </>
        }
      />
      <Panel title="Entries" flush actions={list?.length ? add : undefined}>
        <DataTable
          label="Form library entries"
          columns={columns}
          rows={list}
          rowKey={(e) => e.key}
          minWidth={820}
          loading={entries.isPending}
          error={entries.isError && <ErrorState error={entries.error} onRetry={() => void entries.refetch()} />}
          empty={
            <EmptyState
              icon={Library}
              title="No entries yet"
              body="Write once what several forms ask a service, such as the CMDB's applications, or check before approval, such as an open cost centre."
              action={add}
            />
          }
          rowMenuLabel={(e) => `Actions for ${e.name}`}
          rowMenu={(e) => (
            <>
              <MenuItem onSelect={() => setEditing({ entry: e })}>Change or test</MenuItem>
              <MenuSeparator />
              <MenuItem danger disabled={holders(e).length > 0} onSelect={() => setRemoving(e)}>
                {holders(e).length > 0 ? (
                  <>
                    <span>Delete:</span>{" "}
                    <span>{holders(e).length === 1 ? "1 item uses it" : `${holders(e).length} items use it`}</span>
                  </>
                ) : (
                  "Delete"
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
          title={`Delete ${removing.name}?`}
          description="Every version of the entry is deleted."
          consequences="No published item uses it. A draft that names it is refused when next saved."
          confirmLabel="Delete entry"
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
