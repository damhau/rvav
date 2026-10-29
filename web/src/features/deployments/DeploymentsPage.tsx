import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useEffect, useState } from "react";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Tip } from "@/components/ui/tooltip";
import type { Deployment, DeploymentStatus } from "@/lib/api/types";
import { useCurrentProject } from "@/lib/project";
import { q, type DeploymentFilters } from "@/lib/query";
import { cn, daysUntil, leaseLabel } from "@/lib/utils";
import { RowActions } from "./actions";
import { BulkDialog, bulkActions } from "./bulk";

export interface DeploymentsSearch {
  status?: string;
  env?: string;
  q?: string;
  mine?: boolean;
  expiring?: boolean;
  sort?: DeploymentFilters["sort"];
}

const statusFilters: { key: string; label: string; statuses?: DeploymentStatus[] }[] = [
  { key: "all", label: "All" },
  { key: "ready", label: "Ready", statuses: ["ready"] },
  { key: "provisioning", label: "Provisioning", statuses: ["provisioning", "updating"] },
  { key: "failed", label: "Failed", statuses: ["failed"] },
];

const weekAhead = () => new Date(Math.ceil(Date.now() / 3_600_000) * 3_600_000 + 7 * 86_400_000).toISOString();

const chip = (on: boolean) =>
  cn(
    "inline-flex h-7 items-center gap-1.5 rounded-full border px-2.5",
    on
      ? "border-fg bg-fg text-bg hover:text-bg"
      : "border-line bg-surface text-muted hover:border-line-strong hover:text-fg",
  );

function Dots({ d }: { d: Deployment }) {
  return (
    <span className="inline-flex items-center gap-1" title={`${d.vms_powered_on} of ${d.vm_count} powered on`}>
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
        "tnum",
        days === null
          ? "text-faint"
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

const Slice2 = ({ children }: { children: React.ReactNode }) => (
  <Tip content="Adopting existing VMs comes later">
    <span>{children}</span>
  </Tip>
);

export function DeploymentsPage({ search }: { search: DeploymentsSearch }) {
  const { projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/deployments/" });
  const [text, setText] = useState(search.q ?? "");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulk, setBulk] = useState<(typeof bulkActions)[number] | null>(null);
  const status = statusFilters.find((f) => f.key === (search.status ?? "all")) ?? statusFilters[0]!;
  const envs = (search.env ?? "").split(",").filter(Boolean);
  const list = useInfiniteQuery(
    q.deployments({
      projectId,
      status: status.statuses,
      environment: envs.length ? envs : undefined,
      owner: search.mine ? "me" : undefined,
      q: search.q,
      leaseExpiresBefore: search.expiring ? weekAhead() : undefined,
      sort: search.sort ?? "name",
    }),
  );

  useEffect(() => {
    const t = setTimeout(() => {
      const next = text.trim() || undefined;
      if (next !== search.q) void navigate({ search: (s: DeploymentsSearch) => ({ ...s, q: next }), replace: true });
    }, 250);
    return () => clearTimeout(t);
  }, [text, search.q, navigate]);

  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const vms = items.reduce((n, d) => n + d.vm_count, 0);
  const on = items.reduce((n, d) => n + d.vms_powered_on, 0);
  const expiring = items.filter((d) => d.lease.expires_at && daysUntil(d.lease.expires_at) <= 7).length;
  const visibleSelected = items.filter((d) => selected.has(d.id));
  const toggleEnv = (e: string) => {
    const next = envs.includes(e) ? envs.filter((x) => x !== e) : [...envs, e];
    return next.length ? next.join(",") : undefined;
  };

  return (
    <Page>
      <PageHeader
        title="Deployments"
        meta={
          list.data
            ? `${items.length} deployments · ${vms} VMs, ${on} powered on${expiring ? ` · ${expiring} lease${expiring === 1 ? "" : "s"} end within 7 days` : ""}`
            : `Everything created in ${project.name}`
        }
        actions={
          <>
            <Slice2>
              <Button disabled>Adopt existing VMs</Button>
            </Slice2>
            <Button variant="primary" asChild>
              <Link to="/catalog">
                <Plus /> New request
              </Link>
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap items-center gap-2">
        <label htmlFor="deployments-search" className="sr-only">
          Filter deployments by name, IP or owner
        </label>
        <Input
          id="deployments-search"
          type="search"
          placeholder="Name, IP or owner"
          value={text}
          onChange={(e) => setText(e.target.value)}
          className="w-64 max-w-full"
        />
        <div role="group" aria-label="Status" className="flex flex-wrap gap-1.5">
          {statusFilters.map((f) => (
            <Link
              key={f.key}
              to="/deployments"
              search={(s: DeploymentsSearch) => ({ ...s, status: f.key === "all" ? undefined : f.key })}
              aria-current={f.key === status.key ? "true" : undefined}
              className={chip(f.key === status.key)}
            >
              {f.label}
            </Link>
          ))}
        </div>
        <div role="group" aria-label="Environment" className="flex flex-wrap gap-1.5">
          {["dev", "tst", "prd"].map((e) => (
            <Link
              key={e}
              to="/deployments"
              search={(s: DeploymentsSearch) => ({ ...s, env: toggleEnv(e) })}
              aria-current={envs.includes(e) ? "true" : undefined}
              className={cn(chip(envs.includes(e)), "font-mono text-[11px] uppercase")}
            >
              {e}
            </Link>
          ))}
        </div>
        <Link
          to="/deployments"
          search={(s: DeploymentsSearch) => ({ ...s, expiring: s.expiring ? undefined : true })}
          aria-current={search.expiring ? "true" : undefined}
          className={chip(Boolean(search.expiring))}
        >
          Lease ends ≤ 7 days
        </Link>
        <label className="ml-auto flex items-center gap-2 text-xs text-muted">
          <input
            type="checkbox"
            checked={Boolean(search.mine)}
            onChange={(e) =>
              void navigate({ search: (s: DeploymentsSearch) => ({ ...s, mine: e.target.checked || undefined }) })
            }
            className="size-3.5 accent-[var(--accent)]"
          />
          Only mine
        </label>
      </div>

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
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            Clear
          </Button>
        </div>
      )}

      <div className="overflow-x-auto rounded-lg border border-line bg-surface">
        {list.isPending ? (
          <SkeletonRows rows={7} cols={6} />
        ) : list.isError ? (
          <ErrorState error={list.error} onRetry={() => void list.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState
            title="No deployments match these filters"
            body={`Clear the search or choose “All” to see every deployment in ${project.name}.`}
          />
        ) : (
          <table className="w-full min-w-[980px] border-collapse">
            <thead>
              <tr className="label text-left">
                <th scope="col" className="w-9 border-b border-line py-2.5 pl-3.5">
                  <input
                    type="checkbox"
                    aria-label="Select all"
                    checked={visibleSelected.length === items.length}
                    onChange={(e) => setSelected(e.target.checked ? new Set(items.map((d) => d.id)) : new Set())}
                    className="size-3.5 accent-[var(--accent)]"
                  />
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Deployment
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Env
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Status
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  VMs
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Address
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Owner
                </th>
                <th scope="col" className="border-b border-line px-3 py-2.5 font-medium">
                  Lease
                </th>
                <th scope="col" className="w-11 border-b border-line py-2.5 pr-3.5">
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {items.map((d) => {
                const isSel = selected.has(d.id);
                return (
                  <tr key={d.id} className={cn(isSel ? "bg-accent-soft" : "hover:bg-sunken")}>
                    <td className="border-b border-line py-2.5 pl-3.5">
                      <input
                        type="checkbox"
                        aria-label={`Select ${d.name}`}
                        checked={isSel}
                        onChange={() =>
                          setSelected((s) => {
                            const n = new Set(s);
                            if (n.has(d.id)) n.delete(d.id);
                            else n.add(d.id);
                            return n;
                          })
                        }
                        className="size-3.5 accent-[var(--accent)]"
                      />
                    </td>
                    <td className="border-b border-line px-3 py-2.5">
                      <Link
                        to="/deployments/$deploymentId"
                        params={{ deploymentId: d.id }}
                        className="font-medium text-fg hover:text-accent"
                      >
                        {d.name}
                      </Link>
                      <div className="text-[11px] text-faint">
                        {d.catalog_item.name} · {d.version.number}
                      </div>
                    </td>
                    <td className="border-b border-line px-3 py-2.5">
                      <EnvBadge env={d.environment} />
                    </td>
                    <td className="border-b border-line px-3 py-2.5">
                      <DeploymentStatusBadge status={d.status} />
                    </td>
                    <td className="border-b border-line px-3 py-2.5">
                      <Dots d={d} />
                    </td>
                    <td className="border-b border-line px-3 py-2.5">
                      <span className="font-mono text-[12px]">
                        {d.primary_address?.ip || "–"}
                        {d.vm_count > 1 && <span className="text-faint"> +{d.vm_count - 1}</span>}
                      </span>
                      {d.primary_address?.fqdn && (
                        <div className="font-mono text-[11px] text-faint">{d.primary_address.fqdn}</div>
                      )}
                    </td>
                    <td className="border-b border-line px-3 py-2.5 text-muted">{d.owner.name}</td>
                    <td className="border-b border-line px-3 py-2.5">
                      <Lease expiresAt={d.lease.expires_at} />
                    </td>
                    <td className="border-b border-line py-2.5 pr-3.5 text-right">
                      <RowActions deployment={d} />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>
      {list.hasNextPage && (
        <Button className="self-start" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
          {list.isFetchingNextPage ? "Loading…" : "Load more"}
        </Button>
      )}
      <p className="text-xs text-faint">Power state and addresses come from vCenter and update live.</p>
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
