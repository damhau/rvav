// What someone may do, and which of their roles allows it (ADR-0034): the
// answer to "why can't I?", about anyone with access.read, about oneself
// otherwise.
import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Fragment, useState } from "react";
import { useTranslation } from "react-i18next";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { ScrollRegion } from "@/components/ui/scroll-region";
import { Badge } from "@/components/ui/badge";
import { Field } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { formatList } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { td, th, UserPicker } from "../governance/common";
import { allows } from "./permissions";
import { areaName, permissionSummary, roleName } from "@/lib/i18n/names";

type S = components["schemas"];

/** Where a role comes from, in words. */
export function grantText(g: S["AccessGrant"], project: S["ProjectRef"] | null): string {
  const t = i18n.getFixedT(null, "admin-access");
  if (g.through === "rvav") return t("grant.rvav");
  const inProject = g.given === "project";
  const values = { project: project?.name ?? t("grant.theProject"), group: g.group ?? t("grant.aGroup") };
  if (g.through === "group") return t(inProject ? "grant.groupInProject" : "grant.groupForRvav", values);
  return t(inProject ? "grant.givenInProject" : "grant.givenForRvav", values);
}

function RoleName({ role, linked }: { role: S["RoleRef"]; linked: boolean }) {
  if (!linked) return <>{roleName(role)}</>;
  return (
    <Link
      to="/admin/roles/$roleKey"
      params={{ roleKey: role.key }}
      className="text-accent underline underline-offset-2"
    >
      {roleName(role)}
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
  const { t } = useTranslation("admin-access");
  const [show, setShow] = useState<Show>("all");
  return (
    <div className="flex flex-col">
      <div className="flex flex-wrap items-end gap-2 border-b border-line px-4 py-3">
        {filters}
        <Field label={t("explain.show")} htmlFor="explain-show" className="w-48">
          <Select
            id="explain-show"
            value={show}
            onChange={(v) => setShow(v as Show)}
            options={[
              { value: "all", label: t("explain.showAll") },
              { value: "held", label: t("explain.showHeld") },
              { value: "not-held", label: t("explain.showNotHeld") },
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
  const { t } = useTranslation("admin-access");
  const answer = useQuery(adm.explain(userId, projectId));
  const catalogue = useQuery(adm.permissions());
  if (answer.isError) return <ErrorState error={answer.error} onRetry={() => void answer.refetch()} />;
  if (catalogue.isError) return <ErrorState error={catalogue.error} onRetry={() => void catalogue.refetch()} />;
  if (!answer.data || !catalogue.data) return <SkeletonRows rows={6} cols={3} />;
  const a = answer.data;
  const c = catalogue.data;
  const where = a.project ? t("explain.inProject", { project: a.project.name }) : t("explain.forRvav");
  const tableName = self ? t("explain.tableSelf", { where }) : t("explain.tableOther", { name: a.user.name, where });
  const groups = formatList(a.groups);
  const info = new Map(c.items.map((p) => [p.id, p]));
  const summaryOf = (p: S["PermissionInfo"] | undefined) => p && permissionSummary(p);
  const shown = a.permissions.filter((p) => show === "all" || p.held === (show === "held"));
  const areas = new Map<string, S["AccessPermission"][]>();
  for (const p of shown) {
    const known = info.get(p.id)?.area;
    const area = known ? areaName(known) : t("explain.day2Area");
    areas.set(area, [...(areas.get(area) ?? []), p]);
  }
  return (
    <>
      <div className="flex flex-col gap-2 border-b border-line px-4 py-3 text-body">
        <p>
          {a.groups.length > 0
            ? t(self ? "explain.rolesSelf" : "explain.rolesOther", { name: a.user.name, where, groups })
            : t(self ? "explain.rolesSelfNone" : "explain.rolesOtherNone", { name: a.user.name, where })}
        </p>
        <ul aria-label={t("explain.rolesThatCount", { where })} className="flex flex-col gap-0.5">
          {a.roles.map((g) => (
            <li key={`${g.role.key}/${g.given}/${g.through}/${g.group ?? ""}`}>
              <RoleName role={g.role} linked={linked} />{" "}
              <span className="text-secondary text-muted">{grantText(g, a.project)}</span>
            </li>
          ))}
        </ul>
        <p className="text-secondary text-muted">{t(self ? "explain.tokenSelf" : "explain.tokenOther")}</p>
      </div>
      <ScrollRegion label={tableName}>
        <table aria-label={tableName} className="w-full border-collapse text-body">
          <thead>
            <tr className="label text-left">
              <th scope="col" className={`${th} pl-4`}>
                {t("explain.permission")}
              </th>
              <th scope="col" className={th}>
                {t("explain.held")}
              </th>
              <th scope="col" className={th}>
                {t("explain.grantedBy")}
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
                      {summaryOf(info.get(p.id)) ?? allows(c, [p.id])[0]?.text ?? p.id}
                      <span className="block font-mono text-secondary text-muted">{p.id}</span>
                    </td>
                    <td className={td}>
                      {p.held ? <Badge tone="ok">{t("explain.held")}</Badge> : <Badge>{t("explain.notHeld")}</Badge>}
                    </td>
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
                          {t(self ? "explain.noRoleSelf" : "explain.noRoleOther", { where })}
                          {p.id === "approval.project" && <> {t("explain.projectOnly")}</>}
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </Fragment>
            ))}
          </tbody>
        </table>
      </ScrollRegion>
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
  const { t } = useTranslation("admin-access");
  const { me } = useCurrentProject();
  const projects = useQuery(adm.projectNames());
  const who = userId ?? me.user.id;
  return (
    <Panel title={t("explain.someone")} description={t("explain.someoneLead")} flush>
      <AccessExplained
        userId={who}
        projectId={projectId}
        self={who === me.user.id}
        linked
        filters={
          <>
            <div className="w-72">
              <UserPicker
                label={t("explain.who")}
                id="explain-user"
                onPick={(u) => onChange({ user: u.id, project: projectId })}
              />
            </div>
            <Field label={t("explain.where")} htmlFor="explain-project" className="w-56">
              <Select
                id="explain-project"
                value={projectId ?? RVAV}
                onChange={(v) => onChange({ user: userId, project: v === RVAV ? undefined : v })}
                loading={projects.isPending}
                options={[
                  { value: RVAV, label: t("explain.forAllOfRvav") },
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
  const { t } = useTranslation(["admin-access", "shell"]);
  const { me, projectId: current } = useCurrentProject();
  const navigate = useNavigate();
  const chosen = project ?? (current || RVAV);
  return (
    <Page>
      <PageHeader title={t("shell:topBar.whatIMayDo")} description={t("explain.mineLead")} />
      <Panel title={t("explain.mine")} flush>
        <AccessExplained
          userId={me.user.id}
          projectId={chosen === RVAV ? undefined : chosen}
          self
          linked={can(me, "access.read")}
          filters={
            <Field label={t("explain.where")} htmlFor="my-access-project" className="w-56">
              <Select
                id="my-access-project"
                value={chosen}
                onChange={(v) => void navigate({ to: "/access", search: { project: v }, replace: true })}
                options={[
                  { value: RVAV, label: t("explain.forAllOfRvav") },
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
