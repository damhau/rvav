// Shared variables (ADR-0057), as vRO's configuration elements: named values
// that aren't secret, which catalog admins keep and composite steps and
// conditions read as vars.<group>.<name> each time they run.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Variable as VariableIcon } from "lucide-react";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { i18n } from "@/lib/i18n";

type Kind = "text" | "number" | "boolean" | "json";

const tr = i18n.getFixedT(null, "admin-catalog");

const kinds = (): { value: Kind; label: string }[] =>
  (["text", "number", "boolean", "json"] as const).map((value) => ({ value, label: tr(`variables.kind.${value}`) }));

// Code placeholders, the same in every language.
const groupSlot = "<group>";
const nameSlot = "<name>";
const listExample = '["10.20.0.53", "10.20.0.54"]';

/** Code inside a sentence: Trans leaves its text alone. */
const Expr = ({ text }: { text: string }) => <code className="font-mono">{text}</code>;

function kindOf(v: unknown): Kind {
  if (typeof v === "number") return "number";
  if (typeof v === "boolean") return "boolean";
  if (typeof v === "string") return "text";
  return "json";
}

const kindLabel = (v: unknown) => {
  const k = kindOf(v);
  return tr(Array.isArray(v) ? "variables.kind.list" : k === "json" ? "variables.kind.object" : `variables.kind.${k}`);
};

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
      return text.trim() !== "" && Number.isFinite(n) ? { value: n } : { error: tr("variables.error.number") };
    }
    case "boolean":
      return { value: text === "true" };
    case "json":
      try {
        const v: unknown = JSON.parse(text);
        if (v === null || typeof v !== "object") return { error: tr("variables.error.json") };
        return { value: v };
      } catch (e) {
        return {
          error: tr("variables.error.notJson", {
            reason: e instanceof Error ? e.message : tr("variables.error.unreadable"),
          }),
        };
      }
  }
}

const refOf = (v: Pick<Variable, "group" | "name">) => `vars.${v.group}.${v.name}`;

function VariableSheet({ existing, onClose }: { existing?: Variable; onClose: () => void }) {
  const { t } = useTranslation("admin-catalog");
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
      toast.success(existing ? t("variables.saved", { ref: refOf(v) }) : t("variables.added", { ref: refOf(v) }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("variables.notSaved"));
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
      title={existing ? t("variables.change", { ref: refOf(existing) }) : t("variables.new")}
      description={t("variables.sheetDescription")}
      submitLabel={save.isPending ? t("variables.saving") : existing ? t("variables.save") : t("variables.add")}
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
        label={t("variables.group")}
        htmlFor="variable-group"
        hint={t("variables.groupHint")}
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
        label={t("variables.name")}
        htmlFor="variable-name"
        hint={
          <Trans
            t={t}
            i18nKey="variables.readAs"
            components={{ expr: <Expr text={`vars.${group || groupSlot}.${name || nameSlot}`} /> }}
          />
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
      <Field label={t("variables.type")} htmlFor="variable-kind">
        <Select id="variable-kind" value={kind} onChange={changeKind} options={kinds()} />
      </Field>
      {kind === "boolean" ? (
        <Field label={t("variables.value")} htmlFor="variable-value" error={errors["/value"]}>
          <Select
            id="variable-value"
            value={text === "false" ? "false" : "true"}
            onChange={setText}
            options={[
              { value: "true", label: t("variables.true") },
              { value: "false", label: t("variables.false") },
            ]}
          />
        </Field>
      ) : kind === "json" ? (
        <Field
          label={t("variables.value")}
          htmlFor="variable-value"
          hint={t("variables.jsonHint", { example: listExample })}
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
        <Field label={t("variables.value")} htmlFor="variable-value" error={errors["/value"]}>
          <Input
            id="variable-value"
            className={kind === "text" ? "font-mono" : undefined}
            inputMode={kind === "number" ? "decimal" : undefined}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </Field>
      )}
      <Field label={t("variables.description")} htmlFor="variable-description" error={errors["/description"]}>
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

const columns = (): Column<Variable>[] => [
  {
    id: "name",
    header: tr("variables.col.variable"),
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
    header: tr("variables.col.value"),
    cell: (v) => (
      <>
        <div className="font-mono break-all">{shown(v.value)}</div>
        <div className="text-caption text-muted">{kindLabel(v.value)}</div>
      </>
    ),
  },
  {
    id: "used",
    header: tr("variables.col.readBy"),
    sortValue: (v) => v.used_by.length,
    cell: (v) => (
      <span className="text-secondary text-muted">
        {v.used_by.length ? v.used_by.join(", ") : tr("variables.nothing")}
      </span>
    ),
  },
  {
    id: "changed",
    header: tr("variables.col.changed"),
    sortValue: (v) => v.updated_at,
    cell: (v) => (
      <span className="text-secondary text-muted">
        {relative(v.updated_at)}
        {v.updated_by && tr("variables.changedBy", { name: v.updated_by })}
      </span>
    ),
  },
];

export function VariablesPage() {
  const { t } = useTranslation("admin-catalog");
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
      toast.success(t("variables.removed", { ref: refOf(v) }));
      setRemoving(null);
    },
    onError: (e) => {
      void qc.invalidateQueries({ queryKey: adm.variables().queryKey });
      setRemoving(null);
      failed(e, t("variables.notRemoved"));
    },
  });
  const list = variables.data?.items;
  const add = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      {t("variables.new")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("variables.title")}
        description={
          <Trans
            t={t}
            i18nKey="variables.lead"
            components={{ expr: <Expr text={`vars.${groupSlot}.${nameSlot}`} /> }}
          />
        }
        actions={list?.length ? add : undefined}
      />
      <Panel title={t("variables.shared")} flush>
        <DataTable
          label={t("variables.title")}
          columns={columns()}
          rows={list}
          rowKey={(v) => v.key}
          minWidth={760}
          loading={variables.isPending}
          error={variables.isError && <ErrorState error={variables.error} onRetry={() => void variables.refetch()} />}
          empty={
            <EmptyState
              icon={VariableIcon}
              title={t("variables.empty")}
              body={canEdit ? t("variables.emptyEdit") : t("variables.emptyRead")}
              action={add || undefined}
            />
          }
          rowMenuLabel={(v) => t("variables.actionsFor", { ref: refOf(v) })}
          rowMenu={
            canEdit
              ? (v) => (
                  <>
                    <MenuItem onSelect={() => setEditing(v)}>{t("variables.changeValue")}</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger disabled={v.used_by.length > 0} onSelect={() => setRemoving(v)}>
                      {v.used_by.length > 0 ? t("variables.removeInUse") : t("variables.remove")}
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
          title={t("variables.removeTitle", { ref: refOf(removing) })}
          description={t("variables.removeBody")}
          consequences={t("variables.removeConsequences")}
          confirmLabel={t("variables.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Page>
  );
}
