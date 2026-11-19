import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { HardDrive, SearchX } from "lucide-react";
import { useState } from "react";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { EnvBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { DataTable, TableSearch, type Column, type Sort } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import type { VmListItem, VmPowerState } from "@/lib/api/types";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { q, type VmSort } from "@/lib/query";
import { cn } from "@/lib/utils";
import { SavedViewsMenu, ScopeToggle, toQuery, type ListQuery } from "@/features/deployments/listing";
import { VmPropertiesSheet } from "@/features/deployments/VmProperties";

export interface ResourcesSearch {
  /** Environment keys, separated by commas. */
  env?: string;
  power?: VmPowerState;
  vcenter?: string;
  cluster?: string;
  network?: string;
  q?: string;
  sort?: VmSort;
  /** `all`: every project the person is in, instead of the one they work in. */
  project?: "all";
}

export const vmSorts = [
  "name",
  "-name",
  "deployment",
  "-deployment",
  "project",
  "-project",
  "environment",
  "-environment",
  "power_state",
  "-power_state",
  "cpu_count",
  "-cpu_count",
  "memory_mb",
  "-memory_mb",
  "disk_gb",
  "-disk_gb",
] as const satisfies readonly VmSort[];

const powers: Record<VmPowerState, string> = { on: "Powered on", off: "Powered off", suspended: "Suspended" };
const shortPower: Record<VmPowerState, string> = { on: "On", off: "Off", suspended: "Suspended" };

// Each sortable column's sort: the API sorts the whole list, not the pages loaded.
const sortKeys: Record<string, string> = {
  name: "name",
  power: "power_state",
  deployment: "deployment",
  project: "project",
  env: "environment",
  cpu: "cpu_count",
  memory: "memory_mb",
  disks: "disk_gb",
};

const toSort = (s: ResourcesSearch["sort"]): Sort | null => {
  const key = (s ?? "name").replace(/^-/, "");
  const id = Object.keys(sortKeys).find((k) => sortKeys[k] === key);
  return id ? { id, desc: Boolean(s?.startsWith("-")) } : null;
};

const fromSort = (s: Sort | null): ResourcesSearch["sort"] => {
  const key = s && sortKeys[s.id];
  if (!key || (key === "name" && !s.desc)) return undefined;
  return (s.desc ? `-${key}` : key) as VmSort;
};

const gb = (mb: number) => `${Math.round((mb / 1024) * 10) / 10} GB`;
const diskGb = (v: VmListItem) => v.vm.disks.reduce((n, d) => n + d.capacity_gb, 0);
const mono = "font-mono text-secondary";

function columnsFor(all: boolean, open: (v: VmListItem) => void): Column<VmListItem>[] {
  return [
    {
      id: "name",
      className: "whitespace-nowrap",
      header: "VM",
      sortValue: (v) => v.name,
      cell: (v) => (
        <>
          <button
            type="button"
            onClick={() => open(v)}
            className="text-left font-medium text-fg underline-offset-2 hover:text-accent hover:underline"
          >
            {v.name}
          </button>
          {v.vm.fqdn && (
            <div className="max-w-52 truncate font-mono text-caption text-muted" title={v.vm.fqdn}>
              {v.vm.fqdn}
            </div>
          )}
        </>
      ),
    },
    {
      id: "power",
      header: "Power",
      sortValue: (v) => v.vm.power_state,
      cell: (v) => (
        <span className="inline-flex items-center gap-1.5 text-secondary text-muted">
          <span
            aria-hidden
            className={cn(
              "size-2 shrink-0 rounded-full",
              v.vm.power_state === "on"
                ? "bg-ok"
                : v.vm.power_state === "suspended"
                  ? "bg-warn"
                  : "border border-line-strong",
            )}
          />
          {shortPower[v.vm.power_state]}
        </span>
      ),
      className: "whitespace-nowrap",
    },
    {
      id: "addresses",
      className: "whitespace-nowrap",
      header: "Addresses",
      cell: (v) =>
        v.addresses.length === 0 ? (
          <span className="text-muted">–</span>
        ) : (
          <ul className={mono}>
            {v.addresses.map((a) => (
              <li key={a}>{a}</li>
            ))}
          </ul>
        ),
    },
    {
      id: "deployment",
      className: "whitespace-nowrap",
      header: "Deployment",
      sortValue: (v) => v.deployment.name,
      cell: (v) => (
        <Link
          to="/deployments/$deploymentId"
          params={{ deploymentId: v.deployment.id }}
          className="text-fg underline-offset-2 hover:text-accent hover:underline"
        >
          {v.deployment.name}
        </Link>
      ),
    },
    ...(all
      ? [
          {
            id: "project",
            header: "Project",
            sortValue: (v: VmListItem) => v.project.name,
            cell: (v: VmListItem) => <span className="text-muted">{v.project.name}</span>,
          },
        ]
      : []),
    {
      id: "env",
      header: "Environment",
      sortValue: (v) => v.environment,
      cell: (v) => <EnvBadge env={v.environment} />,
    },
    {
      id: "placement",
      className: "[overflow-wrap:anywhere]",
      header: "Placement",
      cell: (v) => (
        <>
          <div className={mono}>{v.vm.vcenter}</div>
          {v.vm.cluster && <div className="font-mono text-caption text-muted">{v.vm.cluster}</div>}
        </>
      ),
    },
    {
      id: "cpu",
      header: "vCPU",
      align: "end",
      sortValue: (v) => v.vm.cpu_count,
      cell: (v) => <span className="tnum">{v.vm.cpu_count}</span>,
    },
    {
      id: "memory",
      header: "Memory",
      align: "end",
      sortValue: (v) => v.vm.memory_mb,
      cell: (v) => <span className="tnum whitespace-nowrap">{gb(v.vm.memory_mb)}</span>,
    },
    {
      id: "disks",
      header: "Disks",
      align: "end",
      sortValue: diskGb,
      cell: (v) => (
        <span className="tnum whitespace-nowrap">
          {v.vm.disks.length} · {diskGb(v)} GB
        </span>
      ),
    },
    {
      id: "networks",
      className: "[overflow-wrap:anywhere]",
      header: "Networks",
      cell: (v) => (
        <ul className={mono}>
          {[...new Set(v.vm.nics.map((n) => n.network))].map((n) => (
            <li key={n}>{n}</li>
          ))}
        </ul>
      ),
    },
  ];
}

/** The count above the table: the rows loaded, said as such while more pages remain. */
export function vmCount(n: number, on: number, more: boolean) {
  if (more) return `${n} shown · ${on} of them powered on`;
  return `${n} VM${n === 1 ? "" : "s"} · ${on} powered on`;
}

export function ResourcesPage({ search }: { search: ResourcesSearch }) {
  const { me, projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/resources" });
  const [text, setText] = useSearchBox(
    search.q,
    (next) => void navigate({ search: (s: ResourcesSearch) => ({ ...s, q: next }), replace: true }),
  );
  const [shown, setShown] = useState<VmListItem | null>(null);
  const all = search.project === "all";
  const scope = all ? "" : projectId;
  const environments = useQuery(q.environments());
  const ownProject = useQuery({ ...q.project(projectId), enabled: !all && Boolean(projectId) });
  const facets = useQuery(q.vmFacets(scope));
  const envs = (search.env ?? "").split(",").filter(Boolean);
  const list = useInfiniteQuery(
    q.vms({
      projectId: scope,
      environment: envs.length ? envs : undefined,
      powerState: search.power ? [search.power] : undefined,
      vcenter: search.vcenter,
      cluster: search.cluster,
      network: search.network,
      q: search.q,
      sort: search.sort ?? "name",
    }),
  );
  const filtered = Boolean(
    envs.length || search.power || search.vcenter || search.cluster || search.network || search.q,
  );

  const set = (patch: Partial<ResourcesSearch>) =>
    void navigate({ search: (s: ResourcesSearch) => ({ ...s, ...patch }), replace: true });
  const clear = () =>
    void navigate({ search: (s: ResourcesSearch) => ({ sort: s.sort, project: s.project }), replace: true });
  const apply = (query: ListQuery) => void navigate({ search: query as ResourcesSearch });

  const items = list.data?.pages.flatMap((p) => p.items);
  const rows = items ?? [];
  const on = rows.filter((v) => v.vm.power_state === "on").length;
  const envName = (key: string) => environments.data?.items.find((e) => e.key === key)?.name ?? key;
  const envKeys = all ? (environments.data?.items.map((e) => e.key) ?? []) : (ownProject.data?.environments ?? []);
  const vcenters = facets.data?.vcenters ?? [];
  const clusters = [
    ...new Set(vcenters.filter((v) => !search.vcenter || v.name === search.vcenter).flatMap((v) => v.clusters)),
  ].sort();
  const keep = (values: string[], v: string | undefined) => (v && !values.includes(v) ? [...values, v] : values);
  const where = all ? "your projects" : project.name;

  const toolbar = (
    <>
      <TableSearch label="Search VMs" placeholder="Name, IP or FQDN" value={text} onChange={setText} />
      <Select
        aria-label="Environment"
        value={search.env ?? "any"}
        onChange={(v) => set({ env: v === "any" ? undefined : v })}
        options={[
          { value: "any", label: "All environments" },
          ...keep(envKeys, envs.length === 1 ? envs[0] : search.env).map((k) => ({
            value: k,
            label: k.includes(",") ? k.split(",").join(", ") : envName(k),
            meta: k.includes(",") ? undefined : k,
          })),
        ]}
        className="w-44"
      />
      <Select
        aria-label="Power"
        value={search.power ?? "any"}
        onChange={(v) => set({ power: v === "any" ? undefined : (v as VmPowerState) })}
        options={[
          { value: "any", label: "Any power state" },
          ...Object.entries(powers).map(([value, label]) => ({ value, label })),
        ]}
        className="w-40"
      />
      <Select
        aria-label="vCenter"
        value={search.vcenter ?? "any"}
        onChange={(v) => set({ vcenter: v === "any" ? undefined : v, cluster: undefined })}
        options={[
          { value: "any", label: "All vCenters" },
          ...keep(
            vcenters.map((v) => v.name),
            search.vcenter,
          ).map((v) => ({ value: v, label: v })),
        ]}
        className="w-56"
      />
      <Select
        aria-label="Cluster"
        value={search.cluster ?? "any"}
        onChange={(v) => set({ cluster: v === "any" ? undefined : v })}
        options={[
          { value: "any", label: "All clusters" },
          ...keep(clusters, search.cluster).map((c) => ({ value: c, label: c })),
        ]}
        className="w-44"
      />
      <Select
        aria-label="Network"
        value={search.network ?? "any"}
        onChange={(v) => set({ network: v === "any" ? undefined : v })}
        options={[
          { value: "any", label: "All networks" },
          ...keep(facets.data?.networks ?? [], search.network).map((n) => ({ value: n, label: n })),
        ]}
        className="w-52"
      />
      {filtered && (
        <Button variant="quiet" onClick={clear}>
          Clear filters
        </Button>
      )}
    </>
  );

  return (
    <Page>
      <PageHeader
        title="Resources"
        description={`The VMs your deployments hold in ${where}, live from vCenter.`}
        meta={items && vmCount(rows.length, on, list.hasNextPage)}
        actions={
          <>
            {(me.projects.length > 1 || all) && (
              <ScopeToggle
                all={all}
                projectName={project.name}
                onChange={(next) => set({ project: next ? "all" : undefined })}
              />
            )}
            <SavedViewsMenu list="resources" query={toQuery(search)} onApply={apply} />
          </>
        }
      />
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
          label="VMs"
          columns={columnsFor(all, setShown)}
          rows={items}
          rowKey={(v) => v.id}
          sort={toSort(search.sort)}
          onSortChange={(s) => set({ sort: fromSort(s) })}
          presorted
          minWidth={all ? 1280 : 1100}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          toolbar={toolbar}
          empty={
            filtered ? (
              <EmptyState
                icon={SearchX}
                title="No VMs match"
                body="Try other words, or other filters."
                action={
                  <Button size="sm" onClick={clear}>
                    Clear filters
                  </Button>
                }
              />
            ) : (
              <EmptyState
                icon={HardDrive}
                title={`No VMs in ${where} yet`}
                body="The VMs your requests build show here, with their addresses, placement and size."
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
      {shown && <VmPropertiesSheet resource={shown} onClose={() => setShown(null)} />}
    </Page>
  );
}
