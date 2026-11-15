// Administration › Roles (ADR-0034): the built-in roles and the custom ones,
// what each allows and where it can be given.
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page, PageHeader, Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { td, th } from "../governance/common";
import { scopeText } from "./permissions";

function Roles({ canEdit }: { canEdit: boolean }) {
  const roles = useQuery(adm.roles());
  return (
    <Panel
      title="Roles"
      flush
      actions={
        canEdit && (
          <Button asChild size="sm">
            <Link to="/admin/roles/new">
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
          <table className="w-full border-collapse text-[13px]">
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
                      <span className="font-mono text-xs text-muted">{r.key}</span>
                      {r.built_in ? <Badge>Built in</Badge> : <Badge tone="accent">Custom</Badge>}
                    </div>
                    {r.description && <p className="mt-0.5 text-xs text-muted">{r.description}</p>}
                  </td>
                  <td className={`${td} text-xs`}>{scopeText[r.scope]}</td>
                  <td className={`${td} text-xs text-muted`}>
                    {r.permissions.length === 0 ? "None: everyone signed in holds it" : r.permissions.length}
                    {r.permissions.includes("access.manage") && (
                      <Badge tone="warn" className="ml-1.5">
                        Manages access
                      </Badge>
                    )}
                  </td>
                  <td className={`${td} text-xs text-muted`}>
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

export function RolesPage() {
  const { me } = useCurrentProject();
  return (
    <Page>
      <PageHeader title="Roles" meta="What each role allows, where it can be given, and who holds it." />
      <Roles canEdit={can(me, "access.manage")} />
    </Page>
  );
}
