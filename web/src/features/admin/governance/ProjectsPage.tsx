// Projects, environments and identity provider groups (S9, ADR-0020).
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { FolderKanban, Layers, UsersRound } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CheckField, Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterCheck, FilterEmpty, useAddressFilters } from "@/components/ui/filter-bar";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { UserRef } from "@/lib/api/types";
import { useEnvName } from "@/lib/environments";
import { adm, q } from "@/lib/query";
import { can, projectPagePermissions, projectsWithAny } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { cn } from "@/lib/utils";
import { AdminWarning, Refusal, refused } from "../access/refusal";
import { failed, fieldErrors, UserPicker } from "./common";
import { DirectoryPlacements } from "./DirectoryPlacements";
import { roleName } from "@/lib/i18n/names";
import { formatNumber } from "@/lib/format";

type S = components["schemas"];
type T = TFunction<"admin-governance">;

const keyExample = "dev";

const mappedRole = (m: { role: string; role_name?: string }) =>
  m.role_name ? roleName({ key: m.role, name: m.role_name }) : m.role;
const groupExample = "rvav-web";

export const projectsTabs = ["projects", "environments", "groups"] as const;
export type ProjectsTab = (typeof projectsTabs)[number];

/** The projects list's filters, in the address beside the tab. */
export interface ProjectsSearch {
  q?: string;
  archived?: boolean;
}

/** A project, a member, a mapping: what changes with it is everywhere, /me too. */
function useRefreshAccess() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["admin"] });
    void qc.invalidateQueries({ queryKey: ["me"] });
  };
}

function NewProjectSheet({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshAccess();
  const navigate = useNavigate();
  const envs = useQuery(q.environments());
  const [f, setF] = useState({
    code: "",
    name: "",
    description: "",
    cost_center: "",
    environments: ["dev"] as string[],
  });
  const [admins, setAdmins] = useState<UserRef[]>([]);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => call(api.POST("/admin/projects", { body: { ...f, project_admins: admins.map((a) => a.id) } })),
    onSuccess: (p) => {
      refresh();
      toast.success(t("projects.new.created", { name: p.name }));
      void navigate({ to: "/admin/projects/$projectId", params: { projectId: p.id } });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("projects.new.notCreated"));
    },
  });
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={create.isPending}
      title={t("projects.new.title")}
      description={t("projects.new.lead")}
      submitLabel={create.isPending ? t("projects.new.creating") : t("projects.new.create")}
      submitDisabled={!f.code || !f.name}
      onSubmit={() => {
        setErrors({});
        create.mutate();
      }}
    >
      <div className="grid grid-cols-[120px_1fr] gap-3">
        <Field label={t("projects.code")} htmlFor="project-code" error={errors["/code"]}>
          <Input
            id="project-code"
            className="font-mono"
            value={f.code}
            onChange={(e) => setF({ ...f, code: e.target.value })}
          />
        </Field>
        <Field label={t("projects.name")} htmlFor="project-name" error={errors["/name"]}>
          <Input id="project-name" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
        </Field>
      </div>
      <Field label={t("projects.description")} htmlFor="project-description">
        <Input
          id="project-description"
          value={f.description}
          onChange={(e) => setF({ ...f, description: e.target.value })}
        />
      </Field>
      <Field label={t("projects.costCentre")} htmlFor="project-cost">
        <Input id="project-cost" value={f.cost_center} onChange={(e) => setF({ ...f, cost_center: e.target.value })} />
      </Field>
      <fieldset className="flex flex-wrap gap-3">
        <legend className="mb-1 text-secondary font-medium">{t("projects.environmentsLegend")}</legend>
        {(envs.data?.items ?? []).map((e) => (
          <label key={e.key} htmlFor={`project-env-${e.key}`} className="flex items-center gap-1.5 text-body">
            <Checkbox
              id={`project-env-${e.key}`}
              checked={f.environments.includes(e.key)}
              onChange={(on) =>
                setF({
                  ...f,
                  environments: on ? [...f.environments, e.key] : f.environments.filter((x) => x !== e.key),
                })
              }
            />
            {e.name}
          </label>
        ))}
      </fieldset>
      <UserPicker
        label={t("projects.new.admins")}
        id="project-admin-search"
        onPick={(u) => !admins.some((a) => a.id === u.id) && setAdmins([...admins, u])}
      />
      {admins.length > 0 && (
        <ul aria-label={t("projects.new.chosenAdmins")} className="flex flex-wrap gap-1.5">
          {admins.map((a) => (
            <li key={a.id}>
              <Badge tone="accent">
                {a.name}{" "}
                <button
                  type="button"
                  aria-label={t("projects.new.removeAdmin", { name: a.name })}
                  onClick={() => setAdmins(admins.filter((x) => x.id !== a.id))}
                >
                  ×
                </button>
              </Badge>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}

/** Archiving asks, and so does putting back in use: either changes what its members may do. */
export function ArchiveDialog({ project, onClose }: { project: S["AdminProject"]; onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshAccess();
  const save = useMutation({
    mutationFn: () =>
      call(
        api.PATCH("/admin/projects/{projectId}", {
          params: { path: { projectId: project.id } },
          body: { archived: !project.archived },
        }),
      ),
    onSuccess: (p) => {
      refresh();
      toast.success(t(p.archived ? "projects.archive.archived" : "projects.archive.backInUse", { name: p.name }));
      onClose();
    },
    onError: (e) => failed(e, t("projects.archive.notChanged")),
  });
  const common = { open: true, onOpenChange: (o: boolean) => !o && onClose(), pending: save.isPending };
  if (project.archived)
    return (
      <ConfirmDialog
        {...common}
        tone="primary"
        title={t("projects.archive.restoreTitle", { name: project.name })}
        description={t("projects.archive.restoreLead")}
        confirmLabel={t("projects.archive.restore")}
        onConfirm={() => save.mutate()}
      />
    );
  return (
    <ConfirmDialog
      {...common}
      title={t("projects.archive.title", { name: project.name })}
      description={t("projects.archive.lead")}
      consequences={
        project.deployments === 0
          ? t("projects.archive.noDeployment")
          : t("projects.archive.keeps", { count: project.deployments })
      }
      confirmLabel={t("projects.archive.confirm")}
      onConfirm={() => save.mutate()}
    />
  );
}

const inGb: Record<S["QuotaResource"], boolean> = {
  vcpu: false,
  memory_gb: true,
  storage_gb: true,
  vm_count: false,
};

type QuotaLine = S["QuotaUsage"]["lines"][number];

/** What deployments and running requests take, of the limit: "12 of 64 GB". */
export function takenText(t: T, l: QuotaLine): string {
  const taken = formatNumber(l.used + l.reserved);
  if (l.limit == null) return inGb[l.resource] ? t("projects.quotaUse.gb", { taken }) : taken;
  return t(inGb[l.resource] ? "projects.quotaUse.ofGb" : "projects.quotaUse.of", {
    taken,
    limit: formatNumber(l.limit),
  });
}

export function quotaTone(l: QuotaLine): string | undefined {
  if (l.limit == null) return undefined;
  const taken = l.used + l.reserved;
  if (taken >= l.limit) return "text-bad";
  if (taken >= 0.8 * l.limit) return "text-warn";
  return undefined;
}

/** The project-wide limit nearest to being reached. */
function QuotaUse({ projectId }: { projectId: string }) {
  const { t } = useTranslation("admin-governance");
  const quota = useQuery(q.quota(projectId));
  if (quota.isPending) return <Skeleton className="h-4 w-24" />;
  if (quota.isError) return <span className="text-faint">{t("projects.quotaUse.notKnown")}</span>;
  const tightest = quota.data.lines
    .filter((l) => l.limit != null && l.limit > 0)
    .sort((a, b) => (b.used + b.reserved) / b.limit! - (a.used + a.reserved) / a.limit!)[0];
  if (!tightest) return <span className="text-muted">{t("projects.quotaUse.noProjectWide")}</span>;
  return (
    <span className={cn("tabular-nums", quotaTone(tightest))}>
      {t(`projects.quotaUse.name.${tightest.resource}`)} {takenText(t, tightest)}
    </span>
  );
}

function ProjectsPanel() {
  const { t } = useTranslation("admin-governance");
  const navigate = useNavigate();
  const envName = useEnvName();
  const search: ProjectsSearch = useSearch({ from: "/admin/projects/" });
  const filters = useAddressFilters(search, ["q", "archived"]);
  const [creating, setCreating] = useState(false);
  const [archiving, setArchiving] = useState<S["AdminProject"] | null>(null);
  const { me } = useCurrentProject();
  const scope = projectsWithAny(me, projectPagePermissions);
  const projects = useQuery({
    ...adm.projects(search.q ?? "", Boolean(search.archived)),
    enabled: scope === "all",
    placeholderData: keepPreviousData,
  });
  if (scope !== "all") {
    // The projects whose page the caller opens: a project admin's own, or those a role changes a part of.
    return (
      <Panel title={t("projects.list.yours")}>
        <ul className="flex flex-col gap-1 text-body">
          {scope.map((p) => (
            <li key={p.id}>
              <Link
                to="/admin/projects/$projectId"
                params={{ projectId: p.id }}
                className="font-medium hover:text-accent"
              >
                {p.name}
              </Link>
            </li>
          ))}
        </ul>
      </Panel>
    );
  }
  const columns: Column<S["AdminProject"]>[] = [
    {
      id: "project",
      header: t("projects.list.project"),
      sortValue: (p) => p.name.toLowerCase(),
      cell: (p) => (
        <div className="flex flex-wrap items-center gap-x-2">
          <Link to="/admin/projects/$projectId" params={{ projectId: p.id }} className="font-medium hover:text-accent">
            {p.name}
          </Link>
          <span className="font-mono text-caption text-faint">{p.code}</span>
          {p.archived && <Badge tone="neutral">{t("projects.list.archived")}</Badge>}
        </div>
      ),
    },
    {
      id: "environments",
      header: t("projects.list.environments"),
      cell: (p) => (
        <span className="text-secondary text-muted">
          {p.environments.map(envName).join(", ") || t("projects.none")}
        </span>
      ),
    },
    {
      id: "members",
      header: t("projects.list.members"),
      align: "end",
      sortValue: (p) => p.members,
      cell: (p) => <span className="tabular-nums">{p.members}</span>,
    },
    {
      id: "deployments",
      header: t("projects.list.deployments"),
      align: "end",
      sortValue: (p) => p.deployments,
      cell: (p) => <span className="tabular-nums">{p.deployments}</span>,
    },
    {
      id: "quota",
      header: t("projects.list.quota"),
      cell: (p) => (
        <span className="text-secondary">
          <QuotaUse projectId={p.id} />
        </span>
      ),
    },
  ];
  const create = can(me, "project.create") && (
    <Button size="sm" variant="primary" onClick={() => setCreating(true)}>
      {t("projects.list.add")}
    </Button>
  );
  // With no project at all, the empty state offers New project itself.
  const none = projects.data?.items.length === 0 && !filters.filtered;
  return (
    <Panel title={t("projects.list.title")} flush actions={none ? undefined : create}>
      <DataTable
        label={t("projects.list.title")}
        columns={columns}
        rows={projects.data?.items}
        rowKey={(p) => p.id}
        rowLinks
        minWidth={720}
        loading={projects.isPending}
        error={projects.isError && <ErrorState error={projects.error} onRetry={() => void projects.refetch()} />}
        toolbar={
          <FilterBar
            filters={filters}
            search={{ label: t("projects.list.search"), placeholder: t("projects.list.searchPlaceholder") }}
            end={projects.data ? t("projects.list.count", { count: projects.data.items.length }) : ""}
          >
            <FilterCheck name="archived" label={t("projects.list.archivedToo")} />
          </FilterBar>
        }
        empty={
          filters.filtered ? (
            <FilterEmpty
              filters={filters}
              icon={FolderKanban}
              title={t("projects.list.noMatch")}
              body={t("projects.list.noMatchBody")}
            />
          ) : (
            <EmptyState
              icon={FolderKanban}
              title={t("projects.list.emptyTitle")}
              body={t("projects.list.emptyBody")}
              action={create || undefined}
            />
          )
        }
        rowMenuLabel={(p) => t("projects.actionsFor", { name: p.name })}
        rowMenu={(p) => (
          <>
            <MenuItem onSelect={() => void navigate({ to: "/admin/projects/$projectId", params: { projectId: p.id } })}>
              {t("projects.list.open")}
            </MenuItem>
            {can(me, "project.update", p.id) && (
              <>
                <MenuSeparator />
                <MenuItem danger={!p.archived} onSelect={() => setArchiving(p)}>
                  {p.archived ? t("projects.archive.restore") : t("projects.archive.archive")}
                </MenuItem>
              </>
            )}
          </>
        )}
      />
      {creating && <NewProjectSheet onClose={() => setCreating(false)} />}
      {archiving && <ArchiveDialog project={archiving} onClose={() => setArchiving(null)} />}
    </Panel>
  );
}

function useRefreshEnvironments() {
  const qc = useQueryClient();
  return () => {
    void qc.invalidateQueries({ queryKey: ["admin", "environments"] });
    void qc.invalidateQueries({ queryKey: ["environments"] });
  };
}

function EnvironmentSheet({
  existing,
  next,
  onClose,
}: {
  existing?: S["AdminEnvironment"];
  /** The order a new environment gets: after the last one. */
  next: number;
  onClose: () => void;
}) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshEnvironments();
  const [f, setF] = useState({
    key: existing?.key ?? "",
    name: existing?.name ?? "",
    production: existing?.production ?? false,
    snapshots: String(existing?.snapshot_max_count ?? 3),
    tagCategories: existing?.tag_categories ?? [],
  });
  const [errors, setErrors] = useState<Record<string, string>>({});
  const snapshots = Number(f.snapshots);
  const tag_categories = f.tagCategories;
  const save = useMutation({
    mutationFn: () =>
      existing
        ? call(
            api.PATCH("/admin/environments/{key}", {
              params: { path: { key: existing.key } },
              body: { name: f.name, production: f.production, snapshot_max_count: snapshots, tag_categories },
            }),
          )
        : call(
            api.POST("/admin/environments", {
              body: {
                key: f.key,
                name: f.name,
                production: f.production,
                order: next,
                snapshot_max_count: snapshots,
                tag_categories,
              },
            }),
          ),
    onSuccess: () => {
      refresh();
      toast.success(t(existing ? "projects.environments.saved" : "projects.environments.added", { name: f.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0)
        failed(e, existing ? t("projects.notSaved") : t("projects.environments.notAdded"));
    },
  });
  const whole = f.snapshots.trim() !== "" && Number.isInteger(snapshots) && snapshots >= 0;
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={
        existing ? t("projects.environments.changeNamed", { name: existing.name }) : t("projects.environments.new")
      }
      description={t("projects.environments.sheetLead")}
      submitLabel={
        save.isPending
          ? t("projects.saving")
          : existing
            ? t("projects.environments.save")
            : t("projects.environments.create")
      }
      submitDisabled={!f.key || !f.name || !whole}
      onSubmit={() => {
        setErrors({});
        save.mutate();
      }}
    >
      <Field
        label={t("projects.environments.key")}
        htmlFor="env-key"
        hint={t("projects.environments.keyHint")}
        error={errors["/key"]}
      >
        <Input
          id="env-key"
          className="font-mono"
          placeholder={keyExample}
          value={f.key}
          disabled={Boolean(existing)}
          onChange={(e) => setF({ ...f, key: e.target.value })}
        />
      </Field>
      <Field label={t("projects.name")} htmlFor="env-name" error={errors["/name"]}>
        <Input
          id="env-name"
          placeholder={t("projects.environments.namePlaceholder")}
          value={f.name}
          onChange={(e) => setF({ ...f, name: e.target.value })}
        />
      </Field>
      <CheckField
        id="env-production"
        label={t("projects.environments.production")}
        hint={t("projects.environments.productionNote")}
        checked={f.production}
        onChange={(on) => setF({ ...f, production: on })}
      />
      <Field
        label={t("projects.environments.snapshots")}
        htmlFor="env-snapshots"
        hint={t("projects.environments.snapshotsHint")}
        error={errors["/snapshot_max_count"]}
      >
        <Input
          id="env-snapshots"
          type="number"
          min={0}
          className="w-24"
          value={f.snapshots}
          onChange={(e) => setF({ ...f, snapshots: e.target.value })}
        />
      </Field>
      <TagCategoriesField
        value={f.tagCategories}
        onChange={(tagCategories) => setF({ ...f, tagCategories })}
        error={errors["/tag_categories"]}
      />
    </Sheet>
  );
}

/**
 * The vSphere tag categories an environment's VMs' users may change with
 * Edit tags (X1.3): those vCenter has for VMs, and any already chosen.
 */
function TagCategoriesField({
  value,
  onChange,
  error,
}: {
  value: string[];
  onChange: (v: string[]) => void;
  error?: string;
}) {
  const { t } = useTranslation("admin-governance");
  const inventory = useQuery(adm.inventory());
  const known = new Map<string, S["InventoryTagCategory"]>();
  for (const c of inventory.data?.tag_categories ?? []) if (!known.has(c.name)) known.set(c.name, c);
  const names = [...new Set([...known.keys(), ...value])].sort((a, b) => a.localeCompare(b));
  const toggle = (name: string, on: boolean) =>
    onChange(on ? [...value, name].sort((a, b) => a.localeCompare(b)) : value.filter((v) => v !== name));
  return (
    <fieldset className="flex flex-col gap-1.5" aria-describedby="env-tag-categories-note">
      <legend className="mb-1 text-secondary font-medium">{t("projects.environments.tagsLegend")}</legend>
      {inventory.isPending ? (
        <Skeleton className="h-5 w-40" />
      ) : names.length === 0 ? (
        <p className="text-secondary text-muted">
          {inventory.isError ? t("projects.environments.tagsUnreadable") : t("projects.environments.tagsNone")}
        </p>
      ) : (
        names.map((name) => {
          const c = known.get(name);
          const id = `env-tag-category-${name.replace(/[^a-zA-Z0-9_-]/g, "_")}`;
          return (
            <div key={name} className="flex items-baseline gap-2 text-body">
              <Checkbox id={id} checked={value.includes(name)} onChange={(on) => toggle(name, on)} />
              <label htmlFor={id} className="font-mono text-secondary">
                {name}
              </label>
              <span className="min-w-0 truncate text-caption text-faint">
                {c
                  ? [
                      c.single && t("projects.environments.onePerVm"),
                      c.tags.join(", ") || t("projects.environments.noTagsYet"),
                    ]
                      .filter(Boolean)
                      .join(" · ")
                  : t("projects.environments.notInVcenter")}
              </span>
            </div>
          );
        })
      )}
      <FieldNote id="env-tag-categories-note" error={error} hint={t("projects.environments.tagsHint")} />
    </fieldset>
  );
}

function EnvironmentsPanel({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshEnvironments();
  const envs = useQuery(adm.environments());
  const [editing, setEditing] = useState<S["AdminEnvironment"] | "new" | null>(null);
  const [removing, setRemoving] = useState<S["AdminEnvironment"] | null>(null);
  const remove = useMutation({
    mutationFn: (key: string) => call(api.DELETE("/admin/environments/{key}", { params: { path: { key } } })),
    onSuccess: () => {
      refresh();
      toast.success(
        t("projects.environments.removed", { name: removing?.name ?? t("projects.environments.theEnvironment") }),
      );
      setRemoving(null);
    },
    onError: (e) => failed(e, t("projects.notRemoved")),
  });
  const list = envs.data?.items;
  const columns: Column<S["AdminEnvironment"]>[] = [
    {
      id: "environment",
      header: t("projects.environments.environment"),
      sortValue: (e) => e.order,
      cell: (e) => (
        <div className="flex flex-wrap items-center gap-x-2">
          <span className="font-medium">{e.name}</span>
          <span className="font-mono text-caption text-faint">{e.key}</span>
        </div>
      ),
    },
    {
      id: "production",
      header: t("projects.environments.production"),
      cell: (e) =>
        e.production ? (
          <Badge tone="warn">{t("projects.environments.production")}</Badge>
        ) : (
          <span className="text-muted">{t("projects.environments.no")}</span>
        ),
    },
    {
      id: "snapshots",
      header: t("projects.environments.snapshots"),
      align: "end",
      sortValue: (e) => e.snapshot_max_count,
      cell: (e) => <span className="tabular-nums">{e.snapshot_max_count}</span>,
    },
    {
      id: "tags",
      header: t("projects.environments.tags"),
      cell: (e) =>
        e.tag_categories?.length ? (
          <span className="font-mono text-secondary">{e.tag_categories.join(", ")}</span>
        ) : (
          <span className="text-muted">{t("projects.none")}</span>
        ),
    },
    {
      id: "used",
      header: t("projects.environments.usedBy"),
      cell: (e) => (
        <span className="text-secondary text-muted">
          {t("projects.environments.projects", { count: e.projects ?? 0 })},{" "}
          {t("projects.environments.deployments", { count: e.deployments ?? 0 })}
        </span>
      ),
    },
  ];
  const add = canEdit && (
    <Button size="sm" variant="primary" onClick={() => setEditing("new")}>
      {t("projects.environments.new")}
    </Button>
  );
  const why = (e: S["AdminEnvironment"]) =>
    (e.projects ?? 0) > 0
      ? t("projects.environments.removeProjects")
      : (e.deployments ?? 0) > 0
        ? t("projects.environments.removeDeployments")
        : undefined;
  return (
    <Panel
      title={t("projects.environments.title")}
      description={t("projects.environments.lead")}
      flush
      actions={list?.length ? add : undefined}
    >
      <DataTable
        label={t("projects.environments.title")}
        columns={columns}
        rows={list}
        rowKey={(e) => e.key}
        minWidth={680}
        loading={envs.isPending}
        error={envs.isError && <ErrorState error={envs.error} onRetry={() => void envs.refetch()} />}
        empty={
          <EmptyState
            icon={Layers}
            title={t("projects.environments.emptyTitle")}
            body={t("projects.environments.emptyBody")}
            action={add || undefined}
          />
        }
        rowMenuLabel={(e) => t("projects.actionsFor", { name: e.name })}
        rowMenu={
          canEdit
            ? (e) => (
                <>
                  <MenuItem onSelect={() => setEditing(e)}>{t("projects.change")}</MenuItem>
                  <MenuSeparator />
                  <MenuItem danger disabled={Boolean(why(e))} onSelect={() => setRemoving(e)}>
                    {why(e) ?? t("projects.remove")}
                  </MenuItem>
                </>
              )
            : undefined
        }
      />
      {editing && (
        <EnvironmentSheet
          existing={editing === "new" ? undefined : editing}
          next={Math.max(0, ...(list ?? []).map((e) => e.order)) + 1}
          onClose={() => setEditing(null)}
        />
      )}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("projects.environments.removeTitle", { name: removing.name })}
          description={t("projects.environments.removeLead")}
          consequences={t("projects.environments.removeConsequences")}
          confirmLabel={t("projects.environments.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.key)}
        />
      )}
    </Panel>
  );
}

function MapGroupSheet({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshAccess();
  const projects = useQuery(adm.projectNames());
  const roles = useQuery(adm.roles());
  const [f, setF] = useState({ group: "", project: "none", role: "auditor" });
  const add = useMutation({
    mutationFn: () =>
      call(
        api.POST("/admin/group-mappings", {
          body: { oidc_group: f.group, role: f.role, ...(f.project !== "none" ? { project_id: f.project } : {}) },
        }),
      ),
    onSuccess: () => {
      refresh();
      toast.success(t("projects.groups.mapped", { group: f.group }));
      onClose();
    },
    onError: (e) => {
      if (!refused(e)) failed(e, t("projects.groups.notMapped"));
    },
  });
  // A role is mapped where it can be given: for all of rvav without a project, in one with it.
  const offered = (roles.data?.items ?? []).filter(
    (r) => r.key !== "user" && r.scope !== (f.project === "none" ? "project" : "rvav"),
  );
  const chosen = offered.find((r) => r.key === f.role);
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={add.isPending}
      title={t("projects.groups.title")}
      description={t("projects.groups.lead")}
      submitLabel={add.isPending ? t("projects.groups.mapping") : t("projects.groups.map")}
      submitDisabled={!f.group}
      onSubmit={() => add.mutate()}
    >
      <Field label={t("projects.groups.group")} htmlFor="map-group" hint={t("projects.groups.groupHint")}>
        <Input
          id="map-group"
          className="font-mono"
          placeholder={groupExample}
          value={f.group}
          onChange={(e) => setF({ ...f, group: e.target.value })}
        />
      </Field>
      <Field label={t("projects.groups.inProject")} htmlFor="map-project">
        <Select
          id="map-project"
          value={f.project}
          onChange={(v) => setF({ ...f, project: v, role: v === "none" ? "auditor" : "member" })}
          options={[
            { value: "none", label: t("projects.groups.forAll") },
            ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
          ]}
        />
      </Field>
      <Field label={t("projects.groups.role")} htmlFor="map-role">
        <Select
          id="map-role"
          value={f.role}
          onChange={(v) => setF({ ...f, role: v })}
          loading={roles.isPending}
          options={offered.map((r) => ({
            value: r.key,
            label: roleName(r),
            group: r.built_in ? t("projects.builtIn") : t("projects.custom"),
          }))}
        />
      </Field>
      {chosen && !chosen.built_in && chosen.permissions.includes("access.manage") && (
        <AdminWarning who={t("projects.groups.everyoneIn", { group: f.group || t("projects.groups.theGroup") })} />
      )}
      <Refusal error={add.error} />
    </Sheet>
  );
}

function GroupMappingsPanel({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-governance");
  const refresh = useRefreshAccess();
  const mappings = useQuery(adm.groupMappings());
  const [mapping, setMapping] = useState(false);
  const [removing, setRemoving] = useState<S["GroupMapping"] | null>(null);
  const remove = useMutation({
    mutationFn: (id: string) => call(api.DELETE("/admin/group-mappings/{id}", { params: { path: { id } } })),
    onSuccess: () => {
      refresh();
      toast.success(t("projects.groups.removed", { group: removing?.oidc_group ?? t("projects.groups.theGroup") }));
      setRemoving(null);
    },
    onError: (e) => {
      setRemoving(null);
      if (!refused(e)) failed(e, t("projects.notRemoved"));
    },
  });
  const gives = (m: S["GroupMapping"]) =>
    m.project
      ? t("projects.groups.givesIn", { role: mappedRole(m), project: m.project.name })
      : t("projects.groups.givesAll", { role: mappedRole(m) });
  const columns: Column<S["GroupMapping"]>[] = [
    {
      id: "group",
      header: t("projects.groups.groupColumn"),
      sortValue: (m) => m.oidc_group,
      cell: (m) => <span className="font-mono text-secondary">{m.oidc_group}</span>,
    },
    {
      id: "gives",
      header: t("projects.groups.gives"),
      sortValue: (m) => gives(m),
      cell: (m) => (
        <span className="text-secondary">
          <Trans
            t={t}
            i18nKey={m.project ? "projects.groups.givesInLink" : "projects.groups.givesAllLink"}
            values={{ role: mappedRole(m), project: m.project?.name }}
            components={{
              role: (
                <Link
                  to="/admin/roles/$roleKey"
                  params={{ roleKey: m.role }}
                  className="text-accent underline underline-offset-2"
                />
              ),
            }}
          />
        </span>
      ),
    },
    {
      id: "people",
      header: t("projects.groups.people"),
      align: "end",
      sortValue: (m) => m.users ?? 0,
      cell: (m) => <span className="tabular-nums">{m.users ?? 0}</span>,
    },
  ];
  const add = canEdit && (
    <Button size="sm" variant="primary" onClick={() => setMapping(true)}>
      {t("projects.groups.add")}
    </Button>
  );
  const list = mappings.data?.items;
  return (
    <Panel
      title={t("projects.groups.panelTitle")}
      description={t("projects.groups.panelLead")}
      flush
      actions={list?.length ? add : undefined}
    >
      {refused(remove.error) && (
        <div className="border-b border-line px-4 py-3">
          <Refusal error={remove.error} />
        </div>
      )}
      <DataTable
        label={t("projects.groups.panelTitle")}
        columns={columns}
        rows={list}
        rowKey={(m) => m.id}
        minWidth={560}
        loading={mappings.isPending}
        error={mappings.isError && <ErrorState error={mappings.error} onRetry={() => void mappings.refetch()} />}
        empty={
          <EmptyState
            icon={UsersRound}
            title={t("projects.groups.emptyTitle")}
            body={t("projects.groups.emptyBody")}
            action={add || undefined}
          />
        }
        rowMenuLabel={(m) => t("projects.actionsFor", { name: m.oidc_group })}
        rowMenu={
          canEdit
            ? (m) => (
                <MenuItem danger onSelect={() => setRemoving(m)}>
                  {t("projects.remove")}
                </MenuItem>
              )
            : undefined
        }
      />
      {mapping && <MapGroupSheet onClose={() => setMapping(false)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("projects.groups.removeTitle", { group: removing.oidc_group })}
          description={t("projects.groups.removeLead", { group: removing.oidc_group, gives: gives(removing) })}
          consequences={
            (removing.users ?? 0) > 0
              ? t("projects.groups.signedIn", { count: removing.users ?? 0 })
              : t("projects.groups.nobodySignedIn")
          }
          confirmLabel={t("projects.groups.removeConfirm")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing.id)}
        />
      )}
    </Panel>
  );
}

export function ProjectsPage({ tab }: { tab?: ProjectsTab }) {
  const { t } = useTranslation(["admin-governance", "shell"]);
  const navigate = useNavigate();
  const { me } = useCurrentProject();
  const seesEnvironments = can(me, "environments.read");
  const seesGroups = can(me, "access.read");
  const seesPlacements = can(me, "project.settings.read") || can(me, "placement.manage");
  const tabs = [
    { value: "projects", show: true },
    { value: "environments", show: seesEnvironments },
    { value: "groups", show: seesGroups },
  ].filter((s) => s.show);
  const shown = tabs.some((s) => s.value === tab) ? tab : "projects";
  return (
    <Page>
      <PageHeader title={t("shell:nav.admin.projects")} description={t("projects.lead")} />
      {tabs.length === 1 ? (
        <ProjectsPanel />
      ) : (
        <Tabs
          value={shown}
          onValueChange={(next) =>
            void navigate({ to: "/admin/projects", search: (s) => ({ ...s, tab: next as ProjectsTab }), replace: true })
          }
        >
          <TabsList aria-label={t("projects.tabsLabel")}>
            {tabs.map((s) => (
              <TabsTrigger key={s.value} value={s.value}>
                {t(`projects.tabs.${s.value as ProjectsTab}`)}
              </TabsTrigger>
            ))}
          </TabsList>
          <TabsContent value="projects" className="pt-4">
            <ProjectsPanel />
          </TabsContent>
          {seesEnvironments && (
            <TabsContent value="environments" className="flex flex-col gap-4 pt-4">
              <EnvironmentsPanel canEdit={can(me, "environments.manage")} />
              {seesPlacements && <DirectoryPlacements canManage={can(me, "placement.manage")} />}
            </TabsContent>
          )}
          {seesGroups && (
            <TabsContent value="groups" className="pt-4">
              <GroupMappingsPanel canEdit={can(me, "access.manage")} />
            </TabsContent>
          )}
        </Tabs>
      )}
    </Page>
  );
}
