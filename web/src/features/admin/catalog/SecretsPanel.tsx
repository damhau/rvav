// Stored secrets for REST value sources and calls, Windows guests, cloud-init
// and AWX steps (ADR-0019, ADR-0033, ADR-0038): catalog admins add and
// replace them, global or a project's own; their values are never shown
// again.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { keys } from "@/lib/query";
import { relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";

type Secret = components["schemas"]["Secret"];
type SecretWrite = components["schemas"]["SecretWrite"];
type ProjectRef = NonNullable<components["schemas"]["SecretList"]["projects"]>[number];

const secretsKey = [...keys.catalogAdmin.all, "secrets"] as const;
const kinds = ["token", "basic", "header"] as const;
const tr = i18n.getFixedT(null, "admin-catalog");
const kindLabel = (kind: Secret["kind"]) => tr(`secrets.kind.${kind}`);
// Radix's select has no empty value: the global scope stands as this.
const global = "*";
const scopeQuery = (s: Secret) => (s.project_id ? { project_id: s.project_id } : {});

function SecretSheet({
  existing,
  projects,
  onClose,
}: {
  existing?: Secret;
  projects: ProjectRef[];
  onClose: () => void;
}) {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const [w, setW] = useState<SecretWrite>({
    name: existing?.name ?? "",
    kind: existing?.kind ?? "token",
    description: existing?.description ?? "",
    username: existing?.username ?? "",
    header: existing?.header ?? "",
    value: "",
  });
  const [scope, setScope] = useState(existing?.project_id ?? global);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body: SecretWrite = {
        name: w.name,
        kind: w.kind,
        description: w.description,
        value: w.value,
        ...(w.kind === "basic" ? { username: w.username } : {}),
        ...(w.kind === "header" ? { header: w.header } : {}),
      };
      return existing
        ? call(
            api.PUT("/admin/secrets/{name}", {
              params: { path: { name: existing.name }, query: scopeQuery(existing) },
              body,
            }),
          )
        : call(api.POST("/admin/secrets", { body: { ...body, ...(scope !== global ? { project_id: scope } : {}) } }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: secretsKey });
      toast.success(existing ? t("secrets.replaced", { name: w.name }) : t("secrets.stored", { name: w.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("secrets.notStored"));
    },
  });
  const set = (k: keyof SecretWrite) => (v: string) => setW((x) => ({ ...x, [k]: v }));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={existing ? t("secrets.replaceTitle", { name: existing.name }) : t("secrets.new")}
      description={t("secrets.sheetDescription", { userData: '{{ secret "name" }}', awx: "${secret.name}" })}
      submitLabel={save.isPending ? t("secrets.storing") : existing ? t("secrets.replace") : t("secrets.store")}
      submitDisabled={!w.name || !w.value}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label={t("secrets.name")} htmlFor="secret-name" hint={t("secrets.nameHint")} error={errors["/name"]}>
        <Input
          id="secret-name"
          className="font-mono"
          value={w.name}
          disabled={Boolean(existing)}
          onChange={(e) => set("name")(e.target.value)}
        />
      </Field>
      <Field
        label={t("secrets.project")}
        htmlFor="secret-project"
        hint={t("secrets.projectHint")}
        error={errors["/project_id"]}
      >
        <Select
          id="secret-project"
          value={scope}
          disabled={Boolean(existing)}
          onChange={setScope}
          options={[
            { value: global, label: t("secrets.everyProjectGlobal") },
            ...projects.map((p) => ({ value: p.id, label: p.name, meta: p.code })),
          ]}
        />
      </Field>
      <Field label={t("secrets.kindLabel")} htmlFor="secret-kind">
        <Select
          id="secret-kind"
          value={w.kind}
          onChange={set("kind")}
          options={kinds.map((value) => ({ value, label: kindLabel(value) }))}
        />
      </Field>
      {w.kind === "basic" && (
        <Field label={t("secrets.username")} htmlFor="secret-username" error={errors["/username"]}>
          <Input id="secret-username" value={w.username} onChange={(e) => set("username")(e.target.value)} />
        </Field>
      )}
      {w.kind === "header" && (
        <Field
          label={t("secrets.header")}
          htmlFor="secret-header"
          hint={t("secrets.headerHint")}
          error={errors["/header"]}
        >
          <Input id="secret-header" value={w.header} onChange={(e) => set("header")(e.target.value)} />
        </Field>
      )}
      <Field
        label={w.kind === "basic" ? t("secrets.password") : t("secrets.value")}
        htmlFor="secret-value"
        hint={existing ? t("secrets.valueHint") : undefined}
        error={errors["/value"]}
      >
        <Input
          id="secret-value"
          type="password"
          autoComplete="new-password"
          value={w.value}
          onChange={(e) => set("value")(e.target.value)}
        />
      </Field>
      <Field label={t("secrets.description")} htmlFor="secret-description">
        <Input
          id="secret-description"
          value={w.description}
          maxLength={200}
          onChange={(e) => set("description")(e.target.value)}
        />
      </Field>
    </Sheet>
  );
}

const usedBy = (s: Secret) => s.used_by ?? [];
const secretKey = (s: Secret) => `${s.project_id ?? ""}/${s.name}`;

export function SecretsPanel() {
  const { t } = useTranslation("admin-catalog");
  const qc = useQueryClient();
  const secrets = useQuery({
    queryKey: secretsKey,
    queryFn: ({ signal }) => call(api.GET("/admin/secrets", { signal })),
  });
  const [editing, setEditing] = useState<Secret | "new" | null>(null);
  const [removing, setRemoving] = useState<Secret | null>(null);
  const remove = useMutation({
    mutationFn: (s: Secret) =>
      call(api.DELETE("/admin/secrets/{name}", { params: { path: { name: s.name }, query: scopeQuery(s) } })),
    onSuccess: (_, s) => {
      void qc.invalidateQueries({ queryKey: secretsKey });
      toast.success(t("secrets.removed", { name: s.name }));
      setRemoving(null);
    },
    onError: (e) => failed(e, t("secrets.notRemoved")),
  });
  const list = secrets.data?.items;
  const projects = secrets.data?.projects ?? [];
  const projectName = (id: string) => projects.find((p) => p.id === id)?.name ?? id;
  // A project's own secret, while a global one of its name remains, may go even if used.
  const shadows = (s: Secret) =>
    Boolean(s.project_id) && Boolean(list?.some((x) => x.name === s.name && !x.project_id));
  const inUse = (s: Secret) => usedBy(s).length > 0 && !shadows(s);
  const columns: Column<Secret>[] = [
    {
      id: "name",
      header: t("secrets.name"),
      sortValue: (s) => s.name,
      cell: (s) => (
        <>
          <div className="font-mono">{s.name}</div>
          {s.description && <div className="text-caption text-muted">{s.description}</div>}
        </>
      ),
    },
    {
      id: "project",
      header: t("secrets.project"),
      sortValue: (s) => (s.project_id ? projectName(s.project_id) : ""),
      cell: (s) => (
        <>
          <div className="text-secondary text-muted">
            {s.project_id ? projectName(s.project_id) : t("secrets.everyProject")}
          </div>
          {shadows(s) && <div className="text-caption text-muted">{t("secrets.shadows")}</div>}
        </>
      ),
    },
    {
      id: "kind",
      header: t("secrets.kindLabel"),
      sortValue: (s) => kindLabel(s.kind),
      cell: (s) => <span className="text-secondary text-muted">{kindLabel(s.kind)}</span>,
    },
    {
      id: "used",
      header: t("secrets.usedBy"),
      sortValue: (s) => usedBy(s).length,
      cell: (s) => (
        <span className="text-secondary text-muted">
          {usedBy(s).length ? usedBy(s).join(", ") : t("secrets.nothing")}
        </span>
      ),
    },
    {
      id: "changed",
      header: t("secrets.changed"),
      sortValue: (s) => s.updated_at,
      cell: (s) => <span className="text-secondary text-muted">{relative(s.updated_at)}</span>,
    },
  ];
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("secrets.new")}
    </Button>
  );
  return (
    <Panel
      title={t("secrets.title")}
      description={t("secrets.panelDescription")}
      flush
      actions={list?.length ? add : undefined}
    >
      <DataTable
        label={t("secrets.title")}
        columns={columns}
        rows={list}
        rowKey={secretKey}
        minWidth={760}
        loading={secrets.isPending}
        error={secrets.isError && <ErrorState error={secrets.error} onRetry={() => void secrets.refetch()} />}
        empty={<EmptyState icon={KeyRound} title={t("secrets.empty")} body={t("secrets.emptyBody")} action={add} />}
        rowMenuLabel={(s) =>
          s.project_id
            ? t("secrets.actionsForOf", { name: s.name, project: projectName(s.project_id) })
            : t("secrets.actionsFor", { name: s.name })
        }
        rowMenu={(s) => (
          <>
            <MenuItem onSelect={() => setEditing(s)}>{t("secrets.replaceValue")}</MenuItem>
            <MenuSeparator />
            <MenuItem danger disabled={inUse(s)} onSelect={() => setRemoving(s)}>
              {inUse(s) ? t("secrets.removeInUse") : t("secrets.remove")}
            </MenuItem>
          </>
        )}
      />
      {editing && (
        <SecretSheet
          existing={editing === "new" ? undefined : editing}
          projects={projects}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={
            removing.project_id
              ? t("secrets.removeTitleOf", { name: removing.name, project: projectName(removing.project_id) })
              : t("secrets.removeTitle", { name: removing.name })
          }
          description={t("secrets.removeBody")}
          consequences={
            shadows(removing)
              ? t("secrets.removeShadows", { project: projectName(removing.project_id ?? ""), name: removing.name })
              : t("secrets.removeUnused")
          }
          confirmLabel={t("secrets.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Panel>
  );
}
