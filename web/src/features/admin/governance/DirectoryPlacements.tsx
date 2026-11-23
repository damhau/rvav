// Where VMs' computer objects go in Active Directory (ADR-0064): the OUs and
// groups of a project, of an environment in every project, or of both.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FolderTree } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Button } from "@/components/ui/button";
import { CheckField } from "@/components/ui/checkbox";
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
import { useEnvName } from "@/lib/environments";
import { adm } from "@/lib/query";
import { errorAt, failed, fieldErrors } from "./common";

type S = components["schemas"];

// Radix Select takes "" for no value at all: "*" stands for any environment,
// which no environment's key can be.
const ANY = "*";
const ouExample = "OU=Servers,OU=Web";
const groupExample = "CN=Web servers,OU=Groups";

/** Where a row applies, in words. */
function scope(
  t: TFunction<"admin-governance">,
  r: S["AdPlacement"],
  inProject: boolean,
  envName: (key: string) => string,
): string {
  const environment = r.environment && envName(r.environment);
  if (r.project_id && inProject)
    return environment ? t("placements.scope.inEnvironment", { environment }) : t("placements.scope.anyEnvironment");
  if (r.project_id)
    return environment
      ? t("placements.scope.projectIn", { project: r.project_name, environment })
      : t("placements.scope.projectAny", { project: r.project_name });
  return environment ? t("placements.scope.everyIn", { environment }) : t("placements.scope.everyAny");
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
  const { t } = useTranslation("admin-governance");
  const envName = useEnvName();
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
        environment: f.environment === ANY ? null : f.environment,
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
      toast.success(t("placements.saved"));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("projects.notSaved"));
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
      title={
        project
          ? t(row ? "placements.changeFor" : "placements.newFor", { project: project.name })
          : t(row ? "placements.change" : "placements.new")
      }
      description={t("placements.sheetLead")}
      submitLabel={save.isPending ? t("projects.saving") : t(row ? "placements.save" : "placements.create")}
      submitDisabled={!f.ou.trim()}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field label={t("placements.environment")} htmlFor="adp-environment" error={errorAt(errors, "/environment")}>
        <Select
          id="adp-environment"
          value={f.environment}
          onChange={(environment) => setF({ ...f, environment })}
          options={[
            { value: ANY, label: t("placements.scope.anyEnvironment") },
            ...environments.map((e) => ({ value: e, label: envName(e) })),
          ]}
        />
      </Field>
      <Field label={t("placements.ou")} htmlFor="adp-ou" hint={t("placements.ouHint")} error={errorAt(errors, "/ou")}>
        <Input
          id="adp-ou"
          className="font-mono text-secondary"
          placeholder={ouExample}
          value={f.ou}
          onChange={(e) => setF({ ...f, ou: e.target.value })}
        />
      </Field>
      <Field
        label={t("placements.groups")}
        htmlFor="adp-groups"
        hint={t("placements.groupsHint")}
        error={groupErrors.join(" ") || undefined}
      >
        <Textarea
          id="adp-groups"
          rows={3}
          className="font-mono text-secondary"
          placeholder={groupExample}
          value={f.groups}
          onChange={(e) => setF({ ...f, groups: e.target.value })}
        />
      </Field>
      <CheckField
        id="adp-allow"
        label={t("placements.allow")}
        hint={t("placements.allowNote")}
        checked={f.allow}
        onChange={(allow) => setF({ ...f, allow })}
      />
    </Sheet>
  );
}

/**
 * The OUs of a project's VMs, with those set for every project; or, with
 * no project, those set for every project.
 */
export function DirectoryPlacements({ project, canManage }: { project?: S["AdminProject"]; canManage: boolean }) {
  const { t } = useTranslation("admin-governance");
  const qc = useQueryClient();
  const rows = useQuery(adm.adPlacements(project?.id));
  const envs = useQuery({ ...adm.environments(), enabled: !project });
  const envName = useEnvName();
  const [editing, setEditing] = useState<S["AdPlacement"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["AdPlacement"] | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/ad-placements/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "ad-placements"] });
      toast.success(t("placements.removed"));
      setRemoving(null);
    },
    onError: (e) => failed(e, t("projects.notRemoved")),
  });
  const mine = (r: S["AdPlacement"]) => (project ? r.project_id === project.id : r.project_id === null);
  const shown = rows.data?.items.filter((r) => (project ? true : r.project_id === null));
  const environments = project ? project.environments : (envs.data?.items.map((e) => e.key) ?? []);
  const columns: Column<S["AdPlacement"]>[] = [
    {
      id: "scope",
      header: t("placements.appliesTo"),
      cell: (r) => (
        <div className="flex flex-col">
          <span>{scope(t, r, Boolean(project), envName)}</span>
          {project && !mine(r) && <span className="text-caption text-faint">{t("placements.setElsewhere")}</span>}
        </div>
      ),
    },
    {
      id: "ou",
      header: t("placements.ou"),
      cell: (r) => <span className="font-mono text-secondary break-all">{r.ou_dn}</span>,
    },
    {
      id: "groups",
      header: t("placements.groups"),
      cell: (r) =>
        r.groups.length ? (
          <ul className="flex flex-col font-mono text-secondary break-all">
            {r.groups.map((g) => (
              <li key={g}>{g}</li>
            ))}
          </ul>
        ) : (
          <span className="text-muted">{t("projects.none")}</span>
        ),
    },
    {
      id: "blueprints",
      header: t("placements.blueprints"),
      cell: (r) => (
        <span className="text-secondary text-muted">
          {r.allow_blueprint_ou ? t("placements.mayChoose") : t("placements.useThis")}
        </span>
      ),
    },
  ];
  const add = canManage && (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("placements.add")}
    </Button>
  );
  return (
    <Panel
      title={t("placements.title")}
      description={project ? t("placements.leadProject", { project: project.name }) : t("placements.leadAll")}
      flush
      actions={shown?.length ? add : undefined}
    >
      <DataTable
        label={project ? t("placements.labelProject", { project: project.name }) : t("placements.labelAll")}
        columns={columns}
        rows={shown}
        rowKey={(r) => r.id}
        minWidth={760}
        loading={rows.isPending}
        error={rows.isError && <ErrorState error={rows.error} onRetry={() => void rows.refetch()} />}
        empty={
          <EmptyState
            icon={FolderTree}
            title={t("placements.emptyTitle")}
            body={project ? t("placements.emptyProject", { project: project.name }) : t("placements.emptyAll")}
            action={add || undefined}
          />
        }
        rowMenuLabel={(r) => t("placements.actionsFor", { ou: r.ou_dn })}
        rowMenu={
          canManage
            ? (r) =>
                mine(r) ? (
                  <>
                    <MenuItem onSelect={() => setEditing(r)}>{t("projects.change")}</MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(r)}>
                      {t("projects.remove")}
                    </MenuItem>
                  </>
                ) : (
                  <MenuItem disabled onSelect={() => undefined}>
                    {t("placements.changeElsewhere")}
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
          title={t("placements.removeTitle", { ou: removing.ou_dn })}
          description={t("placements.removeLead", { scope: scope(t, removing, Boolean(project), envName) })}
          consequences={t("placements.removeConsequences")}
          confirmLabel={t("placements.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.id)}
        />
      )}
    </Panel>
  );
}
