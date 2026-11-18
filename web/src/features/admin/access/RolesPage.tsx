// Administration › Roles (ADR-0034): the built-in roles and the custom ones,
// what each allows and where it can be given.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { UserRound } from "lucide-react";
import { useState } from "react";
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

type S = components["schemas"];

const roleColumns: Column<S["Role"]>[] = [
  {
    id: "role",
    header: "Role",
    sortValue: (r) => r.name,
    cell: (r) => (
      <>
        <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
          <Link
            to="/admin/roles/$roleKey"
            params={{ roleKey: r.key }}
            className="font-medium text-fg hover:text-accent"
          >
            {r.name}
          </Link>
          <span className="font-mono text-secondary text-muted">{r.key}</span>
          {r.built_in ? <Badge>Built in</Badge> : <Badge tone="accent">Custom</Badge>}
        </div>
        {r.description && <p className="mt-0.5 text-secondary text-muted">{r.description}</p>}
      </>
    ),
  },
  {
    id: "given",
    header: "Given",
    sortValue: (r) => scopeText[r.scope],
    cell: (r) => <span className="text-secondary">{scopeText[r.scope]}</span>,
  },
  {
    id: "permissions",
    header: "Permissions",
    sortValue: (r) => r.permissions.length,
    cell: (r) => (
      <span className="text-secondary text-muted">
        {r.permissions.length === 0 ? "None: everyone signed in holds it" : r.permissions.length}
        {r.permissions.includes("access.manage") && (
          <Badge tone="warn" className="ml-1.5">
            Manages access
          </Badge>
        )}
      </span>
    ),
  },
  {
    id: "holders",
    header: "Given to",
    sortValue: (r) => (r.key === "user" ? Infinity : r.holders),
    cell: (r) => (
      <span className="text-secondary text-muted">
        {r.key === "user"
          ? "Everyone"
          : r.holders === 0
            ? "Nobody"
            : `${r.holders} ${r.holders === 1 ? "time" : "times"}`}
      </span>
    ),
  },
];

function Roles({ canEdit }: { canEdit: boolean }) {
  const navigate = useNavigate();
  const roles = useQuery(adm.roles());
  const [deleting, setDeleting] = useState<S["Role"] | null>(null);
  return (
    <Panel
      title="Roles"
      description="A role's page says, in words, what it allows. A built-in role can't be changed: make one of your own from it."
      flush
    >
      <DataTable
        label="Roles"
        columns={roleColumns}
        rows={roles.data?.items}
        rowKey={(r) => r.key}
        rowLinks
        minWidth={720}
        loading={roles.isPending}
        error={roles.isError && <ErrorState error={roles.error} onRetry={() => void roles.refetch()} />}
        rowMenuLabel={(r) => `Actions for ${r.name}`}
        rowMenu={
          canEdit
            ? (r) => (
                <>
                  <MenuItem onSelect={() => void navigate({ to: "/admin/roles/new", search: { from: r.key } })}>
                    Make a role from it
                  </MenuItem>
                  {!r.built_in && (
                    <>
                      <MenuSeparator />
                      <MenuItem danger disabled={Boolean(cantDelete(r))} onSelect={() => setDeleting(r)}>
                        {cantDelete(r) ?? "Delete"}
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

const assignmentColumns: Column<S["RoleAssignment"]>[] = [
  {
    id: "who",
    header: "Who",
    sortValue: (a) => a.user.name,
    cell: (a) => (
      <>
        {a.user.name} <span className="text-secondary text-faint">{a.user.email}</span>
      </>
    ),
  },
  {
    id: "role",
    header: "Role",
    sortValue: (a) => a.role.name,
    cell: (a) => (
      <Link
        to="/admin/roles/$roleKey"
        params={{ roleKey: a.role.key }}
        className="text-accent underline underline-offset-2"
      >
        {a.role.name}
      </Link>
    ),
  },
  {
    id: "given",
    header: "Given",
    sortValue: (a) => a.created_at,
    cell: (a) => (
      <span className="text-secondary text-muted">
        {a.fixed ? "Set by rvav" : a.created_at ? <Time iso={a.created_at} /> : ""}
      </span>
    ),
  },
];

/** Roles given to users for all of rvav; groups get theirs through a group mapping. */
function ForAllOfRvav({ canEdit }: { canEdit: boolean }) {
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
      toast.success(`${a.user.name} holds ${a.role.name} for all of rvav`);
    },
    onError: (e) => {
      if (!refused(e)) failed(e, "Not given");
    },
  });
  const remove = useMutation({
    mutationFn: (a: S["RoleAssignment"]) =>
      call(api.DELETE("/admin/role-assignments/{id}", { params: { path: { id: a.id! } } })),
    onSuccess: (_, a) => {
      changed();
      setTaking(null);
      toast.success(`${a.role.name} taken away from ${a.user.name}`);
    },
    onError: (e) => {
      setTaking(null);
      if (!refused(e)) failed(e, "Not taken away");
    },
  });
  return (
    <Panel
      title="Given for all of rvav"
      description="A role given here counts in every project."
      flush
      footer={
        <span className="mr-auto">
          Groups of the identity provider get a role for all of rvav through a group mapping without a project, in{" "}
          <Link to="/admin/projects" search={{ tab: "groups" }} className="text-accent underline underline-offset-2">
            Projects › Identity provider groups
          </Link>
          . Roles in a project are given on the project&apos;s page.
        </span>
      }
    >
      <DataTable
        label="Given for all of rvav"
        columns={assignmentColumns}
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
                    label="Give someone"
                    id="rvav-user"
                    onPick={(u) => {
                      remove.reset();
                      give.mutate(u.id);
                    }}
                  />
                </div>
                <Field label="The role" htmlFor="rvav-role" className="w-56">
                  <Select
                    id="rvav-role"
                    value={role}
                    onChange={setRole}
                    loading={roles.isPending}
                    options={offered.map((r) => ({
                      value: r.key,
                      label: r.name,
                      group: r.built_in ? "Built in" : "Custom",
                    }))}
                  />
                </Field>
              </div>
              {chosen && !chosen.built_in && chosen.permissions.includes("access.manage") && (
                <AdminWarning who={`whoever holds ${chosen.name}`} />
              )}
              <Refusal error={give.error} />
              <Refusal error={remove.error} />
            </div>
          )
        }
        empty={
          <EmptyState
            icon={UserRound}
            title="Nobody"
            body={
              canEdit
                ? "No role is given to a user for all of rvav. Give one above."
                : "No role is given to a user for all of rvav."
            }
          />
        }
        rowMenuLabel={(a) => `Actions for ${a.role.name} of ${a.user.name}`}
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
                    Take it away
                  </MenuItem>
                )
            : undefined
        }
      />
      {taking && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setTaking(null)}
          title={`Take ${taking.role.name} away from ${taking.user.name}?`}
          description={`They lose what ${taking.role.name} allows for all of rvav, unless a group or a project gives it to them.`}
          consequences="Requests and approvals they started stay as they are."
          confirmLabel="Take it away"
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
  const { me } = useCurrentProject();
  const navigate = useNavigate();
  const canEdit = can(me, "access.manage");
  return (
    <Page>
      <PageHeader
        title="Roles"
        description="What each role allows, where it can be given, and who holds it."
        actions={
          canEdit && (
            <Button asChild variant="primary">
              <Link to="/admin/roles/new" search={{}}>
                New role
              </Link>
            </Button>
          )
        }
      />
      <Tabs
        value={tab ?? "roles"}
        onValueChange={(v) => void navigate({ to: "/admin/roles", search: { tab: v as RolesTab }, replace: true })}
      >
        <TabsList aria-label="Roles sections">
          <TabsTrigger value="roles">Roles</TabsTrigger>
          <TabsTrigger value="rvav">For all of rvav</TabsTrigger>
          <TabsTrigger value="explain">What someone may do</TabsTrigger>
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
