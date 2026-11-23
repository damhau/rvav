// Administration › Roles (ADR-0034): the built-in roles and the custom ones,
// what each allows and where it can be given.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { UserRound } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { failed, UserPicker } from "../governance/common";
import { WhatSomeoneMayDo } from "./AccessExplained";
import { scopeText } from "./permissions";
import { AdminWarning, Refusal, refused } from "./refusal";
import { cantDelete, DeleteRole } from "./RolePage";
import { roleName } from "@/lib/i18n/names";

type S = components["schemas"];

const roleColumns = (t: TFunction<"admin-access">): Column<S["Role"]>[] => [
  {
    id: "role",
    header: t("roles.role"),
    sortValue: (r) => roleName(r),
    cell: (r) => (
      <>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <Link
            to="/admin/roles/$roleKey"
            params={{ roleKey: r.key }}
            className="font-medium text-fg hover:text-accent"
          >
            {roleName(r)}
          </Link>
          <span className="font-mono text-secondary text-muted">{r.key}</span>
          {r.built_in ? <Badge>{t("roles.builtIn")}</Badge> : <Badge tone="accent">{t("roles.custom")}</Badge>}
        </div>
        {r.description && <p className="mt-0.5 text-secondary text-muted">{r.description}</p>}
      </>
    ),
  },
  {
    id: "given",
    header: t("roles.given"),
    sortValue: (r) => scopeText(r.scope),
    cell: (r) => <span className="text-secondary">{scopeText(r.scope)}</span>,
  },
  {
    id: "permissions",
    header: t("roles.permissions"),
    sortValue: (r) => r.permissions.length,
    cell: (r) => (
      <span className="text-secondary text-muted">
        {r.permissions.length === 0 ? t("roles.noPermissions") : r.permissions.length}
        {r.permissions.includes("access.manage") && (
          <Badge tone="warn" className="ml-1.5">
            {t("roles.managesAccess")}
          </Badge>
        )}
      </span>
    ),
  },
  {
    id: "holders",
    header: t("roles.givenTo"),
    sortValue: (r) => (r.key === "user" ? Infinity : r.holders),
    cell: (r) => (
      <span className="text-secondary text-muted">
        {r.key === "user"
          ? t("roles.everyone")
          : r.holders === 0
            ? t("roles.nobody")
            : t("roles.times", { count: r.holders })}
      </span>
    ),
  },
];

function Roles({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-access");
  const navigate = useNavigate();
  const roles = useQuery(adm.roles());
  const [deleting, setDeleting] = useState<S["Role"] | null>(null);
  return (
    <Panel title={t("roles.tabs.roles")} description={t("roles.panelLead")} flush>
      <DataTable
        label={t("roles.tabs.roles")}
        columns={roleColumns(t)}
        rows={roles.data?.items}
        rowKey={(r) => r.key}
        rowLinks
        minWidth={720}
        loading={roles.isPending}
        error={roles.isError && <ErrorState error={roles.error} onRetry={() => void roles.refetch()} />}
        rowMenuLabel={(r) => t("roles.actionsFor", { name: roleName(r) })}
        rowMenu={
          canEdit
            ? (r) => (
                <>
                  <MenuItem onSelect={() => void navigate({ to: "/admin/roles/new", search: { from: r.key } })}>
                    {t("roles.makeFrom")}
                  </MenuItem>
                  {!r.built_in && (
                    <>
                      <MenuSeparator />
                      <MenuItem danger disabled={Boolean(cantDelete(r))} onSelect={() => setDeleting(r)}>
                        {cantDelete(r) ?? t("roles.delete")}
                      </MenuItem>
                    </>
                  )}
                </>
              )
            : undefined
        }
      />
      {deleting && <DeleteRole role={deleting} onClose={() => setDeleting(null)} />}
    </Panel>
  );
}

const assignmentColumns = (t: TFunction<"admin-access">): Column<S["RoleAssignment"]>[] => [
  {
    id: "who",
    header: t("rvavRoles.who"),
    sortValue: (a) => a.user.name,
    cell: (a) => (
      <>
        {a.user.name} <span className="text-secondary text-faint">{a.user.email}</span>
      </>
    ),
  },
  {
    id: "role",
    header: t("rvavRoles.role"),
    sortValue: (a) => roleName(a.role),
    cell: (a) => (
      <Link
        to="/admin/roles/$roleKey"
        params={{ roleKey: a.role.key }}
        className="text-accent underline underline-offset-2"
      >
        {roleName(a.role)}
      </Link>
    ),
  },
  {
    id: "given",
    header: t("rvavRoles.given"),
    sortValue: (a) => a.created_at,
    cell: (a) => (
      <span className="text-secondary text-muted">
        {a.fixed ? t("rvavRoles.setByRvav") : a.created_at ? <Time iso={a.created_at} /> : ""}
      </span>
    ),
  },
];

/** Roles given to users for all of rvav; groups get theirs through a group mapping. */
function ForAllOfRvav({ canEdit }: { canEdit: boolean }) {
  const { t } = useTranslation("admin-access");
  const qc = useQueryClient();
  const list = useQuery(adm.roleAssignments());
  const roles = useQuery(adm.roles());
  const [role, setRole] = useState("auditor");
  const [taking, setTaking] = useState<S["RoleAssignment"] | null>(null);
  const offered = (roles.data?.items ?? []).filter((r) => r.key !== "user" && r.scope !== "project");
  const chosen = offered.find((r) => r.key === role);
  const changed = () => {
    void qc.invalidateQueries({ queryKey: ["admin"] });
    void qc.invalidateQueries({ queryKey: ["me"] });
  };
  const give = useMutation({
    mutationFn: (userId: string) => call(api.POST("/admin/role-assignments", { body: { user_id: userId, role } })),
    onSuccess: (a) => {
      changed();
      toast.success(t("rvavRoles.holds", { user: a.user.name, role: roleName(a.role) }));
    },
    onError: (e) => {
      if (!refused(e)) failed(e, t("rvavRoles.notGiven"));
    },
  });
  const remove = useMutation({
    mutationFn: (a: S["RoleAssignment"]) =>
      call(api.DELETE("/admin/role-assignments/{id}", { params: { path: { id: a.id! } } })),
    onSuccess: (_, a) => {
      changed();
      setTaking(null);
      toast.success(t("rvavRoles.takenAway", { role: roleName(a.role), user: a.user.name }));
    },
    onError: (e) => {
      setTaking(null);
      if (!refused(e)) failed(e, t("rvavRoles.notTakenAway"));
    },
  });
  return (
    <Panel
      title={t("rvavRoles.title")}
      description={t("rvavRoles.lead")}
      flush
      footer={
        <span className="mr-auto">
          <Trans
            t={t}
            i18nKey="rvavRoles.footer"
            components={{
              groups: (
                <Link
                  to="/admin/projects"
                  search={{ tab: "groups" }}
                  className="text-accent underline underline-offset-2"
                />
              ),
            }}
          />
        </span>
      }
    >
      <DataTable
        label={t("rvavRoles.title")}
        columns={assignmentColumns(t)}
        rows={list.data?.items}
        rowKey={(a) => a.id ?? `${a.user.id}/${a.role.key}`}
        minWidth={560}
        loading={list.isPending}
        error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
        toolbar={
          canEdit && (
            <div className="flex w-full flex-col gap-2">
              <div className="flex flex-wrap items-end gap-2">
                <div className="w-72">
                  <UserPicker
                    label={t("rvavRoles.giveSomeone")}
                    id="rvav-user"
                    onPick={(u) => {
                      remove.reset();
                      give.mutate(u.id);
                    }}
                  />
                </div>
                <Field label={t("rvavRoles.theRole")} htmlFor="rvav-role" className="w-56">
                  <Select
                    id="rvav-role"
                    value={role}
                    onChange={setRole}
                    loading={roles.isPending}
                    options={offered.map((r) => ({
                      value: r.key,
                      label: roleName(r),
                      group: r.built_in ? t("rvavRoles.builtIn") : t("rvavRoles.custom"),
                    }))}
                  />
                </Field>
              </div>
              {chosen && !chosen.built_in && chosen.permissions.includes("access.manage") && (
                <AdminWarning who={t("refusal.whoeverHolds", { name: chosen.name })} />
              )}
              <Refusal error={give.error} />
              <Refusal error={remove.error} />
            </div>
          )
        }
        empty={
          <EmptyState
            icon={UserRound}
            title={t("rvavRoles.nobody")}
            body={canEdit ? t("rvavRoles.noneGiveAbove") : t("rvavRoles.none")}
          />
        }
        rowMenuLabel={(a) => t("rvavRoles.actionsFor", { role: roleName(a.role), user: a.user.name })}
        rowMenu={
          canEdit
            ? (a) =>
                a.fixed ? null : (
                  <MenuItem
                    danger
                    onSelect={() => {
                      give.reset();
                      setTaking(a);
                    }}
                  >
                    {t("rvavRoles.takeAway")}
                  </MenuItem>
                )
            : undefined
        }
      />
      {taking && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setTaking(null)}
          title={t("rvavRoles.takeTitle", { role: roleName(taking.role), user: taking.user.name })}
          description={t("rvavRoles.takeBody", { role: roleName(taking.role) })}
          consequences={t("rvavRoles.takeConsequences")}
          confirmLabel={t("rvavRoles.takeAway")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(taking)}
        />
      )}
    </Panel>
  );
}

export type RolesTab = "roles" | "rvav" | "explain";

export interface RolesSearch {
  tab?: RolesTab;
  user?: string;
  project?: string;
}

export function RolesPage({ tab, user, project }: RolesSearch) {
  const { t } = useTranslation(["admin-access", "shell"]);
  const { me } = useCurrentProject();
  const navigate = useNavigate();
  const canEdit = can(me, "access.manage");
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.roles")}
        description={t("roles.lead")}
        actions={
          canEdit && (
            <Button asChild variant="primary">
              <Link to="/admin/roles/new" search={{}}>
                {t("roles.new")}
              </Link>
            </Button>
          )
        }
      />
      <Tabs
        value={tab ?? "roles"}
        onValueChange={(v) =>
          void navigate({ to: "/admin/roles", search: (s) => ({ ...s, tab: v as RolesTab }), replace: true })
        }
      >
        <TabsList aria-label={t("roles.sections")}>
          <TabsTrigger value="roles">{t("roles.tabs.roles")}</TabsTrigger>
          <TabsTrigger value="rvav">{t("roles.tabs.rvav")}</TabsTrigger>
          <TabsTrigger value="explain">{t("roles.tabs.explain")}</TabsTrigger>
        </TabsList>
        <TabsContent value="roles" className="pt-4">
          <Roles canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="rvav" className="pt-4">
          <ForAllOfRvav canEdit={canEdit} />
        </TabsContent>
        <TabsContent value="explain" className="pt-4">
          <WhatSomeoneMayDo
            userId={user}
            projectId={project}
            onChange={(s) => void navigate({ to: "/admin/roles", search: { tab: "explain", ...s }, replace: true })}
          />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
