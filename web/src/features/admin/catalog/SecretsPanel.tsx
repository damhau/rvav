// Stored secrets for REST value sources and calls (ADR-0019, ADR-0033):
// catalog admins add and replace them; their values are never shown again.
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

const secretsKey = [...keys.catalogAdmin.all, "secrets"] as const;
const kindLabel = { token: "Bearer token", basic: "Basic (user and password)", header: "Header" } as const;

function SecretSheet({ existing, onClose }: { existing?: Secret; onClose: () => void }) {
  const qc = useQueryClient();
  const [w, setW] = useState<SecretWrite>({
    name: existing?.name ?? "",
    kind: existing?.kind ?? "token",
    description: existing?.description ?? "",
    username: existing?.username ?? "",
    header: existing?.header ?? "",
    value: "",
  });
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
        ? call(api.PUT("/admin/secrets/{name}", { params: { path: { name: existing.name } }, body }))
        : call(api.POST("/admin/secrets", { body }));
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
      description="Stored encrypted. Its value is never shown again; a REST value source or call names it."
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

export function SecretsPanel() {
  const qc = useQueryClient();
  const secrets = useQuery({
    queryKey: secretsKey,
    queryFn: ({ signal }) => call(api.GET("/admin/secrets", { signal })),
  });
  const [editing, setEditing] = useState<Secret | "new" | null>(null);
  const [removing, setRemoving] = useState<Secret | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => call(api.DELETE("/admin/secrets/{name}", { params: { path: { name } } })),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: secretsKey });
      toast.success(`${name} removed`);
      setRemoving(null);
    },
    onError: (e) => failed(e, "The secret wasn't removed"),
  });
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
        <span className="text-secondary text-muted">{usedBy(s).length ? usedBy(s).join(", ") : "No item"}</span>
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
  const list = secrets.data?.items;
  return (
    <Panel
      title="Secrets"
      description="What REST value sources and calls sign in to their service with."
      flush
      actions={list?.length ? add : undefined}
    >
      <DataTable
        label="Secrets"
        columns={columns}
        rows={list}
        rowKey={(s) => s.name}
        minWidth={640}
        loading={secrets.isPending}
        error={secrets.isError && <ErrorState error={secrets.error} onRetry={() => void secrets.refetch()} />}
        empty={
          <EmptyState
            icon={KeyRound}
            title="No secrets yet"
            body="Store the token or password a REST value source or call needs; its value is never shown again."
            action={add}
          />
        }
        rowMenuLabel={(s) => `Actions for ${s.name}`}
        rowMenu={(s) => (
          <>
            <MenuItem onSelect={() => setEditing(s)}>Replace the value</MenuItem>
            <MenuSeparator />
            <MenuItem danger disabled={usedBy(s).length > 0} onSelect={() => setRemoving(s)}>
              {usedBy(s).length > 0 ? "Remove: items use it" : "Remove"}
            </MenuItem>
          </>
        )}
      />
      {editing && <SecretSheet existing={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Remove ${removing.name}?`}
          description="Its value is deleted. Storing a secret of the same name later doesn't bring it back."
          consequences="No item's form or call uses it today."
          confirmLabel="Remove secret"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.name)}
        />
      )}
    </Panel>
  );
}
