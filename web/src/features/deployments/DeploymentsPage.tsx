import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus, SearchX, Server } from "lucide-react";
import { useState } from "react";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, TableSearch, type Column, type Sort } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import type { Deployment, DeploymentStatus } from "@/lib/api/types";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { q, type DeploymentFilters } from "@/lib/query";
import { cn, daysUntil, leaseLabel } from "@/lib/utils";
import { RowActionItems, usePickedAction } from "./actions";
import { BulkDialog, bulkActions } from "./bulk";

export interface DeploymentsSearch {
  status?: "ready" | "provisioning" | "failed";
  env?: string;
  q?: string;
  mine?: boolean;
  expiring?: boolean;
  sort?: DeploymentFilters["sort"];
}

const statuses: Record<NonNullable<DeploymentsSearch["status"]>, { label: string; statuses: DeploymentStatus[] }> = {
  ready: { label: "Ready", statuses: ["ready"] },
  provisioning: { label: "Provisioning", statuses: ["provisioning", "updating"] },
  failed: { label: "Failed", statuses: ["failed"] },
};

// Only what the API sorts by: sorting the loaded pages alone would hide the rest of the list.
const sortKeys: Record<string, "name" | "lease_expires_at"> = { name: "name", lease: "lease_expires_at" };

const toSort = (s: DeploymentsSearch["sort"]): Sort | null => {
  const key = (s ?? "name").replace(/^-/, "");
  const id = Object.keys(sortKeys).find((k) => sortKeys[k] === key);
  return id ? { id, desc: Boolean(s?.startsWith("-")) } : null;
};

const fromSort = (s: Sort | null): DeploymentsSearch["sort"] => {
  const key = s && sortKeys[s.id];
  if (!key || (key === "name" && !s.desc)) return undefined;
  return s.desc ? `-${key}` : key;
};

const weekAhead = () => new Date(Math.ceil(Date.now() / 3_600_000) * 3_600_000 + 7 * 86_400_000).toISOString();

function Dots({ d }: { d: Deployment }) {
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap">
      {Array.from({ length: Math.min(d.vm_count, 6) }, (_, i) => (
        <span
          key={i}
          aria-hidden
          className={cn("size-2 rounded-full", i < d.vms_powered_on ? "bg-ok" : "border border-line-strong")}
        />
      ))}
      <span className="tnum ml-1 text-muted">
        {d.vms_powered_on} of {d.vm_count} on
      </span>
    </span>
  );
}

function Lease({ expiresAt }: { expiresAt: string | null }) {
  const days = expiresAt ? daysUntil(expiresAt) : null;
  return (
    <span
      className={cn(
        "tnum whitespace-nowrap",
        days === null
          ? "text-muted"
          : days <= 1
            ? "font-medium text-bad"
            : days <= 7
              ? "font-medium text-warn"
              : "text-fg",
      )}
    >
      {leaseLabel(expiresAt)}
    </span>
  );
}

const columns: Column<Deployment>[] = [
  {
    id: "name",
    header: "Deployment",
    sortValue: (d) => d.name,
    cell: (d) => (
      <>
        <Link
          to="/deployments/$deploymentId"
          params={{ deploymentId: d.id }}
          className="font-medium text-fg hover:text-accent"
        >
          {d.name}
        </Link>
        <div className="text-caption text-muted">
          {d.catalog_item.name} · {d.version.number}
        </div>
      </>
    ),
  },
  { id: "env", header: "Environment", cell: (d) => <EnvBadge env={d.environment} /> },
  { id: "status", header: "Status", cell: (d) => <DeploymentStatusBadge status={d.status} /> },
  { id: "vms", header: "VMs", cell: (d) => <Dots d={d} /> },
  {
    id: "address",
    header: "Address",
    cell: (d) => (
      <>
        <span className="font-mono text-secondary">
          {d.primary_address?.ip || "–"}
          {d.vm_count > 1 && <span className="text-muted"> +{d.vm_count - 1}</span>}
        </span>
        {d.primary_address?.fqdn && <div className="font-mono text-caption text-muted">{d.primary_address.fqdn}</div>}
      </>
    ),
  },
  { id: "owner", header: "Owner", cell: (d) => <span className="text-muted">{d.owner.name}</span> },
  {
    id: "lease",
    header: "Lease",
    sortValue: (d) => d.lease.expires_at,
    cell: (d) => <Lease expiresAt={d.lease.expires_at} />,
  },
];

export function DeploymentsPage({ search }: { search: DeploymentsSearch }) {
  const { projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/deployments/" });
  // The address keeps the search, without a history entry per keystroke.
  const [text, setText] = useSearchBox(
    search.q,
    (next) => void navigate({ search: (s: DeploymentsSearch) => ({ ...s, q: next }), replace: true }),
  );
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulk, setBulk] = useState<(typeof bulkActions)[number] | null>(null);
  const { pick, dialog } = usePickedAction();
  const environments = useQuery(q.environments());
  const envs = (search.env ?? "").split(",").filter(Boolean);
  const list = useInfiniteQuery(
    q.deployments({
      projectId,
      status: search.status ? statuses[search.status].statuses : undefined,
      environment: envs.length ? envs : undefined,
      owner: search.mine ? "me" : undefined,
      q: search.q,
      leaseExpiresBefore: search.expiring ? weekAhead() : undefined,
      sort: search.sort ?? "name",
    }),
  );
  const filtered = Boolean(search.status || envs.length || search.mine || search.q || search.expiring);

  const set = (patch: Partial<DeploymentsSearch>) =>
    void navigate({ search: (s: DeploymentsSearch) => ({ ...s, ...patch }), replace: true });
  const clear = () => {
    void navigate({ search: (s: DeploymentsSearch) => ({ sort: s.sort }), replace: true });
  };

  const items = list.data?.pages.flatMap((p) => p.items);
  const shown = items ?? [];
  const vms = shown.reduce((n, d) => n + d.vm_count, 0);
  const on = shown.reduce((n, d) => n + d.vms_powered_on, 0);
  const expiring = shown.filter((d) => d.lease.expires_at && daysUntil(d.lease.expires_at) <= 7).length;
  const visibleSelected = shown.filter((d) => selected.has(d.id));
  const envOptions = [
    { value: "any", label: "All environments" },
    ...(environments.data?.items.map((e) => ({ value: e.key, label: e.name, meta: e.key })) ??
      ["dev", "tst", "prd"].map((k) => ({ value: k, label: k }))),
    ...(envs.length > 1 ? [{ value: search.env!, label: envs.join(", ") }] : []),
  ];

  return (
    <Page>
      <PageHeader
        title="Deployments"
        description={`What requests built in ${project.name}. Power state and addresses come from vCenter, live.`}
        meta={
          items &&
          `${shown.length} deployment${shown.length === 1 ? "" : "s"} · ${vms} VMs, ${on} powered on${expiring ? ` · ${expiring} lease${expiring === 1 ? "" : "s"} end within 7 days` : ""}`
        }
        actions={
          <Button variant="primary" asChild>
            <Link to="/catalog">
              <Plus /> New request
            </Link>
          </Button>
        }
      />

      {visibleSelected.length > 0 && (
        <div
          className="flex flex-wrap items-center gap-2 rounded-lg border border-accent bg-accent-soft px-3 py-2"
          role="region"
          aria-label="Bulk actions"
        >
          <span className="font-medium">
            {visibleSelected.length} deployment{visibleSelected.length === 1 ? "" : "s"} selected
          </span>
          <span className="flex-1" />
          {bulkActions.map((b) => (
            <Button
              key={b.key}
              size="sm"
              variant={b.key === "delete" ? "danger" : undefined}
              onClick={() => setBulk(b)}
            >
              {b.label}
            </Button>
          ))}
          <Button size="sm" variant="quiet" onClick={() => setSelected(new Set())}>
            Clear
          </Button>
        </div>
      )}

      <Panel
        flush
        footer={
          list.hasNextPage && (
            <Button size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? "Loading…" : "Load more"}
            </Button>
          )
        }
      >
        <DataTable
          label="Deployments"
          columns={columns}
          rows={items}
          rowKey={(d) => d.id}
          rowLinks
          rowMenu={(d) => <RowActionItems deployment={d} onPick={pick} />}
          rowMenuLabel={(d) => `Actions for ${d.name}`}
          selection={{
            selected,
            onChange: setSelected,
            label: (d) => `Select ${d.name}`,
            allLabel: list.hasNextPage ? "Select all shown" : "Select all",
          }}
          sort={toSort(search.sort)}
          onSortChange={(s) => set({ sort: fromSort(s) })}
          minWidth={980}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          toolbar={
            <>
              <TableSearch label="Search deployments" placeholder="Name, IP or owner" value={text} onChange={setText} />
              <Select
                aria-label="Status"
                value={search.status ?? "any"}
                onChange={(v) => set({ status: v === "any" ? undefined : (v as DeploymentsSearch["status"]) })}
                options={[
                  { value: "any", label: "All statuses" },
                  ...Object.entries(statuses).map(([k, s]) => ({ value: k, label: s.label })),
                ]}
                className="w-40"
              />
              <Select
                aria-label="Environment"
                value={search.env ?? "any"}
                onChange={(v) => set({ env: v === "any" ? undefined : v })}
                options={envOptions}
                className="w-48"
              />
              <span className="flex items-center gap-2 text-body">
                <Checkbox
                  id="deployments-expiring"
                  checked={Boolean(search.expiring)}
                  onChange={(v) => set({ expiring: v || undefined })}
                />
                <label htmlFor="deployments-expiring">Lease ends within 7 days</label>
              </span>
              <span className="flex items-center gap-2 text-body">
                <Checkbox
                  id="deployments-mine"
                  checked={Boolean(search.mine)}
                  onChange={(v) => set({ mine: v || undefined })}
                />
                <label htmlFor="deployments-mine">Only mine</label>
              </span>
              {filtered && (
                <Button variant="quiet" onClick={clear}>
                  Clear filters
                </Button>
              )}
            </>
          }
          empty={
            filtered ? (
              <EmptyState
                icon={SearchX}
                title="No deployments match"
                body="Try other words, or another status or environment."
                action={
                  <Button size="sm" onClick={clear}>
                    Clear filters
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={Server}
                title={`Nothing deployed in ${project.name} yet`}
                body="What your requests build shows here, with its VMs, addresses and lease."
                action={
                  <Button size="sm" asChild>
                    <Link to="/catalog">Request something from the catalog</Link>
                  </Button>
                }
              />
            )
          }
        />
      </Panel>
      {dialog}
      {bulk && (
        <BulkDialog
          bulk={bulk}
          deployments={visibleSelected}
          onClose={(done) => {
            setBulk(null);
            if (done) setSelected(new Set());
          }}
        />
      )}
    </Page>
  );
}
