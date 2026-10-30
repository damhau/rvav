import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plus, Power, RefreshCw, SquareTerminal } from "lucide-react";
import { useState } from "react";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { CopyText, Time } from "@/components/common/misc";
import { Page, Panel } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge, PowerDot, RequestStatusBadge } from "@/components/common/status";
import { Button } from "@/components/ui/button";
import { Tip } from "@/components/ui/tooltip";
import type {
  Day2Action,
  DeploymentDetail,
  DnsRecordResource,
  DrsRuleResource,
  IpAddressResource,
  SegmentResource,
  VmResource,
} from "@/lib/api/types";
import { cn, fullDate, leaseLabel, newIdempotencyKey } from "@/lib/utils";
import { q } from "@/lib/query";
import { ActionDialog, ActionsMenu, actionFailed, runsDirectly, useRunAction } from "./actions";

export type DeploymentTab = "overview" | "history" | "inputs";

const Later = ({ children, what }: { children: React.ReactNode; what: string }) => (
  <Tip content={what}>
    <span>{children}</span>
  </Tip>
);

type Pick = (a: Day2Action) => void;

/** The quick power button: power on when off, shut down (or power off) when on. */
function powerAction(actions: Day2Action[], on: boolean) {
  const by = (k: string) => actions.find((a) => a.key === k);
  if (!on) return by("power_on");
  const shutdown = by("shutdown_guest");
  return shutdown?.enabled ? shutdown : (by("power_off") ?? shutdown);
}

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

function VmTable({
  deploymentId,
  vms,
  actions,
  onPick,
}: {
  deploymentId: string;
  vms: VmResource[];
  actions: Day2Action[];
  onPick: Pick;
}) {
  if (vms.length === 0) {
    return (
      <p className="px-4 py-6 text-xs text-muted">No VMs yet. They appear here as soon as vCenter creates them.</p>
    );
  }
  // By component, in the order they come; one group without components.
  const groups: [string | undefined, VmResource[]][] = [];
  for (const v of vms) {
    const key = isMulti(vms) ? v.vm.component : undefined;
    const last = groups[groups.length - 1];
    if (last && last[0] === key) last[1].push(v);
    else groups.push([key, [v]]);
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
        {/* A multi-VM deployment's VMs, a row group per component (ADR-0024). */}
        {groups.map(([component, list]) => (
          <tbody key={component ?? "vms"}>
            {component && (
              <tr>
                <th
                  scope="rowgroup"
                  colSpan={5}
                  className="border-b border-line bg-sunken px-4 py-1.5 text-left text-[11px] font-medium text-muted"
                >
                  {component} · {list.length} VM{list.length === 1 ? "" : "s"}
                </th>
              </tr>
            )}
            {list.map(({ id, name, vm }) => {
              const on = vm.power_state === "on";
              const disk = vm.disks.reduce((n, d) => n + d.capacity_gb, 0);
              const ip = vm.nics.flatMap((n) => n.ip_addresses ?? [])[0];
              const mine = actions.filter((a) => a.resource_id === id);
              const powerBtn = powerAction(mine, on);
              return (
                <tr key={id} className="hover:bg-sunken">
                  <td className="border-b border-line px-4 py-2.5">
                    <span className="flex items-center gap-2">
                      <PowerDot on={on} />
                      <span className="flex flex-col">
                        <span className="font-medium">{name}</span>
                        <span className="font-mono text-[11px] text-faint">
                          {vm.moref} ·{" "}
                          {on
                            ? `on · tools ${vm.guest_state === "running" ? "running" : "not running"}`
                            : vm.power_state}
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
                      <Tip content={powerBtn?.enabled ? powerBtn.name : (powerBtn?.disabled_reason ?? "Power")}>
                        <span>
                          <Button
                            size="icon"
                            aria-label={`${powerBtn?.name ?? "Power"} ${name}`}
                            disabled={!powerBtn?.enabled}
                            onClick={() => powerBtn && onPick(powerBtn)}
                          >
                            <Power />
                          </Button>
                        </span>
                      </Tip>
                      {on ? (
                        <Tip content="Console">
                          <Button size="icon" asChild>
                            <Link
                              to="/deployments/$deploymentId/console/$resourceId"
                              params={{ deploymentId, resourceId: id }}
                              aria-label={`Open the console of ${name}`}
                            >
                              <SquareTerminal />
                            </Link>
                          </Button>
                        </Tip>
                      ) : (
                        <Tip content="The VM is powered off">
                          <span>
                            <Button size="icon" aria-label={`Open the console of ${name}`} disabled>
                              <SquareTerminal />
                            </Button>
                          </span>
                        </Tip>
                      )}
                      <ActionsMenu
                        actions={mine}
                        label={`More actions for ${name}`}
                        onPick={onPick}
                        consoleLink={{ deploymentId, resourceId: id }}
                      />
                    </span>
                  </td>
                </tr>
              );
            })}
          </tbody>
        ))}
      </table>
    </div>
  );
}

function Network({
  ips,
  dns,
  segments,
}: {
  ips: IpAddressResource[];
  dns: DnsRecordResource[];
  segments: SegmentResource[];
}) {
  if (ips.length + dns.length + segments.length === 0)
    return <p className="text-xs text-muted">No addresses reserved yet.</p>;
  return (
    <ul className="flex flex-col gap-1.5">
      {/* NSX segments made for the deployment (ADR-0028). */}
      {segments.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="min-w-0 font-mono text-[12px] break-all">{r.name}</span>
          <span className="text-[11px] text-faint">
            NSX segment · {r.segment.tier1} · {r.segment.subnet}
          </span>
        </li>
      ))}
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

/** A multi-VM deployment: its VMs belong to blueprint components (ADR-0024). */
const isMulti = (vms: VmResource[]) => vms.some((v) => v.vm.component && v.vm.component !== "vm");

/** The values of the VMs, each once, in order. */
const distinct = (vms: VmResource[], of: (v: VmResource) => string | undefined) =>
  [...new Set(vms.map(of).filter((x): x is string => Boolean(x)))].join(", ");

function Placement({ vms, rules }: { vms: VmResource[]; rules: DrsRuleResource[] }) {
  const vm = vms[0];
  if (!vm) return <p className="text-xs text-muted">Placement is decided when the VM is cloned.</p>;
  // Several VMs may be in several places: each value once, the label plural then.
  const row = (one: string, value: string) => [value.includes(", ") ? `${one}s` : one, value];
  const rows = [
    row(
      "Cluster",
      distinct(vms, (v) => v.vm.cluster),
    ),
    row(
      "Datastore",
      distinct(vms, (v) => v.vm.disks[0]?.datastore),
    ),
    row(
      "Folder",
      distinct(vms, (v) => v.vm.folder),
    ),
    row(
      "Network",
      distinct(vms, (v) => v.vm.nics[0]?.network),
    ),
    row(
      "Template",
      distinct(vms, (v) => v.vm.template),
    ),
    row(
      "vCenter",
      distinct(vms, (v) => v.vm.vcenter),
    ),
  ].filter((r): r is [string, string] => Boolean(r[1]));
  return (
    <div className="flex flex-col gap-3">
      <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3.5 gap-y-1.5">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted">{k}</dt>
            <dd className="m-0 font-mono text-[12px] break-all">{v}</dd>
          </div>
        ))}
      </dl>
      {rules.length > 0 && (
        <ul className="flex flex-col gap-1.5 border-t border-line pt-3" aria-label="Kept on separate hosts">
          {rules.map((r) => (
            <li key={r.id} className="flex flex-col">
              <span className="text-[13px]">
                <span className="font-medium">{r.rule.component}</span> on separate hosts:{" "}
                <span className="font-mono text-[12px]">{r.rule.vms.join(", ")}</span>
              </span>
              <span className="text-[11px] text-faint">
                DRS rule {r.name} in {r.rule.cluster}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
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

const tileHint: Record<string, string> = {
  resize: "CPU and memory, hot-add aware",
  disk_add: "Thin, on the VM's datastore",
  snapshot_create: "Kept as long as the environment allows",
  change_owner: "Hand it to another member of the project",
  change_project: "The new project's quota and placement are checked",
  delete: "Releases its IP addresses and DNS records too",
};

function ActionTiles({ actions, loading, onPick }: { actions: Day2Action[]; loading: boolean; onPick: Pick }) {
  if (loading) return <SkeletonRows rows={2} cols={3} />;
  if (actions.length === 0) return <p className="px-4 py-6 text-xs text-muted">No actions apply here.</p>;
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,230px),1fr))] gap-px bg-line">
      {actions.map((a) => (
        <button
          key={`${a.key}-${a.resource_id ?? ""}`}
          type="button"
          disabled={!a.enabled}
          onClick={() => onPick(a)}
          className="flex w-full flex-col items-start gap-0.5 bg-surface px-4 py-2.5 text-left hover:bg-sunken disabled:cursor-not-allowed disabled:hover:bg-surface"
        >
          <span
            className={cn(
              "font-medium",
              !a.enabled && "text-muted",
              a.key === "delete" && (a.enabled ? "text-bad" : "text-bad/60"),
            )}
          >
            {a.name}
            {a.resource_id && (
              <span className="ml-1.5 font-mono text-[11px] font-normal text-faint">{a.resource_name}</span>
            )}
          </span>
          <span className="text-[11px] text-faint">
            {!a.enabled
              ? a.disabled_reason
              : a.approval.required
                ? `Needs approval: ${a.approval.reasons.join("; ")}`
                : tileHint[a.key]}
          </span>
        </button>
      ))}
    </div>
  );
}

const afterExpiry = (d: DeploymentDetail) =>
  d.lease.expiry_action === "delete"
    ? "Then it is deleted."
    : d.lease.expiry_action === "notify_only"
      ? "Then you are told; nothing is deleted."
      : `Then its VMs are powered off, and deleted ${d.lease.grace_days ?? 7} days later.`;

function LeasePanel({ d, extend, onPick }: { d: DeploymentDetail; extend?: Day2Action; onPick: Pick }) {
  const l = d.lease;
  if (!l.expires_at) {
    return <p className="text-xs text-muted">No expiry: the lease policy keeps it until someone deletes it.</p>;
  }
  return (
    <div className="flex flex-col gap-2.5 text-xs">
      {l.expired_at ? (
        <p role="status" className="rounded-md bg-warn-soft px-3 py-2 text-warn">
          The lease ended on {fullDate(l.expires_at)} and its VMs were powered off.
          {l.deletes_at && ` It is deleted on ${fullDate(l.deletes_at)} unless you extend the lease.`}
        </p>
      ) : (
        <p className="text-muted">
          {leaseLabel(l.expires_at)} ({fullDate(l.expires_at)}). {afterExpiry(d)}
        </p>
      )}
      <p className="text-faint">
        {l.extensions_used ?? 0} of {l.extensions_max ?? 0} extensions used
        {l.max_days ? `; up to ${l.max_days} days without approval` : ""}.
      </p>
      {extend && (
        <Tip
          content={extend.enabled ? (extend.approval.required ? "Needs approval" : "Extend") : extend.disabled_reason}
        >
          <span className="self-start">
            <Button size="sm" disabled={!extend.enabled} onClick={() => onPick(extend)}>
              Extend the lease
            </Button>
          </span>
        </Tip>
      )}
    </div>
  );
}

/** VMs changed in vCenter outside rvav, with what differs (S13.2). */
function Drift({ vms, actions, onPick }: { vms: VmResource[]; actions: Day2Action[]; onPick: Pick }) {
  const drifted = vms.filter((v) => v.sync_state === "drifted" && v.drift?.length);
  if (drifted.length === 0) return null;
  return (
    <Panel title="Changed outside rvav" flush className="border-warn/40">
      <p className="px-4 pt-2.5 text-xs text-muted">
        {drifted.length === 1 ? "This VM differs" : "These VMs differ"} in vCenter from how rvav left{" "}
        {drifted.length === 1 ? "it" : "them"}. Change {drifted.length === 1 ? "it" : "them"} back in vCenter, or accept
        the changes.
      </p>
      {drifted.map((v) => {
        const accept = actions.find((a) => a.key === "accept_changes" && a.resource_id === v.id);
        return (
          <div key={v.id} className="border-b border-line px-4 py-2.5 last:border-0">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="font-medium">{v.name}</span>
              {accept && (
                <Tip
                  content={accept.enabled ? "Take the VM as it is in vCenter" : (accept.disabled_reason ?? "Not now")}
                >
                  <span>
                    <Button size="sm" disabled={!accept.enabled} onClick={() => onPick(accept)}>
                      Accept the changes
                    </Button>
                  </span>
                </Tip>
              )}
            </div>
            <table className="mt-1.5 w-full border-collapse text-xs" aria-label={`How ${v.name} differs`}>
              <thead>
                <tr className="label text-left">
                  <th scope="col" className="py-1 pr-3 font-medium">
                    What
                  </th>
                  <th scope="col" className="py-1 pr-3 font-medium">
                    As rvav left it
                  </th>
                  <th scope="col" className="py-1 font-medium">
                    In vCenter
                  </th>
                </tr>
              </thead>
              <tbody>
                {v.drift!.map((x) => (
                  <tr key={x.property + x.label}>
                    <th scope="row" className="py-1 pr-3 text-left font-normal">
                      {x.label}
                    </th>
                    <td className="tnum py-1 pr-3 text-muted">{x.rvav ?? "none"}</td>
                    <td className="tnum py-1 font-medium">{x.vcenter ?? "removed"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      })}
    </Panel>
  );
}

function Overview({
  d,
  actions,
  loading,
  onPick,
}: {
  d: DeploymentDetail;
  actions: Day2Action[];
  loading: boolean;
  onPick: Pick;
}) {
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const rules = d.resources.filter((r): r is DrsRuleResource => r.type === "vsphere.drs_rule");
  if (isMulti(vms)) {
    // By component, in the order they first appear.
    const order = [...new Set(vms.map((v) => v.vm.component))];
    vms.sort((a, b) => order.indexOf(a.vm.component) - order.indexOf(b.vm.component));
  }
  const ips = d.resources.filter((r): r is IpAddressResource => r.type === "ipam.address");
  const dns = d.resources.filter((r): r is DnsRecordResource => r.type === "dns.record");
  const segments = d.resources.filter((r): r is SegmentResource => r.type === "nsx.segment");
  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_600px] flex-col gap-4">
        <Drift vms={vms} actions={actions} onPick={onPick} />
        <Panel
          title="Virtual machines"
          flush
          actions={<span className="text-[11px] text-faint">Live from vCenter</span>}
        >
          <VmTable deploymentId={d.id} vms={vms} actions={actions} onPick={onPick} />
        </Panel>
        <Panel
          title="Actions"
          flush
          actions={<span className="text-[11px] text-faint">More in each VM&apos;s menu</span>}
        >
          <ActionTiles
            loading={loading}
            actions={actions.filter(
              (a) =>
                (a.resource_id === null || a.resource_id === undefined
                  ? a.key in tileHint
                  : a.resource_id === vms[0]?.id && ["resize", "disk_add", "snapshot_create"].includes(a.key)) &&
                a.key !== "console",
            )}
            onPick={onPick}
          />
        </Panel>
      </div>
      <aside className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
        <Panel title="Placement">
          <Placement vms={vms} rules={rules} />
        </Panel>
        <Panel title="IP addresses and DNS">
          <Network ips={ips} dns={dns} segments={segments} />
        </Panel>
        <Panel title="Lease">
          <LeasePanel d={d} extend={actions.find((a) => a.key === "extend_lease")} onPick={onPick} />
        </Panel>
      </aside>
    </div>
  );
}

function History({ deploymentId }: { deploymentId: string }) {
  const history = useQuery(q.deploymentHistory(deploymentId));
  if (history.isError) return <ErrorState error={history.error} onRetry={() => void history.refetch()} />;
  if (!history.data) return <SkeletonRows rows={3} cols={3} />;
  if (history.data.items.length === 0) return <p className="text-xs text-muted">Nothing has run on it yet.</p>;
  return (
    <ol className="flex flex-col">
      {history.data.items.map((r) => (
        <li
          key={r.id}
          className="grid grid-cols-[110px_minmax(0,1fr)_auto] items-baseline gap-3 border-b border-line py-2 last:border-0"
        >
          <span className="tnum text-faint">
            <Time iso={r.submitted_at} />
          </span>
          <span className="min-w-0">
            <Link to="/requests/$requestId" params={{ requestId: r.number }} className="font-medium">
              {r.kind === "day2" ? (r.action?.name ?? r.title) : "Created"}
            </Link>
            <span className="text-muted">
              {" "}
              · {r.kind === "day2"
                ? (r.resource?.name ?? "deployment")
                : `${r.catalog_item.name} ${r.version.number}`}{" "}
              · {r.requester.name} · <span className="font-mono text-[11px]">{r.number}</span>
            </span>
            {r.error && <span className="block text-[11px] text-bad">{r.error.message}</span>}
          </span>
          <RequestStatusBadge status={r.status} />
        </li>
      ))}
    </ol>
  );
}

export function DeploymentDetailPage({ deploymentId, tab }: { deploymentId: string; tab: DeploymentTab }) {
  const dep = useQuery(q.deployment(deploymentId));
  const actionsQ = useQuery(q.deploymentActions(deploymentId));
  const actions = actionsQ.data?.items ?? [];
  const [picked, setPicked] = useState<Day2Action | null>(null);
  const run = useRunAction(deploymentId);
  const pick: Pick = (a) => {
    if (runsDirectly(a)) {
      const draft = { action: a.key, ...(a.resource_id ? { resource_id: a.resource_id } : {}), inputs: {} };
      run.mutate({ draft, key: newIdempotencyKey() }, { onError: actionFailed });
      return;
    }
    setPicked(a);
  };
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
            <Later what="AWX actions come with the AWX integration">
              <Button disabled>
                <RefreshCw /> Patch now <span className="font-mono text-[10px] text-faint">AWX</span>
              </Button>
            </Later>
            <Later what="Scaling out comes later">
              <Button disabled>
                <Plus /> Add VM
              </Button>
            </Later>
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
          <History deploymentId={deploymentId} />
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
        <Overview d={d} actions={actions} loading={actionsQ.isPending} onPick={pick} />
      )}
      {d && picked && <ActionDialog deployment={d} action={picked} onClose={() => setPicked(null)} />}
    </Page>
  );
}
