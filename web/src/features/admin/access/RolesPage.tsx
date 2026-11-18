// Administration › Roles (ADR-0034): the built-in roles and the custom ones,
// what each allows and where it can be given.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Time } from "@/components/common/misc";
import { Page } from "@/components/common/page";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { failed, td, th, UserPicker } from "../governance/common";
import { scopeText } from "./permissions";
import { WhatSomeoneMayDo } from "./AccessExplained";
import { AdminWarning, Refusal, refused } from "./refusal";

type S = components["schemas"];

function Roles({ canEdit }: { canEdit: boolean }) {
  const roles = useQuery(adm.roles());
  return (
    <Panel
      title="Roles"
      flush
      actions={
        canEdit && (
          <Button asChild size="sm">
            <Link to="/admin/roles/new" search={{}}>
              <Plus aria-hidden /> New role
            </Link>
          </Button>
        )
      }
    >
      {roles.isPending && <SkeletonRows rows={6} cols={4} />}
      {roles.isError && <ErrorState error={roles.error} onRetry={() => void roles.refetch()} />}
      {roles.data && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-body">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Role
                </th>
                <th scope="col" className={th}>
                  Given
                </th>
                <th scope="col" className={th}>
                  Permissions
                </th>
                <th scope="col" className={th}>
                  Given to
                </th>
              </tr>
            </thead>
            <tbody>
              {roles.data.items.map((r) => (
                <tr key={r.key}>
                  <td className={`${td} pl-4`}>
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                      <Link
                        to="/admin/roles/$roleKey"
                        params={{ roleKey: r.key }}
                        className="font-medium text-accent hover:underline"
                      >
                        {r.name}
                      </Link>
                      <span className="font-mono text-secondary text-muted">{r.key}</span>
                      {r.built_in ? <Badge>Built in</Badge> : <Badge tone="accent">Custom</Badge>}
                    </div>
                    {r.description && <p className="mt-0.5 text-secondary text-muted">{r.description}</p>}
                  </td>
                  <td className={`${td} text-secondary`}>{scopeText[r.scope]}</td>
                  <td className={`${td} text-secondary text-muted`}>
                    {r.permissions.length === 0 ? "None: everyone signed in holds it" : r.permissions.length}
                    {r.permissions.includes("access.manage") && (
                      <Badge tone="warn" className="ml-1.5">
                        Manages access
                      </Badge>
                    )}
                  </td>
                  <td className={`${td} text-secondary text-muted`}>
                    {r.key === "user"
                      ? "Everyone"
                      : r.holders === 0
                        ? "Nobody"
                        : `${r.holders} ${r.holders === 1 ? "time" : "times"}`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

/** Roles given to users for all of rvav; groups get theirs through a group mapping. */
function ForAllOfRvav({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const list = useQuery(adm.roleAssignments());
  const roles = useQuery(adm.roles());
  const [role, setRole] = useState("auditor");
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
      toast.success(`${a.role.name} taken away from ${a.user.name}`);
    },
    onError: (e) => {
      if (!refused(e)) failed(e, "Not taken away");
    },
  });
  return (
    <Panel title="Given for all of rvav" flush>
      {canEdit && (
        <div className="flex flex-col gap-2 border-b border-line px-4 py-3">
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
      )}
      {list.isPending && <SkeletonRows rows={3} cols={3} />}
      {list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
      {list.data && list.data.items.length === 0 && (
        <EmptyState title="Nobody" body="No role is given to a user for all of rvav." />
      )}
      {list.data && list.data.items.length > 0 && (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-body">
            <thead>
              <tr className="label text-left">
                <th scope="col" className={`${th} pl-4`}>
                  Who
                </th>
                <th scope="col" className={th}>
                  Role
                </th>
                <th scope="col" className={th}>
                  Given
                </th>
                <th scope="col" className={th}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {list.data.items.map((a) => (
                <tr key={a.id ?? `${a.user.id}/${a.role.key}`}>
                  <td className={`${td} pl-4`}>
                    {a.user.name} <span className="text-secondary text-faint">{a.user.email}</span>
                  </td>
                  <td className={td}>
                    <Link
                      to="/admin/roles/$roleKey"
                      params={{ roleKey: a.role.key }}
                      className="text-accent hover:underline"
                    >
                      {a.role.name}
                    </Link>
                  </td>
                  <td className={`${td} text-secondary text-muted`}>
                    {a.fixed ? "Set by rvav" : a.created_at ? <Time iso={a.created_at} /> : ""}
                  </td>
                  <td className={`${td} text-right`}>
                    {canEdit && !a.fixed && (
                      <Button
                        size="sm"
                        variant="quiet"
                        aria-label={`Take ${a.role.name} away from ${a.user.name}`}
                        disabled={remove.isPending}
                        onClick={() => {
                          give.reset();
                          remove.mutate(a);
                        }}
                      >
                        Take away
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <p className="border-t border-line px-4 py-2.5 text-secondary text-muted">
        Groups of the identity provider get a role for all of rvav through a group mapping without a project, in{" "}
        <Link to="/admin/projects" className="text-accent underline underline-offset-2">
          Projects
        </Link>
        . Roles in a project are given on the project&apos;s page.
      </p>
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
  return (
    <Page>
      <PageHeader title="Roles" meta="What each role allows, where it can be given, and who holds it." />
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
          <Roles canEdit={can(me, "access.manage")} />
        </TabsContent>
        <TabsContent value="rvav" className="pt-4">
          <ForAllOfRvav canEdit={can(me, "access.manage")} />
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
