import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";
import { ErrorState, SkeletonRows } from "@/components/common/feedback";
import { EmptyState, Page, PageHeader, Panel } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import type { IpamReconcile } from "@/lib/api/types";
import { q } from "@/lib/query";
import { IntegrationSettings } from "./IntegrationSettings";

const th = "border-b border-line px-4 py-2 font-medium";
const td = "border-b border-line px-4 py-2 align-top";

function Table({ head, rows }: { head: string[]; rows: React.ReactNode[][] }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[560px] border-collapse text-[13px]">
        <thead>
          <tr className="label text-left">
            {head.map((h) => (
              <th key={h} scope="col" className={th}>
                {h}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((cells, i) => (
            <tr key={i} className="hover:bg-sunken">
              {cells.map((c, j) => (
                <td key={j} className={td}>
                  {c}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function Mono({ children }: { children: React.ReactNode }) {
  return <span className="font-mono text-xs">{children}</span>;
}

function Report({ report }: { report: IpamReconcile }) {
  const clean = report.orphan_addresses.length + report.orphan_records.length + report.missing.length === 0;
  return (
    <div className="flex flex-col">
      <p className="px-4 py-3 text-xs text-muted">
        EfficientIP has {report.managed_addresses} addresses and {report.managed_records} DNS records tagged for rvav.
        Checked {new Date(report.checked_at).toLocaleString()}.
      </p>
      {clean && (
        <EmptyState
          title="Nothing to reconcile"
          body="Every address and record tagged for rvav belongs to a deployment, and every deployment's addresses and records are in EfficientIP."
        />
      )}
      {report.orphan_addresses.length > 0 && (
        <section aria-label="Addresses no deployment owns" className="border-t border-line">
          <h3 className="px-4 pt-3 text-[13px] font-semibold">Addresses no deployment owns</h3>
          <Table
            head={["Address", "Name", "Subnet", "EfficientIP ID"]}
            rows={report.orphan_addresses.map((a) => [
              <Mono key="ip">{a.ip}</Mono>,
              a.name || "—",
              <Mono key="subnet">{a.subnet}</Mono>,
              <Mono key="id">{a.efficientip_id}</Mono>,
            ])}
          />
        </section>
      )}
      {report.orphan_records.length > 0 && (
        <section aria-label="DNS records no deployment owns" className="border-t border-line">
          <h3 className="px-4 pt-3 text-[13px] font-semibold">DNS records no deployment owns</h3>
          <Table
            head={["Name", "Type", "Value", "EfficientIP ID"]}
            rows={report.orphan_records.map((r) => [
              <Mono key="fqdn">{r.fqdn}</Mono>,
              r.type,
              <Mono key="value">{r.value}</Mono>,
              <Mono key="id">{r.efficientip_id}</Mono>,
            ])}
          />
        </section>
      )}
      {report.missing.length > 0 && (
        <section aria-label="Missing in EfficientIP" className="border-t border-line">
          <h3 className="px-4 pt-3 text-[13px] font-semibold">Owned by a deployment, missing in EfficientIP</h3>
          <Table
            head={["Deployment", "What", "EfficientIP ID"]}
            rows={report.missing.map((m) => [
              <Link
                key="dep"
                to="/deployments/$deploymentId"
                params={{ deploymentId: m.deployment_id }}
                className="font-medium text-fg hover:text-accent"
              >
                {m.deployment_name}
              </Link>,
              <Mono key="name">{m.name}</Mono>,
              <Mono key="id">{m.efficientip_id ?? "—"}</Mono>,
            ])}
          />
        </section>
      )}
    </div>
  );
}

export function IntegrationsPage() {
  const [run, setRun] = useState(false);
  const report = useQuery({ ...q.ipamReconcile(), enabled: run });
  return (
    <Page>
      <PageHeader
        title="Integrations"
        meta="How rvav connects to vCenter, AWX and EfficientIP, and whether EfficientIP agrees with what rvav recorded."
      />
      <IntegrationSettings />
      <Panel
        title="EfficientIP reconcile"
        flush
        actions={
          <Button size="sm" disabled={report.isFetching} onClick={() => (run ? void report.refetch() : setRun(true))}>
            {report.isFetching ? "Checking…" : report.data ? "Check again" : "Run the check"}
          </Button>
        }
      >
        {!run && (
          <p className="px-4 py-3 text-xs text-muted">
            Compares EfficientIP with what rvav recorded: addresses and records tagged for rvav that no deployment owns
            (for example left by a failed run), and those a deployment owns that EfficientIP no longer has. Nothing is
            changed. A request running during the check may show its newest objects here.
          </p>
        )}
        {run && report.isPending && <SkeletonRows rows={3} cols={4} />}
        {report.isError && <ErrorState error={report.error} onRetry={() => void report.refetch()} />}
        {report.data && <Report report={report.data} />}
      </Panel>
    </Page>
  );
}
