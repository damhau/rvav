// Shared variables (ADR-0057), as vRO's configuration elements: named values
// that aren't secret, which catalog admins keep and composite steps and
// conditions read as vars.<group>.<name> each time they run.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Variable as VariableIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
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
import type { Variable } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { relative } from "@/lib/format";

type Kind = "text" | "number" | "boolean" | "json";

const kinds: { value: Kind; label: string }[] = [
  { value: "text", label: "Text" },
  { value: "number", label: "Number" },
  { value: "boolean", label: "True or false" },
  { value: "json", label: "List or object (JSON)" },
];

function kindOf(v: unknown): Kind {
  if (typeof v === "number") return "number";
  if (typeof v === "boolean") return "boolean";
  if (typeof v === "string") return "text";
  return "json";
}

const kindLabel = (v: unknown) =>
  Array.isArray(v) ? "List" : kindOf(v) === "json" ? "Object" : kinds.find((k) => k.value === kindOf(v))!.label;

/** A value as the form edits it. */
function editable(v: unknown): string {
  if (v === undefined) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v, null, 2);
}

/** A value in a table cell: text as it is, a list's items, anything else as JSON. */
function shown(v: unknown): string {
  if (typeof v === "string") return v;
  if (Array.isArray(v) && v.every((x) => typeof x !== "object" || x === null))
    return v.map((x) => (typeof x === "string" ? x : JSON.stringify(x))).join(", ");
  return JSON.stringify(v);
}

/** The value the form holds, or why it can't be sent. */
function parsed(kind: Kind, text: string): { value: unknown } | { error: string } {
  switch (kind) {
    case "text":
      return { value: text };
    case "number": {
      const n = Number(text);
      return text.trim() !== "" && Number.isFinite(n) ? { value: n } : { error: "A number, such as 14 or 0.5." };
    }
    case "boolean":
      return { value: text === "true" };
    case "json":
      try {
        const v: unknown = JSON.parse(text);
        if (v === null || typeof v !== "object") return { error: "A list in [ ] or an object in { }." };
        return { value: v };
      } catch (e) {
        return { error: `Not JSON: ${e instanceof Error ? e.message : "it can't be read"}.` };
      }
  }
}

const refOf = (v: Pick<Variable, "group" | "name">) => `vars.${v.group}.${v.name}`;

function VariableSheet({ existing, onClose }: { existing?: Variable; onClose: () => void }) {
  const qc = useQueryClient();
  const [group, setGroup] = useState(existing?.group ?? "");
  const [name, setName] = useState(existing?.name ?? "");
  const [kind, setKind] = useState<Kind>(existing ? kindOf(existing.value) : "text");
  const [text, setText] = useState(existing ? editable(existing.value) : "");
  const [description, setDescription] = useState(existing?.description ?? "");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: (value: unknown) =>
      existing
        ? call(
            api.PUT("/admin/variables/{key}", {
              params: { path: { key: existing.key } },
              body: { value, description },
            }),
          )
        : call(api.POST("/admin/variables", { body: { group, name, value, description } })),
    onSuccess: (v) => {
      void qc.invalidateQueries({ queryKey: adm.variables().queryKey });
      toast.success(existing ? `${refOf(v)} saved` : `${refOf(v)} added`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The variable wasn't saved");
    },
  });
  const changeKind = (k: string) => {
    const next = k as Kind;
    setKind(next);
    if (next === "boolean" && text !== "true" && text !== "false") setText("true");
  };
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? `Change ${refOf(existing)}` : "New variable"}
      description="Steps that run from then on read the value saved, in requests already running too. It isn't masked anywhere: store a password, a token or a key as a secret instead."
      submitLabel={save.isPending ? "Saving…" : existing ? "Save" : "Add"}
      submitDisabled={!group || !name}
      onSubmit={() => {
        const p = parsed(kind, text);
        if ("error" in p) {
          setErrors({ "/value": p.error });
          return;
        }
        setErrors({});
        save.mutate(p.value);
      }}
    >
      <Field
        label="Group"
        htmlFor="variable-group"
        hint="Lower-case letters, digits and underscores, starting with a letter, such as network."
        error={errors["/group"]}
      >
        <Input
          id="variable-group"
          className="font-mono"
          value={group}
          disabled={Boolean(existing)}
          onChange={(e) => setGroup(e.target.value)}
        />
      </Field>
      <Field
        label="Name"
        htmlFor="variable-name"
        hint={
          <>
            Steps and conditions read it as{" "}
            <code className="font-mono">
              vars.{group || "<group>"}.{name || "<name>"}
            </code>
            .
          </>
        }
        error={errors["/name"]}
      >
        <Input
          id="variable-name"
          className="font-mono"
          value={name}
          disabled={Boolean(existing)}
          onChange={(e) => setName(e.target.value)}
        />
      </Field>
      <Field label="Type" htmlFor="variable-kind">
        <Select id="variable-kind" value={kind} onChange={changeKind} options={kinds} />
      </Field>
      {kind === "boolean" ? (
        <Field label="Value" htmlFor="variable-value" error={errors["/value"]}>
          <Select
            id="variable-value"
            value={text === "false" ? "false" : "true"}
            onChange={setText}
            options={[
              { value: "true", label: "True" },
              { value: "false", label: "False" },
            ]}
          />
        </Field>
      ) : kind === "json" ? (
        <Field
          label="Value"
          htmlFor="variable-value"
          hint='A list, such as ["10.20.0.53", "10.20.0.54"], or an object.'
          error={errors["/value"]}
        >
          <Textarea
            id="variable-value"
            className="font-mono"
            rows={6}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </Field>
      ) : (
        <Field label="Value" htmlFor="variable-value" error={errors["/value"]}>
          <Input
            id="variable-value"
            className={kind === "text" ? "font-mono" : undefined}
            inputMode={kind === "number" ? "decimal" : undefined}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </Field>
      )}
      <Field label="Description" htmlFor="variable-description" error={errors["/description"]}>
        <Input
          id="variable-description"
          value={description}
          maxLength={500}
          onChange={(e) => setDescription(e.target.value)}
        />
      </Field>
    </Sheet>
  );
}

const columns: Column<Variable>[] = [
  {
    id: "name",
    header: "Variable",
    sortValue: (v) => v.key,
    cell: (v) => (
      <>
        <div className="font-mono">{refOf(v)}</div>
        {v.description && <div className="text-caption text-muted">{v.description}</div>}
      </>
    ),
  },
  {
    id: "value",
    header: "Value",
    cell: (v) => (
      <>
        <div className="font-mono break-all">{shown(v.value)}</div>
        <div className="text-caption text-muted">{kindLabel(v.value)}</div>
      </>
    ),
  },
  {
    id: "used",
    header: "Read by",
    sortValue: (v) => v.used_by.length,
    cell: (v) => (
      <span className="text-secondary text-muted">{v.used_by.length ? v.used_by.join(", ") : "Nothing"}</span>
    ),
  },
  {
    id: "changed",
    header: "Changed",
    sortValue: (v) => v.updated_at,
    cell: (v) => (
      <span className="text-secondary text-muted">
        {relative(v.updated_at)}
        {v.updated_by && ` by ${v.updated_by}`}
      </span>
    ),
  },
];

export function VariablesPage() {
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  // Auditors read them (extensibility.read); changing one is the catalog's (ADR-0057).
  const canEdit = can(me, "catalog.manage");
  const variables = useQuery(adm.variables());
  const [editing, setEditing] = useState<Variable | "new" | null>(null);
  const [removing, setRemoving] = useState<Variable | null>(null);
  const remove = useMutation({
    mutationFn: (v: Variable) => call(api.DELETE("/admin/variables/{key}", { params: { path: { key: v.key } } })),
    onSuccess: (_, v) => {
      void qc.invalidateQueries({ queryKey: adm.variables().queryKey });
      toast.success(`${refOf(v)} removed`);
      setRemoving(null);
    },
    onError: (e) => {
      void qc.invalidateQueries({ queryKey: adm.variables().queryKey });
      setRemoving(null);
      failed(e, "The variable wasn't removed");
    },
  });
  const list = variables.data?.items;
  const add = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      New variable
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Variables"
        description={
          <>
            Values that aren't secret and that several items share, such as a site&apos;s DNS servers. Composite steps
            and conditions read them as <code className="font-mono">vars.&lt;group&gt;.&lt;name&gt;</code> each time
            they run. Store passwords and tokens as secrets instead.
          </>
        }
        actions={list?.length ? add : undefined}
      />
      <Panel title="Shared variables" flush>
        <DataTable
          label="Variables"
          columns={columns}
          rows={list}
          rowKey={(v) => v.key}
          minWidth={760}
          loading={variables.isPending}
          error={variables.isError && <ErrorState error={variables.error} onRetry={() => void variables.refetch()} />}
          empty={
            <EmptyState
              icon={VariableIcon}
              title="No variables yet"
              body={
                canEdit
                  ? "Add a value several items share, such as a site's DNS servers, so that each reads it from one place."
                  : "No value is shared between items yet."
              }
              action={add || undefined}
            />
          }
          rowMenuLabel={(v) => `Actions for ${refOf(v)}`}
          rowMenu={
            canEdit
              ? (v) => (
                  <>
                    <MenuItem onSelect={() => setEditing(v)}>Change the value</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger disabled={v.used_by.length > 0} onSelect={() => setRemoving(v)}>
                      {v.used_by.length > 0 ? "Remove: in use" : "Remove"}
                    </MenuItem>
                  </>
                )
              : undefined
          }
        />
      </Panel>
      {editing && <VariableSheet existing={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${refOf(removing)}?`}
          description="Steps can no longer read it. Adding a variable of the same name later brings its use back."
          consequences="No item reads it today."
          confirmLabel="Remove variable"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Page>
  );
}
