import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis, LayoutGrid, Plus, SearchX, Server, Table2 } from "lucide-react";
import { useState } from "react";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge } from "@/components/common/status";
import { Button, IconButton } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, TableSearch, type Column, type Sort } from "@/components/ui/data-table";
import { Menu, MenuContent, MenuTrigger } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import type { Day2Action, Deployment, DeploymentStatusFilter } from "@/lib/api/types";
import { can } from "@/lib/can";
import { useSearchBox } from "@/lib/hooks";
import { useCurrentProject } from "@/lib/project";
import { q, type DeploymentSort } from "@/lib/query";
import { cn, daysUntil, leaseLabel } from "@/lib/utils";
import { RowActionItems, usePickedAction } from "./actions";
import { BulkDialog, bulkActions } from "./bulk";
import {
  rememberedLayout,
  rememberLayout,
  SavedViewsMenu,
  ScopeToggle,
  toQuery,
  type Layout,
  type ListQuery,
} from "./listing";
import { PersonFilter } from "./PersonFilter";

export interface DeploymentsSearch {
  status?: "ready" | "provisioning" | "failed" | "expired";
  /** Environment keys, separated by commas. */
  env?: string;
  /** A catalog item's slug. */
  item?: string;
  /** A VM template's name. */
  template?: string;
  /** A user ID, or `me`. */
  owner?: string;
  q?: string;
  expiring?: boolean;
  sort?: DeploymentSort;
  layout?: Layout;
  /** `all`: every project the person is in, instead of the one they work in. */
  project?: "all";
}

export const deploymentSorts = [
  "name",
  "-name",
  "created_at",
  "-created_at",
  "lease_expires_at",
  "-lease_expires_at",
  "status",
  "-status",
  "environment",
  "-environment",
  "owner",
  "-owner",
  "item",
  "-item",
  "project",
  "-project",
  "vm_count",
  "-vm_count",
] as const satisfies readonly DeploymentSort[];

const statuses: Record<
  NonNullable<DeploymentsSearch["status"]>,
  { label: string; statuses: DeploymentStatusFilter[] }
> = {
  ready: { label: "Ready", statuses: ["ready"] },
  provisioning: { label: "Provisioning", statuses: ["provisioning", "updating"] },
  failed: { label: "Failed", statuses: ["failed"] },
  expired: { label: "Lease ended", statuses: ["expired"] },
};

// Each sortable column's sort: the API sorts the whole list, not the pages loaded.
const sortKeys: Record<string, string> = {
  name: "name",
  project: "project",
  env: "environment",
  status: "status",
  vms: "vm_count",
  owner: "owner",
  lease: "lease_expires_at",
};

const toSort = (s: DeploymentsSearch["sort"]): Sort | null => {
  const key = (s ?? "name").replace(/^-/, "");
  const id = Object.keys(sortKeys).find((k) => sortKeys[k] === key);
  return id ? { id, desc: Boolean(s?.startsWith("-")) } : null;
};

const fromSort = (s: Sort | null): DeploymentsSearch["sort"] => {
  const key = s && sortKeys[s.id];
  if (!key || (key === "name" && !s.desc)) return undefined;
  return (s.desc ? `-${key}` : key) as DeploymentSort;
};

const cardSorts: { value: DeploymentSort; label: string; all?: boolean }[] = [
  { value: "name", label: "Name, A to Z" },
  { value: "-name", label: "Name, Z to A" },
  { value: "-created_at", label: "Newest first" },
  { value: "lease_expires_at", label: "Lease ending first" },
  { value: "status", label: "Status" },
  { value: "environment", label: "Environment" },
  { value: "owner", label: "Owner" },
  { value: "item", label: "Catalog item" },
  { value: "project", label: "Project", all: true },
  { value: "-vm_count", label: "Most VMs first" },
];

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

function Address({ d }: { d: Deployment }) {
  return (
    <>
      <span className="font-mono text-secondary">
        {d.primary_address?.ip || "–"}
        {d.vm_count > 1 && <span className="text-muted"> +{d.vm_count - 1}</span>}
      </span>
      {d.primary_address?.fqdn && <div className="font-mono text-caption text-muted">{d.primary_address.fqdn}</div>}
    </>
  );
}

const nameLink = (d: Deployment) => (
  <Link
    to="/deployments/$deploymentId"
    params={{ deploymentId: d.id }}
    className="font-medium text-fg hover:text-accent"
  >
    {d.name}
  </Link>
);

function columnsFor(all: boolean): Column<Deployment>[] {
  return [
    {
      id: "name",
      header: "Deployment",
      sortValue: (d) => d.name,
      cell: (d) => (
        <>
          {nameLink(d)}
          <div className="text-caption text-muted">
            {d.catalog_item.name} · {d.version.number}
          </div>
        </>
      ),
    },
    ...(all
      ? [
          {
            id: "project",
            header: "Project",
            sortValue: (d: Deployment) => d.project.name,
            cell: (d: Deployment) => <span className="text-muted">{d.project.name}</span>,
          },
        ]
      : []),
    {
      id: "env",
      header: "Environment",
      sortValue: (d) => d.environment,
      cell: (d) => <EnvBadge env={d.environment} />,
    },
    {
      id: "status",
      header: "Status",
      sortValue: (d) => d.status,
      cell: (d) => <DeploymentStatusBadge status={d.status} />,
    },
    { id: "vms", header: "VMs", sortValue: (d) => d.vm_count, cell: (d) => <Dots d={d} /> },
    { id: "address", header: "Address", cell: (d) => <Address d={d} /> },
    {
      id: "owner",
      header: "Owner",
      sortValue: (d) => d.owner.name,
      cell: (d) => <span className="text-muted">{d.owner.name}</span>,
    },
    {
      id: "lease",
      header: "Lease",
      sortValue: (d) => d.lease.expires_at,
      cell: (d) => <Lease expiresAt={d.lease.expires_at} />,
    },
  ];
}

/** Deployments as cards: each one's facts at a glance, its name opening it. */
function DeploymentCards({
  items,
  all,
  loading,
  onPick,
}: {
  items: Deployment[];
  all: boolean;
  loading: boolean;
  onPick: (d: Deployment, a: Day2Action) => void;
}) {
  if (loading && items.length === 0) {
    return (
      <div className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3" aria-hidden>
        {[0, 1, 2].map((i) => (
          <Skeleton key={i} className="h-36" />
        ))}
      </div>
    );
  }
  return (
    <ul aria-label="Deployments" className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
      {items.map((d) => (
        <li key={d.id} className="flex min-w-0 flex-col gap-2 rounded-lg border border-line bg-surface p-3.5">
          <div className="flex items-start justify-between gap-2">
            <div className="flex min-w-0 flex-col">
              <h2 className="truncate text-body">{nameLink(d)}</h2>
              <span className="truncate text-caption text-muted">
                {d.catalog_item.name} · {d.version.number}
              </span>
            </div>
            <Menu>
              <MenuTrigger asChild>
                <IconButton label={`Actions for ${d.name}`} className="-mt-1 -mr-1.5">
                  <Ellipsis />
                </IconButton>
              </MenuTrigger>
              <MenuContent align="end">
                <RowActionItems deployment={d} onPick={onPick} />
              </MenuContent>
            </Menu>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <DeploymentStatusBadge status={d.status} />
            <EnvBadge env={d.environment} />
            {all && <span className="truncate text-caption text-muted">{d.project.name}</span>}
          </div>
          <div className="text-secondary">
            <Dots d={d} />
          </div>
          <div>
            <Address d={d} />
          </div>
          <dl className="mt-auto flex flex-wrap justify-between gap-x-3 gap-y-1 border-t border-line pt-2 text-caption">
            <div className="flex gap-1">
              <dt className="text-faint">Owner</dt>
              <dd className="text-muted">{d.owner.name}</dd>
            </div>
            <div className="flex gap-1">
              <dt className="text-faint">Lease</dt>
              <dd>
                <Lease expiresAt={d.lease.expires_at} />
              </dd>
            </div>
          </dl>
        </li>
      ))}
    </ul>
  );
}

/** The count above the list: the deployments loaded, said as such while more pages remain. */
export function deploymentCount(n: number, vms: number, on: number, expiring: number, more: boolean) {
  const leases = expiring ? ` · ${expiring} lease${expiring === 1 ? "" : "s"} end within 7 days` : "";
  if (more) return `${n} shown · ${vms} VMs, ${on} powered on${leases}`;
  return `${n} deployment${n === 1 ? "" : "s"} · ${vms} VMs, ${on} powered on${leases}`;
}

export function DeploymentsPage({ search }: { search: DeploymentsSearch }) {
  const { me, projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/deployments/" });
  // The address keeps the search, without a history entry per keystroke.
  const [text, setText] = useSearchBox(
    search.q,
    (next) => void navigate({ search: (s: DeploymentsSearch) => ({ ...s, q: next }), replace: true }),
  );
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [bulk, setBulk] = useState<(typeof bulkActions)[number] | null>(null);
  const { pick, dialog } = usePickedAction();
  const all = search.project === "all";
  const scope = all ? "" : projectId;
  const layout = search.layout ?? rememberedLayout("deployments", me.user.id);
  const environments = useQuery(q.environments());
  const ownProject = useQuery({ ...q.project(projectId), enabled: !all && Boolean(projectId) });
  const facets = useQuery(q.deploymentFacets(scope));
  const envs = (search.env ?? "").split(",").filter(Boolean);
  const list = useInfiniteQuery(
    q.deployments({
      projectId: scope,
      status: search.status ? statuses[search.status].statuses : undefined,
      environment: envs.length ? envs : undefined,
      item: search.item ? [search.item] : undefined,
      template: search.template ? [search.template] : undefined,
      owner: search.owner,
      q: search.q,
      leaseExpiresBefore: search.expiring ? weekAhead() : undefined,
      sort: search.sort ?? "name",
    }),
  );
  const filtered = Boolean(
    search.status || envs.length || search.item || search.template || search.owner || search.q || search.expiring,
  );

  const set = (patch: Partial<DeploymentsSearch>) =>
    void navigate({ search: (s: DeploymentsSearch) => ({ ...s, ...patch }), replace: true });
  const clear = () => {
    void navigate({
      search: (s: DeploymentsSearch) => ({ sort: s.sort, layout: s.layout, project: s.project }),
      replace: true,
    });
  };
  const apply = (query: ListQuery) => void navigate({ search: query as DeploymentsSearch });
  const setLayout = (next: Layout) => {
    rememberLayout("deployments", me.user.id, next);
    set({ layout: next });
  };

  const items = list.data?.pages.flatMap((p) => p.items);
  const shown = items ?? [];
  const vms = shown.reduce((n, d) => n + d.vm_count, 0);
  const on = shown.reduce((n, d) => n + d.vms_powered_on, 0);
  const expiring = shown.filter((d) => d.lease.expires_at && daysUntil(d.lease.expires_at) <= 7).length;
  const visibleSelected = layout === "table" ? shown.filter((d) => selected.has(d.id)) : [];
  const envName = (key: string) => environments.data?.items.find((e) => e.key === key)?.name ?? key;
  const envKeys = all
    ? (environments.data?.items.map((e) => e.key) ?? facets.data?.environments ?? [])
    : (ownProject.data?.environments ?? facets.data?.environments ?? []);
  const envOptions = [
    { value: "any", label: "All environments" },
    ...envKeys.map((k) => ({ value: k, label: envName(k), meta: k })),
    ...(envs.length > 1 ? [{ value: search.env!, label: envs.join(", ") }] : []),
    ...(envs.length === 1 && !envKeys.includes(envs[0]!) ? [{ value: envs[0]!, label: envs[0]! }] : []),
  ];
  const itemOptions = [
    { value: "any", label: "All catalog items" },
    ...(facets.data?.items.map((i) => ({ value: i.slug, label: i.name })) ?? []),
    ...(search.item && !facets.data?.items.some((i) => i.slug === search.item)
      ? [{ value: search.item, label: search.item }]
      : []),
  ];
  const templates = [
    ...(facets.data?.templates ?? []),
    ...(search.template && !facets.data?.templates.includes(search.template) ? [search.template] : []),
  ];
  const where = all ? "your projects" : project.name;

  const toolbar = (
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
        className="w-44"
      />
      <Select
        aria-label="Catalog item"
        value={search.item ?? "any"}
        onChange={(v) => set({ item: v === "any" ? undefined : v })}
        options={itemOptions}
        className="w-48"
      />
      {templates.length > 0 && (
        <Select
          aria-label="VM template"
          value={search.template ?? "any"}
          onChange={(v) => set({ template: v === "any" ? undefined : v })}
          options={[{ value: "any", label: "All templates" }, ...templates.map((t) => ({ value: t, label: t }))]}
          className="w-52"
        />
      )}
      <PersonFilter
        label="Owner"
        value={search.owner === "me" ? undefined : search.owner}
        onChange={(id) => set({ owner: id === me.user.id ? "me" : id })}
      />
      <span className="flex items-center gap-2 text-body">
        <Checkbox
          id="deployments-mine"
          checked={search.owner === "me"}
          onChange={(v) => set({ owner: v ? "me" : undefined })}
        />
        <label htmlFor="deployments-mine">Only mine</label>
      </span>
      <span className="flex items-center gap-2 text-body">
        <Checkbox
          id="deployments-expiring"
          checked={Boolean(search.expiring)}
          onChange={(v) => set({ expiring: v || undefined })}
        />
        <label htmlFor="deployments-expiring">Lease ends within 7 days</label>
      </span>
      {layout === "cards" && (
        <Select
          aria-label="Sort by"
          value={search.sort ?? "name"}
          onChange={(v) => set({ sort: v === "name" ? undefined : (v as DeploymentSort) })}
          options={[
            ...cardSorts.filter((s) => all || !s.all),
            ...(search.sort && !cardSorts.some((s) => s.value === search.sort)
              ? [{ value: search.sort, label: search.sort }]
              : []),
          ]}
          className="w-44"
        />
      )}
      {filtered && (
        <Button variant="quiet" onClick={clear}>
          Clear filters
        </Button>
      )}
    </>
  );

  const empty = filtered ? (
    <EmptyState
      icon={SearchX}
      title="No deployments match"
      body="Try other words, or other filters."
      action={
        <Button size="sm" onClick={clear}>
          Clear filters
        </Button>
      }
    />
  ) : (
    <EmptyState
      icon={Server}
      title={`Nothing deployed in ${where} yet`}
      body="What your requests build shows here, with its VMs, addresses and lease."
      action={
        <Button size="sm" asChild>
          <Link to="/catalog">Request something from the catalog</Link>
        </Button>
      }
    />
  );
  const error = list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />;

  return (
    <Page>
      <PageHeader
        title="Deployments"
        description={`What requests built in ${where}. Power state and addresses come from vCenter, live.`}
        meta={items && deploymentCount(shown.length, vms, on, expiring, list.hasNextPage)}
        actions={
          <>
            {(me.projects.length > 1 || all) && (
              <ScopeToggle
                all={all}
                projectName={project.name}
                onChange={(next) => set({ project: next ? "all" : undefined })}
              />
            )}
            <Segmented
              label="Show as"
              value={layout}
              onChange={setLayout}
              options={[
                { value: "table", label: "Table", icon: Table2, iconOnly: true },
                { value: "cards", label: "Cards", icon: LayoutGrid, iconOnly: true },
              ]}
            />
            <SavedViewsMenu list="deployments" query={toQuery({ ...search, layout })} onApply={apply} />
            {can(me, "import.manage") && (
              <Button asChild>
                <Link to="/admin/onboarding" search={{ tab: "plans" }}>
                  Adopt existing VMs
                </Link>
              </Button>
            )}
            <Button variant="primary" asChild>
              <Link to="/catalog">
                <Plus /> New request
              </Link>
            </Button>
          </>
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
        {layout === "table" ? (
          <DataTable
            label="Deployments"
            columns={columnsFor(all)}
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
            presorted
            minWidth={all ? 1100 : 980}
            loading={list.isPending}
            error={error}
            toolbar={toolbar}
            empty={empty}
          />
        ) : (
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">{toolbar}</div>
            {error || (!list.isPending && shown.length === 0 ? empty : null) || (
              <DeploymentCards items={shown} all={all} loading={list.isPending} onPick={pick} />
            )}
          </div>
        )}
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
