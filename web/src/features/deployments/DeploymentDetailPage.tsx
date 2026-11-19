import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { Power, SquareTerminal } from "lucide-react";
import { lazy, Suspense, useState } from "react";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { CopyText, Time } from "@/components/common/misc";
import { KeyValues, Page } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge, PowerDot, RequestStatusBadge } from "@/components/common/status";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tip } from "@/components/ui/tooltip";
import type {
  Day2Action,
  DeploymentDetail,
  DnsRecordResource,
  DrsRuleResource,
  IpAddressResource,
  SegmentResource,
  SecurityGroupResource,
  FirewallRulesResource,
  GroupMembershipResource,
  VmResource,
} from "@/lib/api/types";
import { cn, fullDate, leaseLabel, newIdempotencyKey } from "@/lib/utils";
import { q } from "@/lib/query";
import { ActionDialog, ActionsMenu, DeploymentActionsMenu, actionFailed, runsDirectly, useRunAction } from "./actions";
import { RemoteAccessPanel } from "./RemoteAccess";
import { VmPropertiesSheet } from "./VmProperties";

const DeploymentTopology = lazy(() => import("./DeploymentTopology"));

export type DeploymentTab = "overview" | "topology" | "history" | "inputs";

type Pick = (a: Day2Action) => void;
/** Opens a VM's properties. */
type Open = (vm: VmResource) => void;

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
  onOpen,
}: {
  deploymentId: string;
  vms: VmResource[];
  actions: Day2Action[];
  onPick: Pick;
  onOpen: Open;
}) {
  if (vms.length === 0) {
    return (
      <p className="px-4 py-6 text-secondary text-muted">
        No VMs yet. They appear here as soon as vCenter creates them.
      </p>
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
                  className="border-b border-line bg-sunken px-4 py-1.5 text-left text-caption font-medium text-muted"
                >
                  {component} · {list.length} VM{list.length === 1 ? "" : "s"}
                </th>
              </tr>
            )}
            {list.map((r) => {
              const { id, name, vm } = r;
              const on = vm.power_state === "on";
              const disk = vm.disks.reduce((n, d) => n + d.capacity_gb, 0);
              const ip = vm.nics.flatMap((n) => n.ip_addresses ?? [])[0];
              const mine = actions.filter((a) => a.resource_id === id);
              const powerBtn = powerAction(mine, on);
              const consoleAction = mine.find((a) => a.key === "console");
              return (
                <tr key={id} className="hover:bg-sunken">
                  <td className="border-b border-line px-4 py-2.5">
                    <span className="flex items-center gap-2">
                      <PowerDot on={on} />
                      <span className="flex flex-col">
                        <button
                          type="button"
                          className="self-start text-left font-medium hover:text-accent hover:underline"
                          onClick={() => onOpen(r)}
                        >
                          <span>{name}</span>
                          <span className="sr-only"> properties</span>
                        </button>
                        <span className="font-mono text-caption text-faint">
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
                    {vm.fqdn && <div className="font-mono text-caption text-faint">{vm.fqdn}</div>}
                  </td>
                  <td className="tnum border-b border-line px-3 py-2.5">
                    {vm.cpu_count} vCPU · {Math.round(vm.memory_mb / 1024)} GB · {disk} GB
                  </td>
                  <td className="border-b border-line px-3 py-2.5">
                    <div className="tnum grid grid-cols-[30px_72px_32px] items-center gap-x-1.5 gap-y-0.5 text-caption text-faint">
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
                      {on && consoleAction?.enabled ? (
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
                        <Tip content={consoleAction?.disabled_reason ?? (on ? "Console" : "The VM is powered off")}>
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
  dhcp,
}: {
  ips: IpAddressResource[];
  dns: DnsRecordResource[];
  segments: SegmentResource[];
  dhcp: VmResource[];
}) {
  if (ips.length + dns.length + segments.length + dhcp.length === 0)
    return <p className="text-secondary text-muted">No network resources yet.</p>;
  return (
    <ul className="flex flex-col gap-1.5">
      {/* A VM whose first adapter is on DHCP: the address it reports, once it has one (ADR-0039). */}
      {dhcp.map((r) => {
        const ip = r.vm.nics[0]?.ip_addresses?.[0];
        return (
          <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
            {ip ? (
              <span className="font-mono text-secondary">{ip}</span>
            ) : (
              <span className="text-secondary text-muted">Address not known yet</span>
            )}
            <span className="text-caption text-faint">DHCP · {r.name}</span>
          </li>
        );
      })}
      {/* NSX segments made for the deployment (ADR-0028). */}
      {segments.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="min-w-0 font-mono text-secondary break-all">{r.name}</span>
          <span className="text-caption text-faint">
            NSX segment · {r.segment.tier1} · {r.segment.subnet}
          </span>
        </li>
      ))}
      {ips.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="font-mono text-secondary">{r.address}</span>
          <span className="text-caption text-faint">EfficientIP · {r.subnet}</span>
        </li>
      ))}
      {dns.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="min-w-0 font-mono text-secondary break-all">{r.fqdn}</span>
          <span className="text-caption text-faint">
            {r.record_type} → {r.value}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** The NSX security groups made for the deployment, the firewall rules into
 * them, and the groups each VM is in; tags removed in NSX are drift (ADR-0028). */
function SecurityGroups({
  groups,
  policies,
  members,
}: {
  groups: SecurityGroupResource[];
  policies: FirewallRulesResource[];
  members: GroupMembershipResource[];
}) {
  // NSX rule IDs are unique within their policy.
  const rules = policies.flatMap((p) => p.rules.map((r) => ({ ...r, key: `${p.id}/${r.id}` })));
  // The deployment's groups by their titles; ANY as people say it.
  const title = (id: string) => groups.find((g) => g.group.id === id)?.group.title ?? id;
  const where = (ids: string[]) => (ids.includes("ANY") ? "anywhere" : ids.map(title).join(", "));
  const what = (services: string[]) => (services.includes("ANY") ? "any service" : services.join(", "));
  return (
    <div className="flex flex-col gap-3 text-secondary">
      {groups.length > 0 && (
        <ul className="flex flex-col gap-2">
          {groups.map((g) => (
            <li key={g.id}>
              <div className="flex flex-wrap items-baseline justify-between gap-x-3">
                <span className="text-body font-medium">{g.group.title ?? g.group.key ?? g.group.id}</span>
                <span className="min-w-0 font-mono text-caption break-all text-faint">{g.group.id}</span>
              </div>
              <ul className="mt-0.5 flex flex-col gap-0.5 text-caption text-muted">
                {/* Every rule naming the group: into it, out of it, or enforced on it. */}
                {rules
                  .filter((r) => [...r.from, ...r.to, ...(r.scope ?? [])].includes(g.group.id))
                  .map((r) => (
                    <li key={r.key}>
                      {r.action === "ALLOW" ? "Allows" : r.action === "DROP" ? "Drops" : "Rejects"} {what(r.services)}{" "}
                      {r.to.includes(g.group.id) ? `from ${where(r.from)}` : `to ${where(r.to)}`}
                    </li>
                  ))}
              </ul>
            </li>
          ))}
        </ul>
      )}
      <ul className="flex flex-col gap-1.5">
        {members.map((m) => (
          <li key={m.id}>
            <div className="flex flex-wrap items-baseline justify-between gap-x-3">
              <span className="font-mono text-secondary">{m.membership.vm}</span>
              <span className="min-w-0 text-caption break-all text-faint">
                {m.membership.groups.map((g) => g.name).join(", ") || "No group"}
              </span>
            </div>
            {m.sync_state === "drifted" && m.membership.missing && (
              <p
                role="status"
                title={m.membership.missing.join(", ")}
                className="mt-1 rounded-md bg-warn-soft px-2 py-1 text-caption text-warn"
              >
                Its tags for {m.membership.out_of?.map((g) => title(g.id)).join(", ") || "a group"} were removed in NSX,
                so the group's firewall rules no longer apply to it. Use Add to a security group to put it back.
              </p>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** A multi-VM deployment: its VMs belong to blueprint components (ADR-0024). */
const isMulti = (vms: VmResource[]) => vms.some((v) => v.vm.component && v.vm.component !== "vm");

/** The values of the VMs, each once, in order. */
const distinct = (vms: VmResource[], of: (v: VmResource) => string | undefined) =>
  [...new Set(vms.map(of).filter((x): x is string => Boolean(x)))].join(", ");

function Placement({ vms, rules }: { vms: VmResource[]; rules: DrsRuleResource[] }) {
  const vm = vms[0];
  if (!vm) return <p className="text-secondary text-muted">Placement is decided when the VM is cloned.</p>;
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
            <dd className="m-0 font-mono text-secondary break-all">{v}</dd>
          </div>
        ))}
      </dl>
      {rules.length > 0 && (
        <ul className="flex flex-col gap-1.5 border-t border-line pt-3" aria-label="Kept on separate hosts">
          {rules.map((r) => (
            <li key={r.id} className="flex flex-col">
              <span className="text-body">
                <span className="font-medium">{r.rule.component}</span> on separate hosts:{" "}
                <span className="font-mono text-secondary">{r.rule.vms.join(", ")}</span>
              </span>
              <span className="text-caption text-faint">
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

const afterExpiry = (d: DeploymentDetail) =>
  d.lease.expiry_action === "delete"
    ? "Then it is deleted."
    : d.lease.expiry_action === "notify_only"
      ? "Then you are told; nothing is deleted."
      : `Then its VMs are powered off, and deleted ${d.lease.grace_days ?? 7} days later.`;

function LeasePanel({ d, extend, onPick }: { d: DeploymentDetail; extend?: Day2Action; onPick: Pick }) {
  const l = d.lease;
  if (!l.expires_at) {
    return <p className="text-secondary text-muted">No expiry: the lease policy keeps it until someone deletes it.</p>;
  }
  return (
    <div className="flex flex-col gap-2.5 text-secondary">
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
        {l.enforcement === "soft"
          ? `${l.max_days ? `; up to ${l.max_days} days` : ""}. The lease policy is soft: going beyond warns you, and needs no approval.`
          : `${l.max_days ? `; up to ${l.max_days} days without approval` : ""}.`}
      </p>
      {extend && (
        <Tip
          content={
            !extend.enabled
              ? extend.disabled_reason
              : extend.approval.required
                ? "Needs approval"
                : extend.warnings?.length
                  ? "A policy warns, and you may go on"
                  : "Extend"
          }
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
      <p className="px-4 pt-2.5 text-secondary text-muted">
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
            <table className="mt-1.5 w-full border-collapse text-secondary" aria-label={`How ${v.name} differs`}>
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

/** What the deployment is, each fact only when it has one. */
function Details({ d }: { d: DeploymentDetail }) {
  const items: { label: string; value: React.ReactNode }[] = [
    {
      label: "Catalog item",
      value: (
        <Link to="/catalog/$itemId" params={{ itemId: d.catalog_item.slug }} className="text-accent hover:underline">
          {d.catalog_item.name}
        </Link>
      ),
    },
    { label: "Version", value: <span className="font-mono text-secondary">{d.version.number}</span> },
    { label: "Project", value: d.project.name },
    { label: "Created", value: <Time iso={d.created_at} /> },
  ];
  if (d.created_by_request_id)
    items.push({
      label: "Created by",
      value: (
        <Link
          to="/requests/$requestId"
          params={{ requestId: d.created_by_request_id }}
          className="font-mono text-accent hover:underline"
        >
          {d.created_by_request_id}
        </Link>
      ),
    });
  if (d.vm_count > 0) items.push({ label: "VMs", value: `${d.vm_count}, ${d.vms_powered_on} powered on` });
  return <KeyValues items={items} />;
}

function Overview({
  d,
  actions,
  onPick,
  onOpen,
}: {
  d: DeploymentDetail;
  actions: Day2Action[];
  onPick: Pick;
  onOpen: Open;
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
  const groups = d.resources.filter((r): r is SecurityGroupResource => r.type === "nsx.group");
  const policies = d.resources.filter((r): r is FirewallRulesResource => r.type === "nsx.policy");
  const members = d.resources.filter((r): r is GroupMembershipResource => r.type === "nsx.membership");
  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_600px] flex-col gap-4">
        <Drift vms={vms} actions={actions} onPick={onPick} />
        <Panel title="Virtual machines" description="Live from vCenter" flush>
          <VmTable deploymentId={d.id} vms={vms} actions={actions} onPick={onPick} onOpen={onOpen} />
        </Panel>
        <Panel title="Network and addresses">
          <Network ips={ips} dns={dns} segments={segments} dhcp={vms.filter((v) => v.vm.dhcp)} />
        </Panel>
        {groups.length + members.length > 0 && (
          <Panel title="Security groups">
            <SecurityGroups groups={groups} policies={policies} members={members} />
          </Panel>
        )}
      </div>
      <aside className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
        <Panel title="Details">
          <Details d={d} />
        </Panel>
        <RemoteAccessPanel d={d} />
        <Panel title="Lease">
          <LeasePanel d={d} extend={actions.find((a) => a.key === "extend_lease")} onPick={onPick} />
        </Panel>
        <Panel title="Placement">
          <Placement vms={vms} rules={rules} />
        </Panel>
      </aside>
    </div>
  );
}

function History({ deploymentId, createdBy }: { deploymentId: string; createdBy?: string }) {
  const history = useQuery(q.deploymentHistory(deploymentId));
  if (history.isError) return <ErrorState error={history.error} onRetry={() => void history.refetch()} />;
  if (!history.data) return <SkeletonRows rows={3} cols={3} />;
  if (history.data.items.length === 0)
    return (
      <p className="text-secondary text-muted">
        No day-2 action has run on it yet.
        {createdBy && (
          <>
            {" "}
            <Link to="/requests/$requestId" params={{ requestId: createdBy }}>
              {createdBy}
            </Link>{" "}
            created it.
          </>
        )}
      </p>
    );
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
              {r.kind === "day2" ? (r.action?.name ?? r.title) : r.kind === "update" ? "Updated" : "Created"}
            </Link>
            <span className="text-muted">
              {" "}
              · {r.kind === "day2"
                ? (r.resource?.name ?? "deployment")
                : `${r.catalog_item.name} ${r.version.number}`}{" "}
              · {r.requester.name} · <span className="font-mono text-caption">{r.number}</span>
            </span>
            {r.error && <span className="block text-caption text-bad">{r.error.message}</span>}
          </span>
          <RequestStatusBadge status={r.status} />
        </li>
      ))}
    </ol>
  );
}

export function DeploymentDetailPage({ deploymentId, tab }: { deploymentId: string; tab: DeploymentTab }) {
  const navigate = useNavigate();
  const dep = useQuery(q.deployment(deploymentId));
  const actionsQ = useQuery(q.deploymentActions(deploymentId));
  const actions = actionsQ.data?.items ?? [];
  const [picked, setPicked] = useState<Day2Action | null>(null);
  // By ID: the sheet follows the VM as live updates replace it.
  const [shown, setShown] = useState<string | null>(null);
  const open: Open = (vm) => setShown(vm.id);
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
  const shownVm = d?.resources.find((r): r is VmResource => r.type === "vsphere.vm" && r.id === shown);
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/deployments">Deployments</Link>
          </Breadcrumbs>
        }
        title={d?.name ?? <Skeleton className="h-5 w-48" />}
        meta={
          d && (
            <>
              <DeploymentStatusBadge status={d.status} />
              <EnvBadge env={d.environment} />
              {d.sync_state !== "in_sync" && (
                <span className="rounded-sm bg-warn-soft px-1.5 py-px text-caption font-medium text-warn">
                  {d.sync_state === "drifted" ? "Changed outside rvav" : "Missing in vCenter"}
                </span>
              )}
              <span>Owner {d.owner.name}</span>
            </>
          )
        }
        actions={
          d && (
            <DeploymentActionsMenu
              deploymentId={deploymentId}
              actions={actions}
              loading={actionsQ.isPending}
              failed={actionsQ.isError}
              onRetry={() => void actionsQ.refetch()}
              onPick={pick}
            />
          )
        }
      />

      <Tabs
        value={tab}
        onValueChange={(next) =>
          void navigate({
            to: "/deployments/$deploymentId",
            params: { deploymentId },
            search: { tab: next === "overview" ? undefined : (next as DeploymentTab) },
            replace: true,
          })
        }
      >
        <TabsList aria-label="Deployment sections">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="topology">Topology</TabsTrigger>
          <TabsTrigger value="history">History</TabsTrigger>
          <TabsTrigger value="inputs">Inputs</TabsTrigger>
        </TabsList>
        {!d ? (
          <div className="mt-4 rounded-lg border border-line bg-surface">
            <SkeletonRows rows={4} cols={4} />
          </div>
        ) : (
          <>
            <TabsContent value="overview" className="pt-4">
              <Overview d={d} actions={actions} onPick={pick} onOpen={open} />
            </TabsContent>
            <TabsContent value="topology" className="pt-4">
              <Suspense
                fallback={
                  <div className="rounded-lg border border-line bg-surface">
                    <SkeletonRows rows={6} cols={2} />
                  </div>
                }
              >
                <DeploymentTopology deployment={d} onOpen={open} />
              </Suspense>
            </TabsContent>
            <TabsContent value="history" className="pt-4">
              <Panel title="History">
                <History deploymentId={deploymentId} createdBy={d?.created_by_request_id} />
              </Panel>
            </TabsContent>
            <TabsContent value="inputs" className="pt-4">
              <Panel
                title="Inputs"
                description={
                  <>
                    What it was requested or last updated with, as passed to the blueprint and to AWX as{" "}
                    <span className="font-mono">extra_vars</span>.
                  </>
                }
                flush
              >
                <pre className="m-0 overflow-x-auto px-4 py-3.5 font-mono text-secondary leading-relaxed">
                  {toYaml(d.inputs)}
                </pre>
              </Panel>
            </TabsContent>
          </>
        )}
      </Tabs>
      {d && picked && <ActionDialog deployment={d} action={picked} onClose={() => setPicked(null)} />}
      {shownVm && <VmPropertiesSheet resource={shownVm} onClose={() => setShown(null)} />}
    </Page>
  );
}
