// What someone may do, and which of their roles allows it (ADR-0034): the
// answer to "why can't I?", about anyone with access.read, about oneself
// otherwise.
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Fragment, useState } from "react";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { td, th, UserPicker } from "../governance/common";
import { allows } from "./permissions";

type S = components["schemas"];

/** Where a role comes from, in words. */
export function grantText(g: S["AccessGrant"], project: S["ProjectRef"] | null): string {
  const where = g.given === "project" ? `in ${project?.name ?? "the project"}` : "for all of rvav";
  if (g.through === "rvav") return "set by rvav";
  if (g.through === "group") return `through ${g.group ?? "a group"}, ${where}`;
  return `given ${where}`;
}

function RoleName({ role, linked }: { role: S["RoleRef"]; linked: boolean }) {
  if (!linked) return <>{role.name}</>;
  return (
    <Link
      to="/admin/roles/$roleKey"
      params={{ roleKey: role.key }}
      className="text-accent underline underline-offset-2"
    >
      {role.name}
    </Link>
  );
}

type Show = "all" | "held" | "not-held";

/**
 * Every permission, held or not, for someone in a project (or for all of
 * rvav without one), with the roles granting it. `self` words it for the
 * person reading; `linked` links the roles to their pages.
 */
export function AccessExplained({
  userId,
  projectId,
  self,
  linked,
  filters,
}: {
  userId: string;
  projectId?: string;
  self: boolean;
  linked: boolean;
  /** Who and where, in the toolbar before Show. */
  filters?: React.ReactNode;
}) {
  const [show, setShow] = useState<Show>("all");
  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-end gap-2 border-b border-line px-4 py-3">
        {filters}
        <Field label="Show" htmlFor="explain-show" className="w-48">
          <Select
            id="explain-show"
            value={show}
            onChange={(v) => setShow(v as Show)}
            options={[
              { value: "all", label: "Every permission" },
              { value: "held", label: "Those held" },
              { value: "not-held", label: "Those not held" },
            ]}
          />
        </Field>
      </div>
      <Explanation userId={userId} projectId={projectId} self={self} linked={linked} show={show} />
    </div>
  );
}

function Explanation({
  userId,
  projectId,
  self,
  linked,
  show,
}: {
  userId: string;
  projectId?: string;
  self: boolean;
  linked: boolean;
  show: Show;
}) {
  const answer = useQuery(adm.explain(userId, projectId));
  const catalogue = useQuery(adm.permissions());
  if (answer.isError) return <ErrorState error={answer.error} onRetry={() => void answer.refetch()} />;
  if (catalogue.isError) return <ErrorState error={catalogue.error} onRetry={() => void catalogue.refetch()} />;
  if (!answer.data || !catalogue.data) return <SkeletonRows rows={6} cols={3} />;
  const a = answer.data;
  const c = catalogue.data;
  const they = self ? "you" : "they";
  const their = self ? "your" : "their";
  const where = a.project ? `in ${a.project.name}` : "for all of rvav";
  const info = new Map(c.items.map((p) => [p.id, p]));
  const shown = a.permissions.filter((p) => show === "all" || p.held === (show === "held"));
  const areas = new Map<string, S["AccessPermission"][]>();
  for (const p of shown) {
    const area = info.get(p.id)?.area ?? "Day-2 actions";
    areas.set(area, [...(areas.get(area) ?? []), p]);
  }
  return (
    <>
      <div className="flex flex-col gap-2 border-b border-line px-4 py-3 text-body">
        <p>
          {self ? "Your roles" : `${a.user.name}'s roles`} {where}, with the groups of {their} last sign-in
          {a.groups.length > 0 ? `: ${a.groups.join(", ")}.` : " (none)."}
        </p>
        <ul aria-label={`Roles that count ${where}`} className="flex flex-col gap-0.5">
          {a.roles.map((g) => (
            <li key={`${g.role.key}/${g.given}/${g.through}/${g.group ?? ""}`}>
              <RoleName role={g.role} linked={linked} />{" "}
              <span className="text-secondary text-muted">{grantText(g, a.project)}</span>
            </li>
          ))}
        </ul>
        <p className="text-secondary text-muted">
          An API token acts with the roles given to {self ? "you" : "them"} only, never those through a group.
        </p>
      </div>
      <div className="overflow-x-auto">
        <table
          aria-label={`What ${self ? "you" : a.user.name} may do ${where}`}
          className="w-full border-collapse text-body"
        >
          <thead>
            <tr className="label text-left">
              <th scope="col" className={`${th} pl-4`}>
                Permission
              </th>
              <th scope="col" className={th}>
                Held
              </th>
              <th scope="col" className={th}>
                Granted by
              </th>
            </tr>
          </thead>
          <tbody>
            {[...areas.entries()].map(([area, list]) => (
              <Fragment key={area}>
                <tr>
                  <th
                    scope="colgroup"
                    colSpan={3}
                    className={`${td} bg-sunken pl-4 text-left text-secondary font-medium`}
                  >
                    {area}
                  </th>
                </tr>
                {list.map((p) => (
                  <tr key={p.id}>
                    <td className={`${td} pl-4`}>
                      {info.get(p.id)?.summary ?? allows(c, [p.id])[0]?.text ?? p.id}
                      <span className="block font-mono text-secondary text-muted">{p.id}</span>
                    </td>
                    <td className={td}>{p.held ? <Badge tone="ok">Held</Badge> : <Badge>Not held</Badge>}</td>
                    <td className={`${td} text-secondary`}>
                      {p.granted_by.length > 0 ? (
                        <ul className="flex flex-col gap-0.5">
                          {p.granted_by.map((g) => (
                            <li key={`${g.role.key}/${g.given}/${g.through}/${g.group ?? ""}`}>
                              <RoleName role={g.role} linked={linked} />,{" "}
                              <span className="text-muted">{grantText(g, a.project)}</span>
                            </li>
                          ))}
                        </ul>
                      ) : (
                        <span className="text-muted">
                          No role {they} hold {where} grants it.
                          {p.id === "approval.project" && " It counts from a role given in the project only."}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </Fragment>
            ))}
          </tbody>
        </table>
      </div>
    </>
  );
}

/** Administration › Roles › What someone may do: anyone, in a project or for all of rvav. */
export function WhatSomeoneMayDo({
  userId,
  projectId,
  onChange,
}: {
  userId?: string;
  projectId?: string;
  onChange: (s: { user?: string; project?: string }) => void;
}) {
  const { me } = useCurrentProject();
  const projects = useQuery(adm.projectNames());
  const who = userId ?? me.user.id;
  return (
    <Panel
      title="What someone may do"
      description="Each permission, held or not, and which of their roles grants it: the answer to why someone can't."
      flush
    >
      <AccessExplained
        userId={who}
        projectId={projectId}
        self={who === me.user.id}
        linked
        filters={
          <>
            <div className="w-72">
              <UserPicker label="Who" id="explain-user" onPick={(u) => onChange({ user: u.id, project: projectId })} />
            </div>
            <Field label="Where" htmlFor="explain-project" className="w-56">
              <Select
                id="explain-project"
                value={projectId ?? RVAV}
                onChange={(v) => onChange({ user: userId, project: v === RVAV ? undefined : v })}
                loading={projects.isPending}
                options={[
                  { value: RVAV, label: "For all of rvav" },
                  ...(projects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name })),
                ]}
              />
            </Field>
          </>
        }
      />
    </Panel>
  );
}

const RVAV = "rvav";

/** Account › What I may do: the signed-in person, in their projects or for all of rvav. */
export function MyAccessPage({ project }: { project?: string }) {
  const { me, projectId: current } = useCurrentProject();
  const navigate = useNavigate();
  const chosen = project ?? (current || RVAV);
  return (
    <Page>
      <PageHeader title="What I may do" meta="Each permission, held or not, and which of your roles grants it." />
      <Panel title="Your permissions" flush>
        <AccessExplained
          userId={me.user.id}
          projectId={chosen === RVAV ? undefined : chosen}
          self
          linked={can(me, "access.read")}
          filters={
            <Field label="Where" htmlFor="my-access-project" className="w-56">
              <Select
                id="my-access-project"
                value={chosen}
                onChange={(v) => void navigate({ to: "/access", search: { project: v }, replace: true })}
                options={[
                  { value: RVAV, label: "For all of rvav" },
                  ...me.projects.map((p) => ({ value: p.project.id, label: p.project.name })),
                ]}
              />
            </Field>
          }
        />
      </Panel>
    </Page>
  );
}
