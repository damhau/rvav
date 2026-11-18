// Stored secrets for REST value sources and calls, Windows guests, cloud-init
// and AWX steps (ADR-0019, ADR-0033, ADR-0038): catalog admins add and
// replace them, global or a project's own; their values are never shown
// again.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { useState } from "react";
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
import { relative } from "@/lib/utils";

type Secret = components["schemas"]["Secret"];
type SecretWrite = components["schemas"]["SecretWrite"];
type ProjectRef = NonNullable<components["schemas"]["SecretList"]["projects"]>[number];

const secretsKey = [...keys.catalogAdmin.all, "secrets"] as const;
const kindLabel = { token: "Bearer token", basic: "Basic (user and password)", header: "Header" } as const;
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
      toast.success(existing ? `${w.name} replaced` : `${w.name} stored`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The secret wasn't stored");
    },
  });
  const set = (k: keyof SecretWrite) => (v: string) => setW((x) => ({ ...x, [k]: v }));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={existing ? `Replace ${existing.name}` : "New secret"}
      description='Stored encrypted. Its value is never shown again. A REST value source or call names it, cloud-init user data reads it as {{ secret "name" }}, an AWX step as ${secret.name}.'
      submitLabel={save.isPending ? "Storing…" : existing ? "Replace" : "Store"}
      submitDisabled={!w.name || !w.value || save.isPending}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label="Name" htmlFor="secret-name" hint="Lower case letters, digits and dashes." error={errors["/name"]}>
        <Input
          id="secret-name"
          className="font-mono"
          value={w.name}
          disabled={Boolean(existing)}
          onChange={(e) => set("name")(e.target.value)}
        />
      </Field>
      <Field
        label="Project"
        htmlFor="secret-project"
        hint="A project's own secret is opened by its requests rather than the global one of the same name. Other projects never open it."
        error={errors["/project_id"]}
      >
        <Select
          id="secret-project"
          value={scope}
          disabled={Boolean(existing)}
          onChange={setScope}
          options={[
            { value: global, label: "Every project (global)" },
            ...projects.map((p) => ({ value: p.id, label: p.name, meta: p.code })),
          ]}
        />
      </Field>
      <Field label="Kind" htmlFor="secret-kind">
        <Select
          id="secret-kind"
          value={w.kind}
          onChange={set("kind")}
          options={Object.entries(kindLabel).map(([value, label]) => ({ value, label }))}
        />
      </Field>
      {w.kind === "basic" && (
        <Field label="User name" htmlFor="secret-username" error={errors["/username"]}>
          <Input id="secret-username" value={w.username} onChange={(e) => set("username")(e.target.value)} />
        </Field>
      )}
      {w.kind === "header" && (
        <Field label="Header" htmlFor="secret-header" hint="For example X-API-Key." error={errors["/header"]}>
          <Input id="secret-header" value={w.header} onChange={(e) => set("header")(e.target.value)} />
        </Field>
      )}
      <Field
        label={w.kind === "basic" ? "Password" : "Value"}
        htmlFor="secret-value"
        hint={existing ? "The new value replaces the stored one at once, in every item that uses it." : undefined}
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
      <Field label="Description" htmlFor="secret-description">
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
      toast.success(`${s.name} removed`);
      setRemoving(null);
    },
    onError: (e) => failed(e, "The secret wasn't removed"),
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
      header: "Name",
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
      header: "Project",
      sortValue: (s) => (s.project_id ? projectName(s.project_id) : ""),
      cell: (s) => (
        <span className="text-secondary text-muted">{s.project_id ? projectName(s.project_id) : "Every project"}</span>
      ),
    },
    {
      id: "kind",
      header: "Kind",
      sortValue: (s) => kindLabel[s.kind],
      cell: (s) => <span className="text-secondary text-muted">{kindLabel[s.kind]}</span>,
    },
    {
      id: "used",
      header: "Used by",
      sortValue: (s) => usedBy(s).length,
      cell: (s) => (
        <span className="text-secondary text-muted">{usedBy(s).length ? usedBy(s).join(", ") : "Nothing"}</span>
      ),
    },
    {
      id: "changed",
      header: "Changed",
      sortValue: (s) => s.updated_at,
      cell: (s) => <span className="text-secondary text-muted">{relative(s.updated_at)}</span>,
    },
  ];
  const add = (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New secret
    </Button>
  );
  return (
    <Panel
      title="Secrets"
      description="What REST value sources and calls sign in with, and what Windows guests, cloud-init and AWX steps are given."
      flush
      actions={list?.length ? add : undefined}
    >
      <DataTable
        label="Secrets"
        columns={columns}
        rows={list}
        rowKey={secretKey}
        minWidth={760}
        loading={secrets.isPending}
        error={secrets.isError && <ErrorState error={secrets.error} onRetry={() => void secrets.refetch()} />}
        empty={
          <EmptyState
            icon={KeyRound}
            title="No secrets yet"
            body="Store the token or password a REST call, a guest or an AWX step needs; its value is never shown again."
            action={add}
          />
        }
        rowMenuLabel={(s) => `Actions for ${s.name}${s.project_id ? ` of ${projectName(s.project_id)}` : ""}`}
        rowMenu={(s) => (
          <>
            <MenuItem onSelect={() => setEditing(s)}>Replace the value</MenuItem>
            <MenuSeparator />
            <MenuItem danger disabled={inUse(s)} onSelect={() => setRemoving(s)}>
              {inUse(s) ? "Remove: in use" : "Remove"}
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
              ? `Remove ${removing.name} of ${projectName(removing.project_id)}?`
              : `Remove ${removing.name}?`
          }
          description="Its value is deleted. Storing a secret of the same name later doesn't bring it back."
          consequences={
            shadows(removing)
              ? `${projectName(removing.project_id ?? "")}'s requests open the global ${removing.name} instead.`
              : "No item or template uses it today."
          }
          confirmLabel="Remove secret"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Panel>
  );
}
