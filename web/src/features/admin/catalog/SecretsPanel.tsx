// Stored secrets for REST value sources (ADR-0019): catalog admins add and
// replace them; their values are never shown again.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { keys } from "@/lib/query";
import { relative } from "@/lib/utils";

type Secret = components["schemas"]["Secret"];
type SecretWrite = components["schemas"]["SecretWrite"];

const secretsKey = [...keys.catalogAdmin.all, "secrets"] as const;
const kindLabel = { token: "Bearer token", basic: "Basic (user and password)", header: "Header" } as const;

function SecretDialog({ existing, onClose }: { existing?: Secret; onClose: () => void }) {
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
      if (isApiError(e) && e.fieldErrors.length)
        setErrors(Object.fromEntries(e.fieldErrors.map((f) => [f.field.replace(/^\//, ""), f.message])));
      else toast.error(isApiError(e) ? (e.problem.detail ?? e.problem.title) : "Not stored");
    },
  });
  const set = (k: keyof SecretWrite) => (v: string) => setW((x) => ({ ...x, [k]: v }));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent
        title={existing ? `Replace ${existing.name}` : "New secret"}
        description="Stored encrypted. Its value is never shown again; a REST value source names it."
      >
        <form
          className="flex flex-col gap-3 p-4"
          onSubmit={(e) => {
            e.preventDefault();
            setErrors({});
            save.mutate();
          }}
        >
          <Field label="Name" htmlFor="secret-name" hint={errors.name ?? "Lower case letters, digits and dashes."}>
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
            <Field label="User name" htmlFor="secret-username" hint={errors.username}>
              <Input id="secret-username" value={w.username} onChange={(e) => set("username")(e.target.value)} />
            </Field>
          )}
          {w.kind === "header" && (
            <Field label="Header" htmlFor="secret-header" hint={errors.header ?? "For example X-API-Key."}>
              <Input id="secret-header" value={w.header} onChange={(e) => set("header")(e.target.value)} />
            </Field>
          )}
          <Field label={w.kind === "basic" ? "Password" : "Value"} htmlFor="secret-value" hint={errors.value}>
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
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={!w.name || !w.value || save.isPending}>
              {save.isPending ? "Storing…" : existing ? "Replace" : "Store"}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function SecretsPanel() {
  const qc = useQueryClient();
  const secrets = useQuery({
    queryKey: secretsKey,
    queryFn: ({ signal }) => call(api.GET("/admin/secrets", { signal })),
  });
  const [editing, setEditing] = useState<Secret | "new" | null>(null);
  const remove = useMutation({
    mutationFn: (name: string) => call(api.DELETE("/admin/secrets/{name}", { params: { path: { name } } })),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: secretsKey });
      toast.success(`${name} removed`);
    },
    onError: (e) => toast.error(isApiError(e) ? (e.problem.detail ?? e.problem.title) : "Not removed"),
  });
  const list = secrets.data?.items ?? [];
  return (
    <Panel
      title="Secrets"
      flush
      actions={
        <Button size="sm" onClick={() => setEditing("new")}>
          New secret
        </Button>
      }
    >
      {secrets.isPending && <SkeletonRows rows={2} cols={4} />}
      {secrets.isError && <ErrorState error={secrets.error} onRetry={() => void secrets.refetch()} />}
      {secrets.data && list.length === 0 && (
        <EmptyState title="No secrets" body="REST value sources sign in to their service with a stored secret." />
      )}
      {list.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full min-w-[640px] border-collapse text-[13px]">
            <thead>
              <tr className="label text-left">
                <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                  Name
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Kind
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Used by
                </th>
                <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                  Changed
                </th>
                <th scope="col" className="border-b border-line px-4 py-2">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.map((s) => {
                const used = s.used_by ?? [];
                return (
                  <tr key={s.name} className="hover:bg-sunken">
                    <td className="border-b border-line px-4 py-2">
                      <div className="font-mono text-xs">{s.name}</div>
                      {s.description && <div className="text-[11px] text-faint">{s.description}</div>}
                    </td>
                    <td className="border-b border-line px-3 py-2 text-xs text-muted">{kindLabel[s.kind]}</td>
                    <td className="border-b border-line px-3 py-2 text-xs text-muted">
                      {used.length ? used.join(", ") : "—"}
                    </td>
                    <td className="border-b border-line px-3 py-2 text-xs text-muted">{relative(s.updated_at)}</td>
                    <td className="border-b border-line px-4 py-2 text-right whitespace-nowrap">
                      <Button size="sm" variant="ghost" onClick={() => setEditing(s)}>
                        Replace
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={`Remove ${s.name}`}
                        disabled={used.length > 0 || remove.isPending}
                        title={used.length > 0 ? "Items use it" : undefined}
                        onClick={() => remove.mutate(s.name)}
                      >
                        Remove
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
      {editing && <SecretDialog existing={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
    </Panel>
  );
}
