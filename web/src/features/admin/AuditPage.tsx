// The audit log (ADR-0020): who did what, when and in which project,
// searched and exported as CSV. Administrators and auditors see all of it,
// project admins their projects' events.
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { useDebounced } from "@/lib/hooks";
import { adm, q, type AuditFilters } from "@/lib/query";
import { useCurrentProject } from "@/lib/project";

type AuditEvent = components["schemas"]["AuditEvent"];

const kinds = [
  { value: "any", label: "Anything" },
  { value: "catalog.", label: "Catalog" },
  { value: "project.", label: "Projects" },
  { value: "member.", label: "Members" },
  { value: "group_mapping.", label: "Group mappings" },
  { value: "environment.", label: "Environments" },
  { value: "placement.", label: "Placement" },
  { value: "network.", label: "Network mappings" },
  { value: "template.", label: "Templates" },
  { value: "size.", label: "Sizes" },
  { value: "policy.", label: "Policies" },
  { value: "naming.", label: "Naming" },
  { value: "settings.", label: "Settings" },
  { value: "token.", label: "API tokens" },
];

/** The start of a local day, or of the day after it: "To" includes the day chosen. */
function dayStart(day: string, after = false): string {
  const d = new Date(`${day}T00:00`);
  if (after) d.setDate(d.getDate() + 1);
  return d.toISOString();
}

const time = (at: string) => new Date(at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });

function Detail({ e }: { e: AuditEvent }) {
  const entries = Object.entries(e.detail);
  if (entries.length === 0) return <span className="text-faint">—</span>;
  return (
    <details>
      <summary className="cursor-pointer text-xs text-muted">
        {entries.length} detail{entries.length > 1 ? "s" : ""}
      </summary>
      <pre className="mt-1 max-w-md overflow-auto rounded bg-sunken p-2 font-mono text-[11px] whitespace-pre-wrap">
        {JSON.stringify(e.detail, null, 2)}
      </pre>
    </details>
  );
}

export function AuditPage() {
  const { me } = useCurrentProject();
  // Administrators and auditors filter by any project; project admins by theirs.
  const seesAll = me.global_roles.includes("admin") || me.global_roles.includes("auditor");
  const allProjects = useQuery({ ...adm.projects("", true), enabled: seesAll });
  const projectOptions = seesAll
    ? (allProjects.data?.items ?? []).map((p) => ({ value: p.id, label: p.name }))
    : me.projects
        .filter((p) => p.role === "project-admin")
        .map((p) => ({ value: p.project.id, label: p.project.name }));
  const [search, setSearch] = useState("");
  const [kind, setKind] = useState("any");
  const [project, setProject] = useState("any");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const filters: AuditFilters = {
    q: useDebounced(search, 300) || undefined,
    action: kind === "any" ? undefined : kind,
    project_id: project === "any" ? undefined : project,
    from: from ? dayStart(from) : undefined,
    to: to ? dayStart(to, true) : undefined,
  };
  const events = useInfiniteQuery(q.audit(filters));
  const list = events.data?.pages.flatMap((p) => p.items) ?? [];
  const exportCsv = useMutation({
    mutationFn: async () => {
      const text = await call(api.GET("/audit", { params: { query: { ...filters, format: "csv" } }, parseAs: "text" }));
      const url = URL.createObjectURL(new Blob([String(text)], { type: "text/csv" }));
      Object.assign(document.createElement("a"), { href: url, download: "rvav-audit.csv" }).click();
      URL.revokeObjectURL(url);
    },
    onError: (e) => toast.error(isApiError(e) ? e.problem.title : "The export failed"),
  });
  return (
    <Page>
      <PageHeader
        title="Audit log"
        meta="Every change people and rvav make: catalog, projects, policies, settings and tokens."
        actions={
          <Button size="sm" disabled={exportCsv.isPending} onClick={() => exportCsv.mutate()}>
            {exportCsv.isPending ? "Exporting…" : "Export CSV"}
          </Button>
        }
      />
      <Panel flush>
        <div className="flex flex-wrap items-end gap-2 border-b border-line px-4 py-3">
          <Field label="Search" htmlFor="audit-search" className="w-56">
            <Input
              id="audit-search"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Who, what, target"
            />
          </Field>
          <Field label="What" htmlFor="audit-kind" className="w-40">
            <Select id="audit-kind" value={kind} onChange={setKind} options={kinds} />
          </Field>
          <Field label="Project" htmlFor="audit-project" className="w-44">
            <Select
              id="audit-project"
              value={project}
              onChange={setProject}
              options={[{ value: "any", label: "Any" }, ...projectOptions]}
            />
          </Field>
          <Field label="From" htmlFor="audit-from">
            <Input id="audit-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
          </Field>
          <Field label="To" htmlFor="audit-to">
            <Input id="audit-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} />
          </Field>
        </div>
        {events.isPending && <SkeletonRows rows={6} cols={5} />}
        {events.isError && <ErrorState error={events.error} onRetry={() => void events.refetch()} />}
        {events.data && list.length === 0 && (
          <EmptyState title="Nothing found" body="No event matches these filters." />
        )}
        {list.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] border-collapse text-[13px]">
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                    When
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    Who
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    What
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    On
                  </th>
                  <th scope="col" className="border-b border-line px-3 py-2 font-medium">
                    Project
                  </th>
                  <th scope="col" className="border-b border-line px-4 py-2 font-medium">
                    Detail
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((e) => (
                  <tr key={e.id} className="align-top hover:bg-sunken">
                    <td className="border-b border-line px-4 py-2 text-xs whitespace-nowrap text-muted">
                      {time(e.at)}
                    </td>
                    <td className="border-b border-line px-3 py-2 text-xs">{e.actor?.name ?? "rvav"}</td>
                    <td className="border-b border-line px-3 py-2 font-mono text-xs">{e.action}</td>
                    <td className="border-b border-line px-3 py-2 text-xs">
                      <span className="text-faint">{e.target_type.replace("_", " ")}</span>{" "}
                      <span className="font-mono">{e.target_id}</span>
                    </td>
                    <td className="border-b border-line px-3 py-2 text-xs">{e.project?.name ?? "—"}</td>
                    <td className="border-b border-line px-4 py-2">
                      <Detail e={e} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {events.hasNextPage && (
          <div className="px-4 py-3">
            <Button size="sm" disabled={events.isFetchingNextPage} onClick={() => void events.fetchNextPage()}>
              {events.isFetchingNextPage ? "Loading…" : "Load older events"}
            </Button>
          </div>
        )}
      </Panel>
    </Page>
  );
}
