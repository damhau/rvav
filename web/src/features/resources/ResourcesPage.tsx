import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { HardDrive } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { EnvBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { DataTable, type Column, type Sort } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { FilterBar, FilterEmpty, FilterSelect, useAddressFilters } from "@/components/ui/filter-bar";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Truncate } from "@/components/ui/truncate";
import type { VmListItem, VmPowerState } from "@/lib/api/types";
import { formatSize } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { useCurrentProject } from "@/lib/project";
import { q, type VmSort } from "@/lib/query";
import { cn } from "@/lib/utils";
import { SavedViewsMenu, ScopeToggle, toQuery, type ListQuery } from "@/features/deployments/listing";
import { VmPropertiesSheet } from "@/features/deployments/VmProperties";

const filterKeys = ["env", "power", "vcenter", "cluster", "network", "q"] as const;

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

const powerStates: VmPowerState[] = ["on", "off", "suspended"];

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

const gb = (mb: number) => formatSize(mb / 1024, "GB");
const diskGb = (v: VmListItem) => v.vm.disks.reduce((n, d) => n + d.capacity_gb, 0);
const mono = "font-mono text-secondary";

function columnsFor(t: TFunction<"resources">, all: boolean, open: (v: VmListItem) => void): Column<VmListItem>[] {
  return [
    {
      id: "name",
      className: "whitespace-nowrap",
      header: t("columns.vm"),
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
            <Truncate mono className="max-w-52 text-caption text-muted">
              {v.vm.fqdn}
            </Truncate>
          )}
        </>
      ),
    },
    {
      id: "power",
      header: t("columns.power"),
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
          {t(`shortPower.${v.vm.power_state}`)}
        </span>
      ),
      className: "whitespace-nowrap",
    },
    {
      id: "addresses",
      className: "whitespace-nowrap",
      header: t("columns.addresses"),
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
      header: t("columns.deployment"),
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
            header: t("columns.project"),
            sortValue: (v: VmListItem) => v.project.name,
            cell: (v: VmListItem) => <span className="text-muted">{v.project.name}</span>,
          },
        ]
      : []),
    {
      id: "env",
      header: t("columns.environment"),
      sortValue: (v) => v.environment,
      cell: (v) => <EnvBadge env={v.environment} />,
    },
    {
      id: "placement",
      className: "[overflow-wrap:anywhere]",
      header: t("columns.placement"),
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
      header: t("columns.memory"),
      align: "end",
      sortValue: (v) => v.vm.memory_mb,
      cell: (v) => <span className="tnum whitespace-nowrap">{gb(v.vm.memory_mb)}</span>,
    },
    {
      id: "disks",
      header: t("columns.disks"),
      align: "end",
      sortValue: diskGb,
      cell: (v) => (
        <span className="tnum whitespace-nowrap">
          {v.vm.disks.length} · {formatSize(diskGb(v), "GB")}
        </span>
      ),
    },
    {
      id: "networks",
      className: "[overflow-wrap:anywhere]",
      header: t("columns.networks"),
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
  const t = i18n.getFixedT(null, "resources");
  return more
    ? t("count.summary", { vms: t("count.shown", { count: n }), on: t("count.shownOn", { count: on }) })
    : t("count.summary", { vms: t("count.vms", { count: n }), on: t("count.on", { count: on }) });
}

export function ResourcesPage({ search }: { search: ResourcesSearch }) {
  const { t } = useTranslation(["resources", "common"]);
  const { me, projectId, project } = useCurrentProject();
  const navigate = useNavigate({ from: "/resources" });
  const filters = useAddressFilters(search, filterKeys);
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
  const set = (patch: Partial<ResourcesSearch>) =>
    void navigate({ search: (s: ResourcesSearch) => ({ ...s, ...patch }), replace: true });
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

  const toolbar = (
    <FilterBar filters={filters} search={{ label: t("toolbar.search"), placeholder: t("toolbar.searchPlaceholder") }}>
      <FilterSelect
        name="env"
        label={t("toolbar.environment")}
        any={t("toolbar.allEnvironments")}
        options={[
          ...envKeys.map((k) => ({ value: k, label: envName(k) })),
          ...(envs.length > 1 ? [{ value: search.env!, label: envs.map(envName).join(", ") }] : []),
        ]}
        className="w-44"
      />
      <FilterSelect
        name="power"
        label={t("toolbar.power")}
        any={t("toolbar.anyPower")}
        options={powerStates.map((value) => ({ value, label: t(`power.${value}`) }))}
        className="w-40"
      />
      <FilterSelect
        name="vcenter"
        label="vCenter"
        any={t("toolbar.allVcenters")}
        options={vcenters.map((v) => ({ value: v.name, label: v.name }))}
        onChange={() => ({ cluster: undefined })}
        className="w-56"
      />
      <FilterSelect
        name="cluster"
        label={t("toolbar.cluster")}
        any={t("toolbar.allClusters")}
        options={clusters.map((c) => ({ value: c, label: c }))}
        className="w-44"
      />
      <FilterSelect
        name="network"
        label={t("toolbar.network")}
        any={t("toolbar.allNetworks")}
        options={(facets.data?.networks ?? []).map((n) => ({ value: n, label: n }))}
        className="w-52"
      />
    </FilterBar>
  );

  return (
    <Page>
      <PageHeader
        title={t("page.title")}
        description={all ? t("page.leadAll") : t("page.leadProject", { project: project.name })}
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
              {list.isFetchingNextPage ? t("common:loadingEllipsis") : t("page.loadMore")}
            </Button>
          )
        }
      >
        <DataTable
          label={t("page.table")}
          columns={columnsFor(t, all, setShown)}
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
            filters.filtered ? (
              <FilterEmpty filters={filters} title={t("page.noMatch")} body={t("page.noMatchBody")} />
            ) : (
              <EmptyState
                icon={HardDrive}
                title={all ? t("page.noneAll") : t("page.noneIn", { project: project.name })}
                body={t("page.noneBody")}
                action={
                  <Button size="sm" asChild>
                    <Link to="/catalog">{t("page.fromCatalog")}</Link>
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
