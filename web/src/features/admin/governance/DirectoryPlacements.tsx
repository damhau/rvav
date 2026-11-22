// Where VMs' computer objects go in Active Directory (ADR-0064): the OUs and
// groups of a project, of an environment in every project, or of both.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderTree } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors } from "./common";

type S = components["schemas"];

const ANY = "";

/** Where a row applies, in words. */
function scope(r: S["AdPlacement"], inProject: boolean): string {
  const env = r.environment ? `in ${r.environment}` : "in any environment";
  if (r.project_id)
    return inProject ? (r.environment ? `In ${r.environment}` : "Any environment") : `${r.project_name} ${env}`;
  return r.environment ? `Every project in ${r.environment}` : "Every project, any environment";
}

function PlacementSheet({
  project,
  environments,
  row,
  onClose,
}: {
  project?: S["AdminProject"];
  environments: string[];
  row?: S["AdPlacement"];
  onClose: () => void;
}) {
  const qc = useQueryClient();
  const [f, setF] = useState({
    environment: row?.environment ?? ANY,
    ou: row?.ou ?? "",
    groups: row?.groups.join("\n") ?? "",
    allow: row?.allow_blueprint_ou ?? false,
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const body: S["AdPlacementWrite"] = {
        project_id: project?.id ?? null,
        environment: f.environment || null,
        ou: f.ou.trim(),
        groups: f.groups
          .split("\n")
          .map((g) => g.trim())
          .filter(Boolean),
        allow_blueprint_ou: f.allow,
      };
      return row
        ? call(api.PUT("/admin/ad-placements/{id}", { params: { path: { id: row.id } }, body }))
        : call(api.POST("/admin/ad-placements", { body }));
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "ad-placements"] });
      toast.success("OU saved");
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const groupErrors = Object.entries(errors)
    .filter(([k]) => k.startsWith("/groups"))
    .map(([, v]) => v);
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={`${row ? "Change the" : "New"} Active Directory OU${project ? ` for ${project.name}` : ""}`}
      description="DNs, or DNs relative to the integration's base DN, such as OU=Servers,OU=Web."
      submitLabel={save.isPending ? "Saving…" : "Save OU"}
      submitDisabled={!f.ou.trim()}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label="Environment" htmlFor="adp-environment" error={errorAt(errors, "/environment")}>
        <Select
          id="adp-environment"
          value={f.environment}
          onChange={(environment) => setF({ ...f, environment })}
          options={[{ value: ANY, label: "Any environment" }, ...environments.map((e) => ({ value: e, label: e }))]}
        />
      </Field>
      <Field
        label="OU"
        htmlFor="adp-ou"
        hint="Where the VMs' computer objects are made."
        error={errorAt(errors, "/ou")}
      >
        <Input
          id="adp-ou"
          className="font-mono text-secondary"
          placeholder="OU=Servers,OU=Web"
          value={f.ou}
          onChange={(e) => setF({ ...f, ou: e.target.value })}
        />
      </Field>
      <Field
        label="Groups"
        htmlFor="adp-groups"
        hint="One DN a line. The objects join them once made."
        error={groupErrors.join(" ") || undefined}
      >
        <Textarea
          id="adp-groups"
          rows={3}
          className="font-mono text-secondary"
          placeholder="CN=Web servers,OU=Groups"
          value={f.groups}
          onChange={(e) => setF({ ...f, groups: e.target.value })}
        />
      </Field>
      <label htmlFor="adp-allow" className="flex items-start gap-2 text-body">
        <Checkbox id="adp-allow" checked={f.allow} onChange={(allow) => setF({ ...f, allow })} className="mt-0.5" />
        <span>
          Blueprints may choose their OU
          <span className="block text-secondary text-muted">
            A blueprint&apos;s active_directory names another OU, or none. Otherwise it is ignored.
          </span>
        </span>
      </label>
    </Sheet>
  );
}

/**
 * The OUs of a project's VMs, with those set for every project; or, with
 * no project, those set for every project.
 */
export function DirectoryPlacements({ project, canManage }: { project?: S["AdminProject"]; canManage: boolean }) {
  const qc = useQueryClient();
  const rows = useQuery(adm.adPlacements(project?.id));
  const envs = useQuery({ ...adm.environments(), enabled: !project });
  const [editing, setEditing] = useState<S["AdPlacement"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["AdPlacement"] | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/ad-placements/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "ad-placements"] });
      toast.success("OU removed");
      setRemoving(null);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const mine = (r: S["AdPlacement"]) => (project ? r.project_id === project.id : r.project_id === null);
  const shown = rows.data?.items.filter((r) => (project ? true : r.project_id === null));
  const environments = project ? project.environments : (envs.data?.items.map((e) => e.key) ?? []);
  const columns: Column<S["AdPlacement"]>[] = [
    {
      id: "scope",
      header: "Applies to",
      cell: (r) => (
        <div className="flex flex-col">
          <span>{scope(r, Boolean(project))}</span>
          {project && !mine(r) && <span className="text-caption text-faint">Set in Projects › Environments</span>}
        </div>
      ),
    },
    {
      id: "ou",
      header: "OU",
      cell: (r) => <span className="font-mono text-secondary break-all">{r.ou_dn}</span>,
    },
    {
      id: "groups",
      header: "Groups",
      cell: (r) =>
        r.groups.length ? (
          <ul className="flex flex-col font-mono text-secondary break-all">
            {r.groups.map((g) => (
              <li key={g}>{g}</li>
            ))}
          </ul>
        ) : (
          <span className="text-muted">None</span>
        ),
    },
    {
      id: "blueprints",
      header: "Blueprints",
      cell: (r) => (
        <span className="text-secondary text-muted">
          {r.allow_blueprint_ou ? "May choose their OU" : "Use this OU"}
        </span>
      ),
    },
  ];
  const add = canManage && (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      New OU
    </Button>
  );
  return (
    <Panel
      title="Active Directory"
      description={
        project
          ? `The OU where each VM of ${project.name} gets its computer object before it is built: the row for its environment, else the one for any environment, else one set for every project.`
          : "OUs for every project's VMs, by environment. A project's own OU, on its Placement tab, comes first."
      }
      flush
      actions={shown?.length ? add : undefined}
    >
      <DataTable
        label={project ? `Active Directory OUs of ${project.name}` : "Active Directory OUs of every project"}
        columns={columns}
        rows={shown}
        rowKey={(r) => r.id}
        minWidth={760}
        loading={rows.isPending}
        error={rows.isError && <ErrorState error={rows.error} onRetry={() => void rows.refetch()} />}
        empty={
          <EmptyState
            icon={FolderTree}
            title="No OU"
            body={
              project
                ? `VMs of ${project.name} get no computer object in Active Directory.`
                : "VMs get a computer object only where a project has an OU of its own."
            }
            action={add || undefined}
          />
        }
        rowMenuLabel={(r) => `Actions for the OU ${r.ou_dn}`}
        rowMenu={
          canManage
            ? (r) =>
                mine(r) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(r)}>Change</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(r)}>
                      Remove
                    </MenuItem>
                  </>
                ) : (
                  <MenuItem disabled onSelect={() => undefined}>
                    Change it in Projects › Environments
                  </MenuItem>
                )
            : undefined
        }
      />
      {editing && (
        <PlacementSheet
          project={project}
          environments={environments}
          row={editing === "new" ? undefined : editing}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={`Stop making computer objects in ${removing.ou_dn}?`}
          description={`${scope(removing, Boolean(project))}: new VMs get the OU that applies next, or none.`}
          consequences="Objects already made stay, and are deleted with their VMs."
          confirmLabel="Remove OU"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.id)}
        />
      )}
    </Panel>
  );
}
