import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Camera, Ellipsis, Plus, Power, RefreshCw, SquareTerminal } from "lucide-react";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { CopyText, Time } from "@/components/common/misc";
import { Page, Panel } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge, PowerDot } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Tip } from "@/components/ui/tooltip";
import type { DeploymentDetail, DnsRecordResource, IpAddressResource, VmResource } from "@/lib/api/types";
import { cn, fullDate, leaseLabel } from "@/lib/utils";
import { q } from "@/lib/query";

export type DeploymentTab = "overview" | "history" | "inputs";

const Slice2 = ({ children }: { children: React.ReactNode }) => (
  <Tip content="Coming in slice 2">
    <span>{children}</span>
  </Tip>
);

function Usage({ label, pct, on }: { label: string; pct?: number; on: boolean }) {
  const v = on && pct !== undefined ? Math.round(pct) : null;
  return (
    <>
      <span>{label}</span>
      <span className="h-1 overflow-hidden rounded-full bg-sunken" aria-hidden>
        <span className={cn("block h-full", (v ?? 0) > 80 ? "bg-warn" : "bg-accent")} style={{ width: `${v ?? 0}%` }} />
      </span>
      <span>{v === null ? "–" : `${v}%`}</span>
    </>
  );
}

function VmTable({ vms }: { vms: VmResource[] }) {
  if (vms.length === 0) {
    return (
      <p className="px-4 py-6 text-xs text-muted">No VMs yet. They appear here as soon as vCenter creates them.</p>
    );
  }
  return (
    <div className="overflow-x-auto">
      <table className="w-full min-w-[760px] border-collapse">
        <thead>
          <tr className="label text-left">
            <th scope="col" className="border-b border-line px-4 py-2 font-medium">
              Name
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              Address
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              Size
            </th>
            <th scope="col" className="border-b border-line px-3 py-2 font-medium">
              CPU · Memory
            </th>
            <th scope="col" className="border-b border-line px-4 py-2 text-right font-medium">
              Actions
            </th>
          </tr>
        </thead>
        <tbody>
          {vms.map(({ id, name, vm }) => {
            const on = vm.power_state === "on";
            const disk = vm.disks.reduce((n, d) => n + d.capacity_gb, 0);
            const ip = vm.nics.flatMap((n) => n.ip_addresses ?? [])[0];
            return (
              <tr key={id} className="hover:bg-sunken">
                <td className="border-b border-line px-4 py-2.5">
                  <span className="flex items-center gap-2">
                    <PowerDot on={on} />
                    <span className="flex flex-col">
                      <span className="font-medium">{name}</span>
                      <span className="font-mono text-[11px] text-faint">
                        {vm.moref} ·{" "}
                        {on ? `on · tools ${vm.guest_state === "running" ? "running" : "not running"}` : vm.power_state}
                      </span>
                    </span>
                  </span>
                </td>
                <td className="border-b border-line px-3 py-2.5">
                  {ip ? <CopyText value={ip} /> : <span className="text-faint">waiting for IP</span>}
                  {vm.fqdn && <div className="font-mono text-[11px] text-faint">{vm.fqdn}</div>}
                </td>
                <td className="tnum border-b border-line px-3 py-2.5">
                  {vm.cpu_count} vCPU · {Math.round(vm.memory_mb / 1024)} GB · {disk} GB
                </td>
                <td className="border-b border-line px-3 py-2.5">
                  <div className="tnum grid grid-cols-[30px_72px_32px] items-center gap-x-1.5 gap-y-0.5 text-[11px] text-faint">
                    <Usage label="CPU" pct={vm.usage?.cpu_pct} on={on} />
                    <Usage label="Mem" pct={vm.usage?.memory_pct} on={on} />
                  </div>
                </td>
                <td className="border-b border-line px-4 py-2.5 text-right whitespace-nowrap">
                  <span className="inline-flex gap-1">
                    <Slice2>
                      <Button size="icon" aria-label={on ? `Shut down ${name}` : `Power on ${name}`} disabled>
                        <Power />
                      </Button>
                    </Slice2>
                    <Slice2>
                      <Button size="icon" aria-label={`Open the console of ${name}`} disabled>
                        <SquareTerminal />
                      </Button>
                    </Slice2>
                    <Slice2>
                      <Button size="icon" aria-label={`More actions for ${name}`} disabled>
                        <Ellipsis />
                      </Button>
                    </Slice2>
                  </span>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function Network({ ips, dns }: { ips: IpAddressResource[]; dns: DnsRecordResource[] }) {
  if (ips.length + dns.length === 0) return <p className="text-xs text-muted">No addresses reserved yet.</p>;
  return (
    <ul className="flex flex-col gap-1.5">
      {ips.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="font-mono text-[12px]">{r.address}</span>
          <span className="text-[11px] text-faint">EfficientIP · {r.subnet}</span>
        </li>
      ))}
      {dns.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="min-w-0 font-mono text-[12px] break-all">{r.fqdn}</span>
          <span className="text-[11px] text-faint">
            {r.record_type} → {r.value}
          </span>
        </li>
      ))}
    </ul>
  );
}

function Placement({ vm }: { vm?: VmResource }) {
  if (!vm) return <p className="text-xs text-muted">Placement is decided when the VM is cloned.</p>;
  const rows = [
    ["Cluster", vm.vm.cluster],
    ["Datastore", vm.vm.disks[0]?.datastore],
    ["Folder", vm.vm.folder],
    ["Network", vm.vm.nics[0]?.network],
    ["Template", vm.vm.template],
    ["vCenter", vm.vm.vcenter],
  ].filter((r): r is [string, string] => Boolean(r[1]));
  return (
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="text-muted">{k}</dt>
          <dd className="m-0 font-mono text-[12px] break-all">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function toYaml(value: unknown, indent = 0): string {
  const pad = "  ".repeat(indent);
  if (Array.isArray(value)) return value.map((v) => `\n${pad}- ${String(v as string)}`).join("");
  if (value && typeof value === "object") {
    return Object.entries(value as Record<string, unknown>)
      .map(([k, v]) =>
        Array.isArray(v) || (v && typeof v === "object")
          ? `${pad}${k}:${toYaml(v, indent + 1)}`
          : `${pad}${k}: ${String(v as string)}`,
      )
      .join("\n");
  }
  return String(value);
}

function Overview({ d }: { d: DeploymentDetail }) {
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const ips = d.resources.filter((r): r is IpAddressResource => r.type === "ipam.address");
  const dns = d.resources.filter((r): r is DnsRecordResource => r.type === "dns.record");
  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_600px] flex-col gap-4">
        <Panel
          title="Virtual machines"
          flush
          actions={<span className="text-[11px] text-faint">Live from vCenter</span>}
        >
          <VmTable vms={vms} />
        </Panel>
        <Panel
          title="Actions"
          flush
          actions={<span className="text-[11px] text-faint">Day-2 actions come in slice 2</span>}
        >
          <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,230px),1fr))] gap-px bg-line">
            {[
              ["Resize", "CPU and memory, hot-add aware"],
              ["Add or extend disk", vms[0]?.vm.disks[0]?.datastore ?? "on the current datastore"],
              ["Snapshots", "Kept 72 h in production"],
              ["Install monitoring agent", "AWX · monitoring-agent"],
              ["Renew TLS certificate", "AWX · pki-renew-deploy"],
              [
                "Delete deployment",
                d.environment === "prd" ? "Needs approval in production" : "Releases IP and DNS too",
              ],
            ].map(([title, hint]) => (
              <Slice2 key={title}>
                <button
                  type="button"
                  disabled
                  className="flex w-full flex-col items-start gap-0.5 bg-surface px-4 py-2.5 text-left disabled:cursor-not-allowed"
                >
                  <span className={cn("font-medium text-muted", title === "Delete deployment" && "text-bad/70")}>
                    {title}
                  </span>
                  <span className="text-[11px] text-faint">{hint}</span>
                </button>
              </Slice2>
            ))}
          </div>
        </Panel>
      </div>
      <aside className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
        <Panel title="Placement">
          <Placement vm={vms[0]} />
        </Panel>
        <Panel title="IP addresses and DNS">
          <Network ips={ips} dns={dns} />
        </Panel>
        <Panel title="Lease">
          <p className="text-xs text-muted">
            {d.lease.expires_at
              ? `${leaseLabel(d.lease.expires_at)} (${fullDate(d.lease.expires_at)}). Then powered off, and deleted after a grace period. ${d.lease.extensions_used ?? 0} of ${d.lease.extensions_max ?? 0} extensions used.`
              : "No expiry. The production lease policy keeps deployments until someone deletes them."}
          </p>
        </Panel>
      </aside>
    </div>
  );
}

export function DeploymentDetailPage({ deploymentId, tab }: { deploymentId: string; tab: DeploymentTab }) {
  const dep = useQuery(q.deployment(deploymentId));
  if (dep.isError) {
    return (
      <Page>
        <ErrorState
          error={dep.error}
          onRetry={() => void dep.refetch()}
          className="rounded-lg border border-line bg-surface"
        />
      </Page>
    );
  }
  const d = dep.data;
  const tabs: [DeploymentTab, string][] = [
    ["overview", "Overview"],
    ["history", "History"],
    ["inputs", "Inputs"],
  ];
  return (
    <Page>
      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-faint">
          <Link to="/deployments">Deployments</Link> / {d?.name ?? "…"}
        </span>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2.5">
            <h1 className="text-lg font-semibold tracking-[-0.01em]">{d?.name ?? <Skeleton className="h-5 w-48" />}</h1>
            {d && <EnvBadge env={d.environment} />}
            {d && <DeploymentStatusBadge status={d.status} />}
            {d && d.sync_state !== "in_sync" && (
              <span className="rounded-sm bg-warn-soft px-1.5 py-px text-[11px] font-medium text-warn">
                {d.sync_state === "drifted" ? "Changed outside rvav" : "Missing in vCenter"}
              </span>
            )}
          </div>
          <div className="flex flex-wrap gap-2">
            <Slice2>
              <Button disabled>
                <Camera /> Snapshot all
              </Button>
            </Slice2>
            <Slice2>
              <Button disabled>
                <RefreshCw /> Patch now <span className="font-mono text-[10px] text-faint">AWX</span>
              </Button>
            </Slice2>
            <Slice2>
              <Button disabled>
                <Plus /> Add VM
              </Button>
            </Slice2>
          </div>
        </div>
        {d && (
          <div className="flex flex-wrap gap-x-3.5 gap-y-1 text-xs text-muted">
            <span>
              {d.catalog_item.name} <span className="font-mono text-[11px]">{d.version.number}</span>
            </span>
            <span>Owner {d.owner.name}</span>
            <span>
              Created <Time iso={d.created_at} />
              {d.created_by_request_id && (
                <>
                  {" "}
                  by{" "}
                  <Link to="/requests/$requestId" params={{ requestId: d.created_by_request_id }}>
                    {d.created_by_request_id}
                  </Link>
                </>
              )}
            </span>
            <span>{d.lease.expires_at ? `Lease: ${leaseLabel(d.lease.expires_at).toLowerCase()}` : "No lease"}</span>
          </div>
        )}
      </div>

      <nav aria-label="Deployment sections" className="flex gap-4 overflow-x-auto border-b border-line">
        {tabs.map(([key, label]) => (
          <Link
            key={key}
            to="/deployments/$deploymentId"
            params={{ deploymentId }}
            search={{ tab: key === "overview" ? undefined : key }}
            aria-current={tab === key ? "page" : undefined}
            className={cn(
              "-mb-px border-b-2 pt-1 pb-2 whitespace-nowrap",
              tab === key
                ? "border-accent font-medium text-fg hover:text-fg"
                : "border-transparent text-muted hover:text-fg",
            )}
          >
            {label}
          </Link>
        ))}
      </nav>

      {!d ? (
        <div className="rounded-lg border border-line bg-surface">
          <SkeletonRows rows={4} cols={4} />
        </div>
      ) : tab === "history" ? (
        <Panel title="History">
          <ol className="flex flex-col">
            <li className="grid grid-cols-[110px_minmax(0,1fr)] gap-3 py-1">
              <span className="tnum text-faint">
                <Time iso={d.created_at} />
              </span>
              <span>
                <b className="font-medium">Created</b>
                {d.created_by_request_id && (
                  <>
                    {" "}
                    by{" "}
                    <Link to="/requests/$requestId" params={{ requestId: d.created_by_request_id }}>
                      {d.created_by_request_id}
                    </Link>
                  </>
                )}{" "}
                · {d.catalog_item.name} {d.version.number}
              </span>
            </li>
          </ol>
          <p className="mt-3 text-xs text-faint">
            Day-2 runs and their timelines appear here once day-2 actions arrive in slice 2.
          </p>
        </Panel>
      ) : tab === "inputs" ? (
        <Panel title="Inputs" flush>
          <p className="border-b border-line px-4 py-2.5 text-xs text-muted">
            Values submitted with {d.created_by_request_id ?? "the creating request"}, as passed to the blueprint and to
            AWX as <span className="font-mono">extra_vars</span>.
          </p>
          <pre className="m-0 overflow-x-auto px-4 py-3.5 font-mono text-[12px] leading-relaxed">
            {toYaml(d.inputs)}
          </pre>
        </Panel>
      ) : (
        <Overview d={d} />
      )}
    </Page>
  );
}
