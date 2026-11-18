import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { CircleCheck } from "lucide-react";
import { useState } from "react";
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
import { fullDate, relative } from "@/lib/utils";
import { integrationAbout, integrationPage, integrationTitle } from "./IntegrationSettings";

type S = components["schemas"];

export const integrationsTabs = ["connections", "checks"] as const;
export type IntegrationsTab = (typeof integrationsTabs)[number];

function State({ it }: { it: S["Integration"] }) {
  if (it.applying) return <Badge tone="warn">Applying</Badge>;
  if (it.configured) return <Badge tone="ok">Configured</Badge>;
  return <Badge tone="neutral">Not configured</Badge>;
}

const connectionColumns: Column<S["Integration"]>[] = [
  {
    id: "connection",
    header: "Connection",
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
  { id: "state", header: "State", cell: (it) => <State it={it} /> },
  {
    id: "saved",
    header: "Last saved",
    className: "whitespace-nowrap",
    cell: (it) =>
      it.updated_at ? (
        <span className="text-secondary">
          <span title={fullDate(it.updated_at)}>{relative(it.updated_at)}</span>
          {it.updated_by && <span className="block text-muted">by {it.updated_by.name}</span>}
        </span>
      ) : (
        <span className="text-secondary text-faint">Never</span>
      ),
  },
];

function Connections() {
  const list = useQuery(adm.integrations());
  // The console's settings are on vCenter's page.
  const items = list.data?.items.filter((it) => integrationPage(it.kind) === it.kind);
  return (
    <Panel
      title="Connections"
      description="Open one to change its settings and test it. A setting an environment variable sets can't be changed here."
      flush
    >
      <DataTable
        label="Connections"
        columns={connectionColumns}
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
  const clean = report.orphan_addresses.length + report.orphan_records.length + report.missing.length === 0;
  return (
    <div className="flex flex-col">
      <p className="px-4 py-3 text-secondary text-muted">
        EfficientIP has {report.managed_addresses} addresses and {report.managed_records} DNS records tagged for rvav.
        Checked {fullDate(report.checked_at)}.
      </p>
      {clean && (
        <EmptyState
          icon={CircleCheck}
          title="Nothing to reconcile"
          body="Every address and record tagged for rvav belongs to a deployment, and every deployment's addresses and records are in EfficientIP."
        />
      )}
      {report.orphan_addresses.length > 0 && (
        <section aria-label="Addresses no deployment owns" className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">Addresses no deployment owns</h3>
          <DataTable
            label="Addresses no deployment owns"
            rows={report.orphan_addresses}
            rowKey={(a) => a.efficientip_id}
            minWidth={560}
            columns={[
              { id: "ip", header: "Address", cell: (a) => <span className={mono}>{a.ip}</span> },
              { id: "name", header: "Name", cell: (a) => a.name || "—" },
              { id: "subnet", header: "Subnet", cell: (a) => <span className={mono}>{a.subnet}</span> },
              { id: "id", header: "EfficientIP ID", cell: (a) => <span className={mono}>{a.efficientip_id}</span> },
            ]}
          />
        </section>
      )}
      {report.orphan_records.length > 0 && (
        <section aria-label="DNS records no deployment owns" className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">DNS records no deployment owns</h3>
          <DataTable
            label="DNS records no deployment owns"
            rows={report.orphan_records}
            rowKey={(r) => r.efficientip_id}
            minWidth={560}
            columns={[
              { id: "fqdn", header: "Name", cell: (r) => <span className={mono}>{r.fqdn}</span> },
              { id: "type", header: "Type", cell: (r) => r.type },
              { id: "value", header: "Value", cell: (r) => <span className={mono}>{r.value}</span> },
              { id: "id", header: "EfficientIP ID", cell: (r) => <span className={mono}>{r.efficientip_id}</span> },
            ]}
          />
        </section>
      )}
      {report.missing.length > 0 && (
        <section aria-label="Missing in EfficientIP" className="border-t border-line">
          <h3 className="px-4 py-2.5 text-body font-semibold">Owned by a deployment, missing in EfficientIP</h3>
          <DataTable
            label="Missing in EfficientIP"
            rows={report.missing}
            rowKey={(m) => `${m.deployment_id}/${m.name}`}
            minWidth={560}
            columns={[
              {
                id: "deployment",
                header: "Deployment",
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
              { id: "what", header: "What", cell: (m) => <span className={mono}>{m.name}</span> },
              {
                id: "id",
                header: "EfficientIP ID",
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
  const [run, setRun] = useState(false);
  const report = useQuery({ ...q.ipamReconcile(), enabled: run });
  return (
    <Panel
      title="EfficientIP reconcile"
      description="Compares EfficientIP with what rvav recorded. Nothing is changed."
      flush
      actions={
        <Button
          size="sm"
          variant="primary"
          disabled={report.isFetching}
          onClick={() => (run ? void report.refetch() : setRun(true))}
        >
          {report.isFetching ? "Checking…" : report.data ? "Check again" : "Run the check"}
        </Button>
      }
    >
      {!run && (
        <p className="px-4 py-3 text-secondary text-muted">
          It lists the addresses and records tagged for rvav that no deployment owns (for example left by a failed run),
          and those a deployment owns that EfficientIP no longer has. A request running during the check may show its
          newest objects here.
        </p>
      )}
      {run && report.isPending && <SkeletonRows rows={3} cols={4} />}
      {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
      {report.data && <Report report={report.data} />}
    </Panel>
  );
}

export function IntegrationsPage({ tab = "connections" }: { tab?: IntegrationsTab }) {
  const navigate = useNavigate();
  return (
    <Page>
      <PageHeader
        title="Integrations"
        description="Where people sign in, the systems rvav connects to and notifies through, and whether EfficientIP agrees with what rvav recorded."
      />
      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({ to: "/admin/integrations", search: { tab: next as IntegrationsTab }, replace: true })
        }
      >
        <TabsList aria-label="Integrations">
          <TabsTrigger value="connections">Connections</TabsTrigger>
          <TabsTrigger value="checks">Checks</TabsTrigger>
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
