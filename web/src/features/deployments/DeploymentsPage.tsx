import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Ellipsis, LayoutGrid, Plus, Server, Table2 } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge } from "@/components/common/status";
import { Button, IconButton } from "@/components/ui/button";
import { DataTable, type Column, type Sort } from "@/components/ui/data-table";
import { Menu, MenuContent, MenuTrigger } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterCheck, FilterEmpty, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { Truncate } from "@/components/ui/truncate";
import type { Day2Action, Deployment, DeploymentStatusFilter } from "@/lib/api/types";
import { can } from "@/lib/can";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { q, type DeploymentSort } from "@/lib/query";
import { cn } from "@/lib/utils";
import { daysUntil, leaseLabel } from "@/lib/format";
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

const filterKeys = ["status", "env", "item", "template", "owner", "q", "expiring"] as const;

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

const statuses: Record<NonNullable<DeploymentsSearch["status"]>, DeploymentStatusFilter[]> = {
  ready: ["ready"],
  provisioning: ["provisioning", "updating"],
  failed: ["failed"],
  expired: ["expired"],
};

const statusKeys = Object.keys(statuses) as (keyof typeof statuses)[];

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

interface CardSort {
  value: DeploymentSort;
  key:
    "nameAz" | "nameZa" | "newest" | "leaseFirst" | "status" | "environment" | "owner" | "item" | "project" | "mostVms";
  all?: boolean;
}

const cardSorts: CardSort[] = [
  { value: "name", key: "nameAz" },
  { value: "-name", key: "nameZa" },
  { value: "-created_at", key: "newest" },
  { value: "lease_expires_at", key: "leaseFirst" },
  { value: "status", key: "status" },
  { value: "environment", key: "environment" },
  { value: "owner", key: "owner" },
  { value: "item", key: "item" },
  { value: "project", key: "project", all: true },
  { value: "-vm_count", key: "mostVms" },
];

const weekAhead = () => new Date(Math.ceil(Date.now() / 3_600_000) * 3_600_000 + 7 * 86_400_000).toISOString();

function Dots({ d }: { d: Deployment }) {
  const { t } = useTranslation("deployments");
  return (
    <span className="inline-flex items-center gap-1 whitespace-nowrap">
      {Array.from({ length: Math.min(d.vm_count, 6) }, (_, i) => (
        <span
          key={i}
          aria-hidden
          className={cn("size-2 rounded-full", i < d.vms_powered_on ? "bg-ok" : "border border-line-strong")}
        />
      ))}
      <span className="tnum ml-1 text-muted">{t("onOf", { count: d.vms_powered_on, total: d.vm_count })}</span>
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
      {d.primary_address?.fqdn && (
        <Truncate mono className="max-w-56 text-caption text-muted">
          {d.primary_address.fqdn}
        </Truncate>
      )}
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

function columnsFor(t: TFunction<"deployments">, all: boolean): Column<Deployment>[] {
  return [
    {
      id: "name",
      header: t("list.columns.deployment"),
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
            header: t("list.columns.project"),
            sortValue: (d: Deployment) => d.project.name,
            cell: (d: Deployment) => <span className="text-muted">{d.project.name}</span>,
          },
        ]
      : []),
    {
      id: "env",
      header: t("list.columns.environment"),
      sortValue: (d) => d.environment,
      cell: (d) => <EnvBadge env={d.environment} />,
    },
    {
      id: "status",
      header: t("list.columns.status"),
      sortValue: (d) => d.status,
      cell: (d) => <DeploymentStatusBadge status={d.status} />,
    },
    { id: "vms", header: t("list.columns.vms"), sortValue: (d) => d.vm_count, cell: (d) => <Dots d={d} /> },
    { id: "address", header: t("list.columns.address"), cell: (d) => <Address d={d} /> },
    {
      id: "owner",
      header: t("list.columns.owner"),
      sortValue: (d) => d.owner.name,
      cell: (d) => <span className="text-muted">{d.owner.name}</span>,
    },
    {
      id: "lease",
      header: t("list.columns.lease"),
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
  const { t } = useTranslation("deployments");
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
    <ul aria-label={t("list.cards")} className="grid gap-3 p-4 sm:grid-cols-2 xl:grid-cols-3">
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
                <IconButton label={t("actionsFor", { name: d.name })} className="-mt-1 -mr-1.5">
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
              <dt className="text-faint">{t("list.owner")}</dt>
              <dd className="text-muted">{d.owner.name}</dd>
            </div>
            <div className="flex gap-1">
              <dt className="text-faint">{t("list.lease")}</dt>
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
  const t = i18n.getFixedT(null, "deployments");
  const counts = {
    what: more ? t("count.shown", { count: n }) : t("count.deployments", { count: n }),
    vms: t("count.vms", { count: vms }),
    on: t("count.on", { count: on }),
  };
  return expiring
    ? t("count.summaryLeases", { ...counts, leases: t("count.leases", { count: expiring }) })
    : t("count.summary", counts);
}

export function DeploymentsPage({ search }: { search: DeploymentsSearch }) {
  const { t } = useTranslation(["deployments", "common"]);
  const { me, projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/deployments/" });
  const filters = useAddressFilters(search, filterKeys);
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
      status: search.status ? statuses[search.status] : undefined,
      environment: envs.length ? envs : undefined,
      item: search.item ? [search.item] : undefined,
      template: search.template ? [search.template] : undefined,
      owner: search.owner,
      q: search.q,
      leaseExpiresBefore: search.expiring ? weekAhead() : undefined,
      sort: search.sort ?? "name",
    }),
  );

  const set = (patch: Partial<DeploymentsSearch>) =>
    void navigate({ search: (s: DeploymentsSearch) => ({ ...s, ...patch }), replace: true });
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
    ...envKeys.map((k) => ({ value: k, label: envName(k), meta: k })),
    ...(envs.length > 1 ? [{ value: search.env!, label: envs.join(", ") }] : []),
  ];
  const templates = facets.data?.templates ?? [];

  const toolbar = (
    <FilterBar filters={filters} search={{ label: t("list.search"), placeholder: t("list.searchPlaceholder") }}>
      <FilterSelect
        name="status"
        label={t("list.statusFilter")}
        any={t("list.allStatuses")}
        options={statusKeys.map((k) => ({ value: k, label: t(`list.status.${k}`) }))}
        className="w-40"
      />
      <FilterSelect
        name="env"
        label={t("list.environment")}
        any={t("list.allEnvironments")}
        options={envOptions}
        className="w-44"
      />
      <FilterSelect
        name="item"
        label={t("list.item")}
        any={t("list.allItems")}
        options={facets.data?.items.map((i) => ({ value: i.slug, label: i.name })) ?? []}
        className="w-48"
      />
      {(templates.length > 0 || search.template) && (
        <FilterSelect
          name="template"
          label={t("list.template")}
          any={t("list.allTemplates")}
          options={templates.map((x) => ({ value: x, label: x }))}
          className="w-52"
        />
      )}
      <PersonFilter
        label={t("list.ownerFilter")}
        value={search.owner === "me" ? undefined : search.owner}
        onChange={(id) => filters.set({ owner: id === me.user.id ? "me" : id })}
      />
      <FilterCheck name="owner" value="me" label={t("list.onlyMine")} />
      <FilterCheck name="expiring" label={t("list.expiring")} />
      {layout === "cards" && (
        <Select
          aria-label={t("list.sortBy")}
          value={search.sort ?? "name"}
          onChange={(v) => set({ sort: v === "name" ? undefined : (v as DeploymentSort) })}
          options={[
            ...cardSorts.filter((s) => all || !s.all).map((s) => ({ value: s.value, label: t(`list.sort.${s.key}`) })),
            ...(search.sort && !cardSorts.some((s) => s.value === search.sort)
              ? [{ value: search.sort, label: search.sort }]
              : []),
          ]}
          className="w-44"
        />
      )}
    </FilterBar>
  );

  const empty = filters.filtered ? (
    <FilterEmpty filters={filters} title={t("list.noMatch")} body={t("list.noMatchBody")} />
  ) : (
    <EmptyState
      icon={Server}
      title={all ? t("list.noneAll") : t("list.noneIn", { project: project.name })}
      body={t("list.noneBody")}
      action={
        <Button size="sm" asChild>
          <Link to="/catalog">{t("list.fromCatalog")}</Link>
        </Button>
      }
    />
  );
  const error = list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />;

  return (
    <Page>
      <PageHeader
        title={t("list.title")}
        description={all ? t("list.leadAll") : t("list.leadProject", { project: project.name })}
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
              label={t("list.showAs")}
              value={layout}
              onChange={setLayout}
              options={[
                { value: "table", label: t("list.table"), icon: Table2, iconOnly: true },
                { value: "cards", label: t("list.cardsLayout"), icon: LayoutGrid, iconOnly: true },
              ]}
            />
            <SavedViewsMenu list="deployments" query={toQuery({ ...search, layout })} onApply={apply} />
            {can(me, "import.manage") && (
              <Button asChild>
                <Link to="/admin/onboarding" search={{ tab: "plans" }}>
                  {t("list.adopt")}
                </Link>
              </Button>
            )}
            <Button variant="primary" asChild>
              <Link to="/catalog">
                <Plus /> {t("list.newRequest")}
              </Link>
            </Button>
          </>
        }
      />

      {visibleSelected.length > 0 && (
        <div
          className="flex flex-wrap items-center gap-2 rounded-lg border border-accent bg-accent-soft px-3 py-2"
          role="region"
          aria-label={t("list.bulk")}
        >
          <span className="font-medium">{t("list.selected", { count: visibleSelected.length })}</span>
          <span className="flex-1" />
          {bulkActions.map((b) => (
            <Button
              key={b.key}
              size="sm"
              variant={b.key === "delete" ? "danger" : undefined}
              onClick={() => setBulk(b)}
            >
              {t(`bulk.${b.key}.label`)}
            </Button>
          ))}
          <Button size="sm" variant="quiet" onClick={() => setSelected(new Set())}>
            {t("list.clearSelection")}
          </Button>
        </div>
      )}

      <Panel
        flush
        footer={
          list.hasNextPage && (
            <Button size="sm" onClick={() => void list.fetchNextPage()} disabled={list.isFetchingNextPage}>
              {list.isFetchingNextPage ? t("common:loadingEllipsis") : t("list.loadMore")}
            </Button>
          )
        }
      >
        {layout === "table" ? (
          <DataTable
            label={t("list.title")}
            columns={columnsFor(t, all)}
            rows={items}
            rowKey={(d) => d.id}
            rowLinks
            rowMenu={(d) => <RowActionItems deployment={d} onPick={pick} />}
            rowMenuLabel={(d) => t("actionsFor", { name: d.name })}
            selection={{
              selected,
              onChange: setSelected,
              label: (d) => t("list.select", { name: d.name }),
              allLabel: list.hasNextPage ? t("list.selectAllShown") : t("common:table.selectAll"),
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
