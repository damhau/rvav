import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { ExternalLink, Power, SquareTerminal } from "lucide-react";
import { lazy, Suspense, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { ErrorState, Skeleton, SkeletonRows } from "@/components/common/feedback";
import { CopyText, Time } from "@/components/common/misc";
import { KeyValues, Page } from "@/components/common/page";
import { DeploymentStatusBadge, EnvBadge, PowerDot, RequestStatusBadge } from "@/components/common/status";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tip } from "@/components/ui/tooltip";
import { Truncate } from "@/components/ui/truncate";
import type {
  CustomResource,
  Day2Action,
  DeploymentDetail,
  DnsRecordResource,
  DrsRuleResource,
  IpAddressResource,
  SegmentResource,
  SecurityGroupResource,
  FirewallRulesResource,
  GroupMembershipResource,
  AdComputerResource,
  VmResource,
} from "@/lib/api/types";
import { cn, newIdempotencyKey } from "@/lib/utils";
import { formatPercent, formatSize, fullDate, leaseLabel } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { q } from "@/lib/query";
import { ActionDialog, ActionsMenu, DeploymentActionsMenu, actionFailed, runsDirectly, useRunAction } from "./actions";
import { componentTitles } from "./componentTitles";
import { CustomResourcesPanel } from "./CustomResources";
import { RemoteAccessPanel } from "./RemoteAccess";
import { VmPropertiesSheet } from "./VmProperties";
import { day2Name } from "@/lib/i18n/names";

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
      <span>{v === null ? "–" : formatPercent(v / 100)}</span>
    </>
  );
}

function VmActions({
  deploymentId,
  vm: r,
  actions,
  onPick,
}: {
  deploymentId: string;
  vm: VmResource;
  actions: Day2Action[];
  onPick: Pick;
}) {
  const { t } = useTranslation("deployments");
  const { id, name, vm } = r;
  const on = vm.power_state === "on";
  const mine = actions.filter((a) => a.resource_id === id);
  const powerBtn = powerAction(mine, on);
  const consoleAction = mine.find((a) => a.key === "console");
  return (
    <span className="inline-flex gap-1">
      <Tip content={powerBtn?.enabled ? day2Name(powerBtn) : (powerBtn?.disabled_reason ?? t("vms.power"))}>
        <span>
          <Button
            size="icon"
            aria-label={t("vms.powerLabel", {
              action: powerBtn ? day2Name(powerBtn) : t("vms.power"),
              name,
            })}
            disabled={!powerBtn?.enabled}
            onClick={() => powerBtn && onPick(powerBtn)}
          >
            <Power />
          </Button>
        </span>
      </Tip>
      {on && consoleAction?.enabled ? (
        <Tip content={t("vms.console")}>
          <Button size="icon" asChild>
            <Link
              to="/deployments/$deploymentId/console/$resourceId"
              params={{ deploymentId, resourceId: id }}
              aria-label={t("vms.openConsole", { name })}
            >
              <SquareTerminal />
            </Link>
          </Button>
        </Tip>
      ) : (
        <Tip content={consoleAction?.disabled_reason ?? (on ? t("vms.console") : t("vms.poweredOff"))}>
          <span>
            <Button size="icon" aria-label={t("vms.openConsole", { name })} disabled>
              <SquareTerminal />
            </Button>
          </span>
        </Tip>
      )}
      <ActionsMenu
        actions={mine}
        label={t("moreActionsFor", { name })}
        onPick={onPick}
        consoleLink={{ deploymentId, resourceId: id }}
      />
    </span>
  );
}

function VmTable({
  deploymentId,
  vms,
  titles,
  actions,
  onPick,
  onOpen,
}: {
  deploymentId: string;
  vms: VmResource[];
  /** Each component's title, by key. */
  titles: Map<string, string>;
  actions: Day2Action[];
  onPick: Pick;
  onOpen: Open;
}) {
  const { t } = useTranslation("deployments");
  const columns: Column<VmResource>[] = [
    {
      id: "name",
      header: t("vms.name"),
      cell: (r) => {
        const { name, vm } = r;
        const on = vm.power_state === "on";
        return (
          <span className="flex items-center gap-2">
            <PowerDot on={on} />
            <span className="flex flex-col">
              <button
                type="button"
                className="self-start text-left font-medium whitespace-nowrap hover:text-accent hover:underline"
                onClick={() => onOpen(r)}
                aria-label={t("vms.properties", { name })}
              >
                {name}
              </button>
              <span className="font-mono text-caption text-faint">
                {vm.moref} ·{" "}
                {on
                  ? t(vm.guest_state === "running" ? "vms.state.toolsRunning" : "vms.state.toolsNotRunning")
                  : t(vm.power_state === "suspended" ? "vms.state.suspended" : "vms.state.off")}
              </span>
            </span>
          </span>
        );
      },
    },
    {
      id: "address",
      header: t("vms.address"),
      cell: ({ vm }) => {
        const ip = vm.nics.flatMap((n) => n.ip_addresses ?? [])[0];
        return (
          <>
            {ip ? <CopyText value={ip} /> : <span className="text-faint">{t("vms.waitingForIp")}</span>}
            {vm.fqdn && (
              <Truncate mono className="max-w-40 text-caption text-faint sm:max-w-56">
                {vm.fqdn}
              </Truncate>
            )}
          </>
        );
      },
    },
    {
      id: "size",
      header: t("vms.size"),
      className: "tnum",
      cell: ({ vm }) =>
        t("vms.sizeValue", {
          cpu: vm.cpu_count,
          memory: unbroken(formatSize(Math.round(vm.memory_mb / 1024), "GB")),
          disk: unbroken(
            formatSize(
              vm.disks.reduce((n, d) => n + d.capacity_gb, 0),
              "GB",
            ),
          ),
        }),
    },
    {
      id: "usage",
      header: t("vms.usage"),
      cell: ({ vm }) => {
        const on = vm.power_state === "on";
        return (
          <div className="tnum grid grid-cols-[30px_72px_32px] items-center gap-x-1.5 gap-y-0.5 text-caption text-faint">
            <Usage label="CPU" pct={vm.usage?.cpu_pct} on={on} />
            <Usage label={t("vms.mem")} pct={vm.usage?.memory_pct} on={on} />
          </div>
        );
      },
    },
    {
      id: "actions",
      header: t("vms.actions"),
      align: "end",
      className: "whitespace-nowrap",
      cell: (r) => <VmActions deploymentId={deploymentId} vm={r} actions={actions} onPick={onPick} />,
    },
  ];
  return (
    <DataTable
      label={t("detail.vms")}
      columns={columns}
      rows={vms}
      rowKey={(r) => r.id}
      minWidth={760}
      // A multi-VM deployment's VMs, a group per component (ADR-0024).
      groups={
        isMulti(vms)
          ? {
              by: (v) => v.vm.component,
              label: (component, list) => {
                const title = titles.get(component) ?? component;
                return (
                  <>
                    {t("vms.group", { component: title, count: list.length })}
                    {title !== component && (
                      <>
                        {" "}
                        <span className="ms-1 font-mono font-normal text-faint">{component}</span>
                      </>
                    )}
                  </>
                );
              },
            }
          : undefined
      }
      empty={<p className="px-4 py-6 text-secondary text-muted">{t("vms.none")}</p>}
    />
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
  const { t } = useTranslation("deployments");
  if (ips.length + dns.length + segments.length + dhcp.length === 0)
    return <p className="text-secondary text-muted">{t("network.none")}</p>;
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
              <span className="text-secondary text-muted">{t("network.addressNotKnown")}</span>
            )}
            <span className="text-caption text-faint">
              {"DHCP"} · {r.name}
            </span>
          </li>
        );
      })}
      {/* NSX segments made for the deployment (ADR-0028). */}
      {segments.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="min-w-0 font-mono text-secondary break-all">{r.name}</span>
          <span className="text-caption text-faint">
            {t("network.segment", { tier1: r.segment.tier1, subnet: r.segment.subnet })}
          </span>
        </li>
      ))}
      {ips.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <span className="font-mono text-secondary">{r.address}</span>
          <span className="text-caption text-faint">
            {"EfficientIP"} · {r.subnet}
          </span>
        </li>
      ))}
      {dns.map((r) => (
        <li key={r.id} className="flex flex-wrap items-baseline justify-between gap-x-3">
          <Truncate mono className="text-secondary">
            {r.fqdn}
          </Truncate>
          <span className="text-caption text-faint">
            {r.record_type} → {r.value}
          </span>
        </li>
      ))}
    </ul>
  );
}

function Computers({ computers }: { computers: AdComputerResource[] }) {
  const { t } = useTranslation("deployments");
  return (
    <ul className="flex flex-col gap-2 text-secondary">
      {computers.map((c) => (
        <li key={c.id}>
          <div className="flex flex-wrap items-baseline justify-between gap-x-3">
            <span className="font-mono text-body">{c.computer.sam_account_name}</span>
            <span className="text-caption text-muted">{c.computer.dns_host_name}</span>
          </div>
          <p className="font-mono text-caption break-all text-faint">{c.computer.dn}</p>
          {c.computer.groups.length > 0 && (
            <p className="text-caption break-all text-muted">
              {t("ad.groups", { groups: c.computer.groups.join("; ") })}
            </p>
          )}
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
  const { t } = useTranslation("deployments");
  // NSX rule IDs are unique within their policy.
  const rules = policies.flatMap((p) => p.rules.map((r) => ({ ...r, key: `${p.id}/${r.id}` })));
  // The deployment's groups by their titles; ANY as people say it.
  const title = (id: string) => groups.find((g) => g.group.id === id)?.group.title ?? id;
  const where = (ids: string[]) => (ids.includes("ANY") ? t("security.anywhere") : ids.map(title).join(", "));
  const what = (services: string[]) => (services.includes("ANY") ? t("security.anyService") : services.join(", "));
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
                      {r.to.includes(g.group.id)
                        ? t(`security.${verb(r.action)}.from`, { services: what(r.services), where: where(r.from) })
                        : t(`security.${verb(r.action)}.to`, { services: what(r.services), where: where(r.to) })}
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
                {m.membership.groups.map((g) => g.name).join(", ") || t("security.noGroup")}
              </span>
            </div>
            {m.sync_state === "drifted" && m.membership.missing && (
              <p
                role="status"
                title={m.membership.missing.join(", ")}
                className="mt-1 rounded-md bg-warn-soft px-2 py-1 text-caption text-warn"
              >
                {t("security.tagsRemoved", {
                  groups: m.membership.out_of?.map((g) => title(g.id)).join(", ") || t("security.aGroup"),
                })}
              </p>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

const verb = (action: string) => (action === "ALLOW" ? "allow" : action === "DROP" ? "drop" : "reject");

/** A multi-VM deployment: its VMs belong to blueprint components (ADR-0024). */
const unbroken = (s: string) => s.replaceAll(" ", "\u00a0");

const isMulti = (vms: VmResource[]) => vms.some((v) => v.vm.component && v.vm.component !== "vm");

/** The values of the VMs, each once, in order. */
const distinct = (vms: VmResource[], of: (v: VmResource) => string | undefined) =>
  [...new Set(vms.map(of).filter((x): x is string => Boolean(x)))].join(", ");

function Placement({ vms, rules }: { vms: VmResource[]; rules: DrsRuleResource[] }) {
  const { t } = useTranslation("deployments");
  const vm = vms[0];
  if (!vm) return <p className="text-secondary text-muted">{t("placement.notYet")}</p>;
  // Several VMs may be in several places: each value once, the label plural then.
  const row = (key: "cluster" | "datastore" | "folder" | "network" | "template" | "vcenter", value: string) => [
    t(value.includes(", ") ? `placement.${key}.several` : `placement.${key}.single`),
    value,
  ];
  const rows = [
    row(
      "cluster",
      distinct(vms, (v) => v.vm.cluster),
    ),
    row(
      "datastore",
      distinct(vms, (v) => v.vm.disks[0]?.datastore),
    ),
    row(
      "folder",
      distinct(vms, (v) => v.vm.folder),
    ),
    row(
      "network",
      distinct(vms, (v) => v.vm.nics[0]?.network),
    ),
    row(
      "template",
      distinct(vms, (v) => v.vm.template),
    ),
    row(
      "vcenter",
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
        <ul className="flex flex-col gap-1.5 border-t border-line pt-3" aria-label={t("placement.separate")}>
          {rules.map((r) => (
            <li key={r.id} className="flex flex-col">
              <span className="text-body">
                <Trans
                  t={t}
                  i18nKey="placement.separateHosts"
                  values={{ component: r.rule.component, vms: r.rule.vms.join(", ") }}
                  components={{
                    name: <span className="font-medium" />,
                    vms: <span className="font-mono text-secondary" />,
                  }}
                />
              </span>
              <span className="text-caption text-faint">
                {t("placement.drsRule", { name: r.name, cluster: r.rule.cluster })}
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
    ? i18n.t("deployments:lease.thenDeleted")
    : d.lease.expiry_action === "notify_only"
      ? i18n.t("deployments:lease.thenTold")
      : i18n.t("deployments:lease.thenOff", { count: d.lease.grace_days ?? 7 });

/** How long the deployment may still live (ADR-0048), and what extending past that does. */
function lifetime(l: DeploymentDetail["lease"]): string | null {
  if (l.max_lifetime_days == null || !l.lifetime_ends_at) return null;
  const ends = Date.parse(l.lifetime_ends_at);
  const left = Math.max(Math.floor((ends - Date.now()) / 86_400_000), 0);
  const t = i18n.getFixedT(null, "deployments");
  const soft = l.enforcement === "soft";
  const vars = { count: l.max_lifetime_days, date: fullDate(l.lifetime_ends_at) };
  if (left === 0) {
    const ended = ends <= Date.now();
    if (soft) return t(ended ? "lease.lifetime.endedSoft" : "lease.lifetime.endsSoft", vars);
    return t(ended ? "lease.lifetime.endedHard" : "lease.lifetime.endsHard", vars);
  }
  const days = t("lease.days", { count: left });
  return t(soft ? "lease.lifetime.livesSoft" : "lease.lifetime.livesHard", { ...vars, left: days });
}

/** The extensions used, and how far the lease policy lets it go. */
function extensions(l: DeploymentDetail["lease"]): string {
  const t = i18n.getFixedT(null, "deployments");
  const used = t("lease.extensions", { count: l.extensions_used ?? 0, max: l.extensions_max ?? 0 });
  if (l.enforcement === "soft")
    return l.max_days
      ? t("lease.extensionsSoftUpTo", { used, count: l.max_days })
      : t("lease.extensionsSoft", { used });
  return l.max_days ? t("lease.extensionsUpTo", { used, count: l.max_days }) : t("lease.extensionsHard", { used });
}

function LeasePanel({ d, extend, onPick }: { d: DeploymentDetail; extend?: Day2Action; onPick: Pick }) {
  const { t } = useTranslation("deployments");
  const l = d.lease;
  if (!l.expires_at) {
    return <p className="text-secondary text-muted">{t("lease.noExpiry")}</p>;
  }
  return (
    <div className="flex flex-col gap-2.5 text-secondary">
      {l.expired_at ? (
        <p role="status" className="rounded-md bg-warn-soft px-3 py-2 text-warn">
          {l.deletes_at
            ? t("lease.endedDeletes", { date: fullDate(l.expires_at), deletes: fullDate(l.deletes_at) })
            : t("lease.ended", { date: fullDate(l.expires_at) })}
        </p>
      ) : (
        <p className="text-muted">
          {t("lease.expires", { when: leaseLabel(l.expires_at), date: fullDate(l.expires_at), then: afterExpiry(d) })}
        </p>
      )}
      <p className="text-faint">{extensions(l)}</p>
      {lifetime(l) && <p className="text-faint">{lifetime(l)}</p>}
      {l.policy && <p className="text-faint">{t("lease.setBy", { policy: l.policy })}</p>}
      {extend && (
        <Tip
          content={
            !extend.enabled
              ? extend.disabled_reason
              : extend.approval.required
                ? t("lease.tip.approval")
                : extend.warnings?.length
                  ? t("lease.tip.warns")
                  : t("lease.tip.extend")
          }
        >
          <span className="self-start">
            <Button size="sm" disabled={!extend.enabled} onClick={() => onPick(extend)}>
              {t("lease.extend")}
            </Button>
          </span>
        </Tip>
      )}
    </div>
  );
}

/** VMs changed in vCenter outside rvav, with what differs (S13.2). */
function Drift({ vms, actions, onPick }: { vms: VmResource[]; actions: Day2Action[]; onPick: Pick }) {
  const { t } = useTranslation("deployments");
  const drifted = vms.filter((v) => v.sync_state === "drifted" && v.drift?.length);
  if (drifted.length === 0) return null;
  const columns: Column<NonNullable<VmResource["drift"]>[number]>[] = [
    { id: "what", header: t("drift.what"), cell: (x) => x.label },
    { id: "asLeft", header: t("drift.asLeft"), className: "tnum text-muted", cell: (x) => x.rvav ?? t("drift.none") },
    {
      id: "inVcenter",
      header: t("drift.inVcenter"),
      className: "tnum font-medium",
      cell: (x) => x.vcenter ?? t("drift.removed"),
    },
  ];
  return (
    <Panel title={t("drift.title")} flush className="border-warn/40">
      <p className="px-4 pt-2.5 text-secondary text-muted">{t("drift.lead", { count: drifted.length })}</p>
      {drifted.map((v) => {
        const accept = actions.find((a) => a.key === "accept_changes" && a.resource_id === v.id);
        return (
          <div key={v.id} className="border-b border-line last:border-0">
            <div className="flex flex-wrap items-center justify-between gap-2 px-4 pt-2.5 pb-1">
              <span className="font-medium">{v.name}</span>
              {accept && (
                <Tip content={accept.enabled ? t("drift.take") : (accept.disabled_reason ?? t("drift.notNow"))}>
                  <span>
                    <Button size="sm" disabled={!accept.enabled} onClick={() => onPick(accept)}>
                      {t("drift.accept")}
                    </Button>
                  </span>
                </Tip>
              )}
            </div>
            <DataTable
              label={t("drift.how", { name: v.name })}
              columns={columns}
              rows={v.drift}
              rowKey={(x) => x.property + x.label}
            />
          </div>
        );
      })}
    </Panel>
  );
}

/** What the deployment is, each fact only when it has one. */
function Details({ d }: { d: DeploymentDetail }) {
  const { t } = useTranslation("deployments");
  const items: { label: string; value: React.ReactNode }[] = [
    {
      label: t("details.item"),
      value: (
        <Link to="/catalog/$itemId" params={{ itemId: d.catalog_item.slug }} className="text-accent hover:underline">
          {d.catalog_item.name}
        </Link>
      ),
    },
    { label: t("details.version"), value: <span className="font-mono text-secondary">{d.version.number}</span> },
    { label: t("details.project"), value: d.project.name },
    { label: t("details.created"), value: <Time iso={d.created_at} /> },
  ];
  if (d.description)
    items.unshift({
      label: t("details.description"),
      value: <span className="whitespace-pre-line">{d.description}</span>,
    });
  if (d.created_by_request_id)
    items.push({
      label: t("details.createdBy"),
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
  if (d.servicenow)
    items.push({
      label: t("details.servicenow"),
      value: (
        <a
          href={d.servicenow.url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1 font-mono text-accent hover:underline"
        >
          {d.servicenow.number}
          <ExternalLink className="size-3" aria-hidden />
          <span className="sr-only">{t("vm.newTab")}</span>
        </a>
      ),
    });
  if (d.vm_count > 0)
    items.push({
      label: t("details.vms"),
      value: t("details.vmsValue", { total: d.vm_count, count: d.vms_powered_on }),
    });
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
  const { t } = useTranslation("deployments");
  const vms = d.resources.filter((r): r is VmResource => r.type === "vsphere.vm");
  const rules = d.resources.filter((r): r is DrsRuleResource => r.type === "vsphere.drs_rule");
  const request = d.created_by_request_id;
  // The same query as the Topology tab's: the creating request's topology titles the components.
  const topology = useQuery({
    ...q.requestTopology(request ?? ""),
    enabled: Boolean(request) && isMulti(vms),
    retry: false,
  });
  const titles = componentTitles(topology.data?.topology);
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
  const computers = d.resources.filter((r): r is AdComputerResource => r.type === "ad.computer");
  return (
    <div className="flex flex-wrap items-start gap-4">
      <div className="flex min-w-0 flex-[999_1_600px] flex-col gap-4">
        <Drift vms={vms} actions={actions} onPick={onPick} />
        <Panel title={t("detail.vms")} description={t("detail.vmsLead")} flush>
          <VmTable deploymentId={d.id} vms={vms} titles={titles} actions={actions} onPick={onPick} onOpen={onOpen} />
        </Panel>
        <CustomResourcesPanel
          resources={d.resources.filter((r): r is CustomResource => r.type === "custom")}
          actions={actions}
          onPick={onPick}
        />
        <Panel title={t("detail.network")}>
          <Network ips={ips} dns={dns} segments={segments} dhcp={vms.filter((v) => v.vm.dhcp)} />
        </Panel>
        {groups.length + members.length > 0 && (
          <Panel title={t("detail.security")}>
            <SecurityGroups groups={groups} policies={policies} members={members} />
          </Panel>
        )}
        {computers.length > 0 && (
          <Panel title={t("ad.title")} description={t("ad.lead")}>
            <Computers computers={computers} />
          </Panel>
        )}
      </div>
      <aside className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
        <Panel title={t("detail.details")}>
          <Details d={d} />
        </Panel>
        <RemoteAccessPanel d={d} />
        <Panel title={t("detail.lease")}>
          <LeasePanel d={d} extend={actions.find((a) => a.key === "extend_lease")} onPick={onPick} />
        </Panel>
        <Panel title={t("detail.placement")}>
          <Placement vms={vms} rules={rules} />
        </Panel>
      </aside>
    </div>
  );
}

function History({ deploymentId, createdBy }: { deploymentId: string; createdBy?: string }) {
  const { t } = useTranslation("deployments");
  const history = useQuery(q.deploymentHistory(deploymentId));
  if (history.isError) return <ErrorState error={history.error} onRetry={() => void history.refetch()} />;
  if (!history.data) return <SkeletonRows rows={3} cols={3} />;
  if (history.data.items.length === 0)
    return (
      <p className="text-secondary text-muted">
        {createdBy ? (
          <Trans
            t={t}
            i18nKey="history.noneCreatedBy"
            values={{ number: createdBy }}
            components={{ request: <Link to="/requests/$requestId" params={{ requestId: createdBy }} /> }}
          />
        ) : (
          t("history.none")
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
              {r.kind === "day2"
                ? r.action
                  ? day2Name(r.action)
                  : r.title
                : r.kind === "update"
                  ? t("history.updated")
                  : t("history.created")}
            </Link>
            <span className="text-muted">
              {" "}
              ·{" "}
              {r.kind === "day2"
                ? (r.resource?.name ?? t("history.deployment"))
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
  const { t } = useTranslation("deployments");
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
            <Link to="/deployments">{t("list.title")}</Link>
          </Breadcrumbs>
        }
        title={d?.name ?? <Skeleton className="h-5 w-48" />}
        meta={
          d && (
            <>
              <DeploymentStatusBadge status={d.status} />
              <EnvBadge env={d.environment} />
              {d.sync_state !== "in_sync" && (
                <Badge tone="warn">{d.sync_state === "drifted" ? t("drift.title") : t("detail.missing")}</Badge>
              )}
              <span>{t("detail.owner", { name: d.owner.name })}</span>
            </>
          )
        }
        actions={
          d && (
            <DeploymentActionsMenu
              deploymentId={deploymentId}
              actions={actions}
              custom={d.resources.flatMap((r) => (r.type === "custom" ? [r.id] : []))}
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
        <TabsList aria-label={t("detail.sections")}>
          <TabsTrigger value="overview">{t("detail.tabs.overview")}</TabsTrigger>
          <TabsTrigger value="topology">{t("detail.tabs.topology")}</TabsTrigger>
          <TabsTrigger value="history">{t("detail.tabs.history")}</TabsTrigger>
          <TabsTrigger value="inputs">{t("detail.tabs.inputs")}</TabsTrigger>
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
              <Panel title={t("detail.history")}>
                <History deploymentId={deploymentId} createdBy={d?.created_by_request_id} />
              </Panel>
            </TabsContent>
            <TabsContent value="inputs" className="pt-4">
              <Panel
                title={t("detail.inputs")}
                description={
                  <Trans t={t} i18nKey="detail.inputsLead" components={{ code: <span className="font-mono" /> }} />
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
