// The audit log (ADR-0020): who did what, when and in which project,
// searched and exported as CSV. Administrators and auditors see all of it,
// project admins their projects' events.
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { Download, ScrollText } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { KeyValues, Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { projectsWith } from "@/lib/can";
import { useDebounced } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { adm, q, type AuditFilters } from "@/lib/query";
import { fullDate } from "@/lib/utils";

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
  { value: "auth.", label: "Sign-ins" },
];

/** The start of a local day, or of the day after it: "To" includes the day chosen. */
function dayStart(day: string, after = false): string {
  const d = new Date(`${day}T00:00`);
  if (after) d.setDate(d.getDate() + 1);
  return d.toISOString();
}

const target = (e: AuditEvent) => (
  <>
    <span className="text-faint">{e.target_type.replace("_", " ")}</span>{" "}
    <span className="font-mono">{e.target_id}</span>
  </>
);

const detailCount = (e: AuditEvent) => Object.keys(e.detail).length;

const value = (v: unknown) =>
  typeof v === "string" ? (
    v
  ) : (
    <pre className="overflow-auto rounded bg-sunken p-2 font-mono text-caption whitespace-pre-wrap">
      {JSON.stringify(v, null, 2)}
    </pre>
  );

function EventSheet({ e, onClose }: { e: AuditEvent; onClose: () => void }) {
  return (
    <Sheet open onOpenChange={(o) => !o && onClose()} title={e.action} description={fullDate(e.at)}>
      <KeyValues
        items={[
          {
            label: "Who",
            value: !e.actor ? "rvav" : e.actor.email ? `${e.actor.name} (${e.actor.email})` : e.actor.name,
          },
          { label: "What", value: <span className="font-mono">{e.action}</span> },
          { label: "On", value: target(e) },
          { label: "Project", value: e.project?.name ?? "None" },
          { label: "Event", value: <span className="font-mono">{e.id}</span> },
        ]}
      />
      <section aria-labelledby="audit-detail" className="flex flex-col gap-2">
        <h3 id="audit-detail" className="label">
          Detail
        </h3>
        {detailCount(e) === 0 ? (
          <p className="text-secondary text-muted">Nothing more was recorded.</p>
        ) : (
          <KeyValues items={Object.entries(e.detail).map(([k, v]) => ({ label: k, value: value(v) }))} />
        )}
      </section>
    </Sheet>
  );
}

const columns = (open: (e: AuditEvent) => void): Column<AuditEvent>[] => [
  {
    id: "when",
    header: "When",
    cell: (e) => (
      <button
        type="button"
        className="text-left text-secondary whitespace-nowrap text-muted hover:text-accent"
        onClick={() => open(e)}
      >
        <span className="sr-only">Open the event from </span>
        <time dateTime={e.at}>{fullDate(e.at)}</time>
      </button>
    ),
  },
  { id: "who", header: "Who", cell: (e) => <span className="text-secondary">{e.actor?.name ?? "rvav"}</span> },
  { id: "what", header: "What", cell: (e) => <span className="font-mono text-secondary">{e.action}</span> },
  { id: "on", header: "On", cell: (e) => <span className="text-secondary">{target(e)}</span> },
  {
    id: "project",
    header: "Project",
    cell: (e) => <span className="text-secondary">{e.project?.name ?? <span className="text-faint">None</span>}</span>,
  },
  {
    id: "detail",
    header: "Detail",
    cell: (e) => (
      <span className="text-secondary text-muted">
        {detailCount(e) === 0 ? "None" : `${detailCount(e)} ${detailCount(e) === 1 ? "detail" : "details"}`}
      </span>
    ),
  },
];

const none = { search: "", kind: "any", project: "any", from: "", to: "" };

export function AuditPage() {
  const { me } = useCurrentProject();
  // Those who read the audit log of every project filter by any, archived
  // ones too; project admins by theirs.
  const scope = projectsWith(me, "audit.read");
  const allProjects = useQuery({ ...adm.projectNames(true), enabled: scope === "all" });
  const projectOptions =
    scope === "all"
      ? (allProjects.data?.items ?? []).map((p) => ({
          value: p.id,
          label: p.archived ? `${p.name} (archived)` : p.name,
        }))
      : scope.map((p) => ({ value: p.id, label: p.name }));
  const [f, setF] = useState(none);
  const [opened, setOpened] = useState<AuditEvent | null>(null);
  const filters: AuditFilters = {
    q: useDebounced(f.search, 300) || undefined,
    action: f.kind === "any" ? undefined : f.kind,
    project_id: f.project === "any" ? undefined : f.project,
    from: f.from ? dayStart(f.from) : undefined,
    to: f.to ? dayStart(f.to, true) : undefined,
  };
  const filtered = (Object.keys(none) as (keyof typeof none)[]).some((k) => f[k] !== none[k]);
  const events = useInfiniteQuery(q.audit(filters));
  const list = events.data?.pages.flatMap((p) => p.items);
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
        description="Every change people and rvav make: catalog, projects, policies, settings and tokens. Newest first."
        actions={
          <Button disabled={exportCsv.isPending} onClick={() => exportCsv.mutate()}>
            <Download />
            {exportCsv.isPending ? "Exporting…" : "Export CSV"}
          </Button>
        }
      />
      <Panel
        flush
        footer={
          events.hasNextPage && (
            <Button size="sm" disabled={events.isFetchingNextPage} onClick={() => void events.fetchNextPage()}>
              {events.isFetchingNextPage ? "Loading…" : "Load older events"}
            </Button>
          )
        }
      >
        <DataTable
          label="Audit events"
          columns={columns(setOpened)}
          rows={list}
          rowKey={(e) => e.id}
          minWidth={820}
          loading={events.isPending}
          error={events.isError && <ErrorState error={events.error} onRetry={() => void events.refetch()} />}
          toolbar={
            <div className="flex w-full flex-wrap items-end gap-2">
              <Field label="Search" htmlFor="audit-search" className="w-56">
                <Input
                  id="audit-search"
                  value={f.search}
                  onChange={(e) => setF({ ...f, search: e.target.value })}
                  placeholder="Who, what, target"
                />
              </Field>
              <Field label="What" htmlFor="audit-kind" className="w-40">
                <Select id="audit-kind" value={f.kind} onChange={(kind) => setF({ ...f, kind })} options={kinds} />
              </Field>
              <Field label="Project" htmlFor="audit-project" className="w-44">
                <Select
                  id="audit-project"
                  value={f.project}
                  onChange={(project) => setF({ ...f, project })}
                  options={[{ value: "any", label: "Any" }, ...projectOptions]}
                />
              </Field>
              <Field label="From" htmlFor="audit-from">
                <Input
                  id="audit-from"
                  type="date"
                  value={f.from}
                  onChange={(e) => setF({ ...f, from: e.target.value })}
                />
              </Field>
              <Field label="To" htmlFor="audit-to">
                <Input id="audit-to" type="date" value={f.to} onChange={(e) => setF({ ...f, to: e.target.value })} />
              </Field>
              {filtered && (
                <Button variant="quiet" onClick={() => setF(none)}>
                  Clear filters
                </Button>
              )}
            </div>
          }
          empty={
            filtered ? (
              <EmptyState
                icon={ScrollText}
                title="Nothing found"
                body="No event matches these filters."
                action={
                  <Button size="sm" onClick={() => setF(none)}>
                    Clear filters
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={ScrollText}
                title="Nothing recorded yet"
                body="Changes to the catalog, projects, policies, settings and tokens show here as people make them."
              />
            )
          }
        />
      </Panel>
      {opened && <EventSheet e={opened} onClose={() => setOpened(null)} />}
    </Page>
  );
}
