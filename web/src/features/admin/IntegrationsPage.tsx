import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { CircleCheck } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { EmptyState } from "@/components/ui/empty-state";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { components } from "@/lib/api/schema";
import type { IpamReconcile } from "@/lib/api/types";
import { adm, q } from "@/lib/query";
import { fullDate, relative } from "@/lib/format";
import { integrationAbout, integrationPage, integrationTitle } from "./IntegrationSettings";

type S = components["schemas"];

export const integrationsTabs = ["connections", "checks"] as const;
export type IntegrationsTab = (typeof integrationsTabs)[number];

function State({ it }: { it: S["Integration"] }) {
  const { t } = useTranslation("admin");
  if (it.applying) return <Badge tone="warn">{t("integrations.state.applying")}</Badge>;
  if (it.configured) return <Badge tone="ok">{t("integrations.state.configured")}</Badge>;
  return <Badge tone="neutral">{t("integrations.state.notConfigured")}</Badge>;
}

const connectionColumns = (t: TFunction<"admin">): Column<S["Integration"]>[] => [
  {
    id: "connection",
    header: t("integrations.columns.connection"),
    cell: (it) => (
      <>
        <Link
          to="/admin/integrations/$kind"
          params={{ kind: it.kind }}
          className="font-medium text-fg hover:text-accent"
        >
          {integrationTitle(it.kind)}
        </Link>
        <div className="text-secondary text-muted">{integrationAbout(it.kind)}</div>
      </>
    ),
  },
  { id: "state", header: t("integrations.columns.state"), cell: (it) => <State it={it} /> },
  {
    id: "saved",
    header: t("integrations.columns.saved"),
    className: "whitespace-nowrap",
    cell: (it) =>
      it.updated_at ? (
        <span className="text-secondary">
          <span title={fullDate(it.updated_at)}>{relative(it.updated_at)}</span>
          {it.updated_by && (
            <span className="block text-muted">{t("integrations.by", { name: it.updated_by.name })}</span>
          )}
        </span>
      ) : (
        <span className="text-secondary text-faint">{t("integrations.never")}</span>
      ),
  },
];

function Connections() {
  const { t } = useTranslation("admin");
  const list = useQuery(adm.integrations());
  // The console's settings are on vCenter's page.
  const items = list.data?.items.filter((it) => integrationPage(it.kind) === it.kind);
  return (
    <Panel title={t("integrations.connections.title")} description={t("integrations.connections.description")} flush>
      <DataTable
        label={t("integrations.connections.title")}
        columns={connectionColumns(t)}
        rows={items}
        rowKey={(it) => it.kind}
        rowLinks
        minWidth={640}
        loading={list.isPending}
        error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
      />
    </Panel>
  );
}

const mono = "font-mono text-secondary";

function Report({ report }: { report: IpamReconcile }) {
  const { t } = useTranslation("admin");
  const clean = report.orphan_addresses.length + report.orphan_records.length + report.missing.length === 0;
  return (
    <div className="flex flex-col">
      <p className="px-4 py-3 text-secondary text-muted">
        {report.efficientip
          ? t("integrations.reconcile.tagged", {
              addresses: t("integrations.reconcile.addresses", { count: report.managed_addresses }),
              records: t("integrations.reconcile.records", { count: report.managed_records }),
            })
          : t("integrations.reconcile.notSet")}{" "}
        {t("integrations.reconcile.checked", { date: fullDate(report.checked_at) })}
      </p>
      {report.orphan_allocations.length + report.unallocated.length > 0 && (
        <p className="border-t border-line px-4 py-3 text-secondary">
          {t("integrations.reconcile.ranges", {
            addresses: t("integrations.reconcile.addresses", { count: report.orphan_allocations.length }),
            vms: t("integrations.reconcile.vms", { count: report.unallocated.length }),
          })}{" "}
          <Link
            to="/admin/infrastructure"
            search={{ tab: "ip-ranges" }}
            className="font-medium text-accent hover:underline"
          >
            {t("integrations.reconcile.toRanges")}
          </Link>
        </p>
      )}
      {clean && report.efficientip && (
        <EmptyState
          icon={CircleCheck}
          title={t("integrations.reconcile.cleanTitle")}
          body={t("integrations.reconcile.cleanBody")}
        />
      )}
      {report.orphan_addresses.length > 0 && (
        <section aria-label={t("integrations.reconcile.orphanAddresses")} className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">{t("integrations.reconcile.orphanAddresses")}</h3>
          <DataTable
            label={t("integrations.reconcile.orphanAddresses")}
            rows={report.orphan_addresses}
            rowKey={(a) => a.efficientip_id}
            minWidth={560}
            columns={[
              {
                id: "ip",
                header: t("integrations.reconcile.column.address"),
                cell: (a) => <span className={mono}>{a.ip}</span>,
              },
              { id: "name", header: t("integrations.reconcile.column.name"), cell: (a) => a.name || "—" },
              {
                id: "subnet",
                header: t("integrations.reconcile.column.subnet"),
                cell: (a) => <span className={mono}>{a.subnet}</span>,
              },
              {
                id: "id",
                header: t("integrations.reconcile.column.id"),
                cell: (a) => <span className={mono}>{a.efficientip_id}</span>,
              },
            ]}
          />
        </section>
      )}
      {report.orphan_records.length > 0 && (
        <section aria-label={t("integrations.reconcile.orphanRecords")} className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">{t("integrations.reconcile.orphanRecords")}</h3>
          <DataTable
            label={t("integrations.reconcile.orphanRecords")}
            rows={report.orphan_records}
            rowKey={(r) => r.efficientip_id}
            minWidth={560}
            columns={[
              {
                id: "fqdn",
                header: t("integrations.reconcile.column.name"),
                cell: (r) => <span className={mono}>{r.fqdn}</span>,
              },
              { id: "type", header: t("integrations.reconcile.column.type"), cell: (r) => r.type },
              {
                id: "value",
                header: t("integrations.reconcile.column.value"),
                cell: (r) => <span className={mono}>{r.value}</span>,
              },
              {
                id: "id",
                header: t("integrations.reconcile.column.id"),
                cell: (r) => <span className={mono}>{r.efficientip_id}</span>,
              },
            ]}
          />
        </section>
      )}
      {report.missing.length > 0 && (
        <section aria-label={t("integrations.reconcile.missing")} className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">{t("integrations.reconcile.missingHeading")}</h3>
          <DataTable
            label={t("integrations.reconcile.missing")}
            rows={report.missing}
            rowKey={(m) => `${m.deployment_id}/${m.name}`}
            minWidth={560}
            columns={[
              {
                id: "deployment",
                header: t("integrations.reconcile.column.deployment"),
                cell: (m) => (
                  <Link
                    to="/deployments/$deploymentId"
                    params={{ deploymentId: m.deployment_id }}
                    className="font-medium text-fg hover:text-accent"
                  >
                    {m.deployment_name}
                  </Link>
                ),
              },
              {
                id: "what",
                header: t("integrations.reconcile.column.what"),
                cell: (m) => <span className={mono}>{m.name}</span>,
              },
              {
                id: "id",
                header: t("integrations.reconcile.column.id"),
                cell: (m) => <span className={mono}>{m.efficientip_id ?? "—"}</span>,
              },
            ]}
          />
        </section>
      )}
    </div>
  );
}

function Reconcile() {
  const { t } = useTranslation("admin");
  const [run, setRun] = useState(false);
  const report = useQuery({ ...q.ipamReconcile(), enabled: run });
  return (
    <Panel
      title={t("integrations.reconcile.title")}
      description={t("integrations.reconcile.description")}
      flush
      actions={
        <Button
          size="sm"
          variant="primary"
          disabled={report.isFetching}
          onClick={() => (run ? void report.refetch() : setRun(true))}
        >
          {report.isFetching
            ? t("integrations.reconcile.checking")
            : report.data
              ? t("integrations.reconcile.again")
              : t("integrations.reconcile.run")}
        </Button>
      }
    >
      {!run && <p className="px-4 py-3 text-secondary text-muted">{t("integrations.reconcile.about")}</p>}
      {run && report.isPending && <SkeletonRows rows={3} cols={4} />}
      {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
      {report.data && <Report report={report.data} />}
    </Panel>
  );
}

export function IntegrationsPage({ tab = "connections" }: { tab?: IntegrationsTab }) {
  const { t } = useTranslation(["admin", "shell"]);
  const navigate = useNavigate();
  return (
    <Page>
      <PageHeader title={t("shell:nav.admin.integrations")} description={t("integrations.description")} />
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({ to: "/admin/integrations", search: { tab: next as IntegrationsTab }, replace: true })
        }
      >
        <TabsList aria-label={t("shell:nav.admin.integrations")}>
          <TabsTrigger value="connections">{t("integrations.tabs.connections")}</TabsTrigger>
          <TabsTrigger value="checks">{t("integrations.tabs.checks")}</TabsTrigger>
        </TabsList>
        <TabsContent value="connections" className="pt-4">
          <Connections />
        </TabsContent>
        <TabsContent value="checks" className="pt-4">
          <Reconcile />
        </TabsContent>
      </Tabs>
    </Page>
  );
}
