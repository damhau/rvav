import { ExternalLink } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Time } from "@/components/common/misc";
import { KeyValues } from "@/components/common/page";
import { Sheet } from "@/components/ui/sheet";
import type { VmNic, VmResource } from "@/lib/api/types";
import { formatSize } from "@/lib/format";
import { i18n } from "@/lib/i18n";

/** Its "••••" values read a secret input (ADR-0038). */
export const MASKED = "••••";

const mono = (v: string) => <span className="font-mono text-secondary break-all">{v}</span>;

function Section({ id, title, children }: { id: string; title: string; children: React.ReactNode }) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-2">
      <h3 id={id} className="label">
        {title}
      </h3>
      {children}
    </section>
  );
}

/** How an adapter gets its address: rvav knows for the adapters it set up (ADR-0039). */
const assignment = (n: VmNic) =>
  n.dhcp === undefined ? i18n.t("deployments:notKnown") : n.dhcp ? "DHCP" : i18n.t("deployments:vm.fixed");

function Adapter({ nic, index }: { nic: VmNic; index: number }) {
  const { t } = useTranslation("deployments");
  const ips = nic.ip_addresses ?? [];
  const v4 = ips.filter((ip) => !ip.includes(":"));
  const v6 = ips.filter((ip) => ip.includes(":"));
  const items = [
    { label: t("vm.network"), value: mono(nic.network) },
    { label: t("vm.mac"), value: nic.mac ? mono(nic.mac) : t("notKnown") },
    { label: "IPv4", value: v4.length ? mono(v4.join(", ")) : t("noneReported") },
    { label: "IPv6", value: v6.length ? mono(v6.join(", ")) : t("noneReported") },
    { label: t("vm.addressFrom"), value: assignment(nic) },
  ];
  return (
    <li className="flex flex-col gap-1">
      <span className="font-medium">{nic.label ?? t("vm.adapterN", { n: index + 1 })}</span>
      <KeyValues items={items} />
    </li>
  );
}

/** A VM's properties, read only: what vCenter reports, where it runs, and what rvav built it with. */
export function VmPropertiesSheet({ resource, onClose }: { resource: VmResource; onClose: () => void }) {
  const { t } = useTranslation("deployments");
  const { name, vm } = resource;
  const id = (s: string) => `vm-${resource.id}-${s}`;
  const datastores = [...new Set(vm.disks.map((d) => d.datastore).filter((d): d is string => Boolean(d)))];
  const placement = [
    { label: "vCenter", value: mono(vm.vcenter) },
    { label: t("vm.cluster"), value: vm.cluster ? mono(vm.cluster) : t("notKnown") },
    { label: t("vm.host"), value: vm.host ? mono(vm.host) : t("notKnown") },
    { label: t("vm.pool"), value: vm.resource_pool ? mono(vm.resource_pool) : t("vm.rootPool") },
    { label: t("vm.folder"), value: vm.folder ? mono(vm.folder) : t("notKnown") },
    {
      label: datastores.length > 1 ? t("vm.datastores") : t("vm.datastore"),
      value: datastores.length ? mono(datastores.join(", ")) : t("notKnown"),
    },
  ];
  const summary = [
    { label: t("vm.guestOs"), value: vm.guest_os ?? t("vm.notReported") },
    { label: t("vm.power"), value: t(`vmPower.${vm.power_state}`) },
    { label: "VMware Tools", value: t(`vm.tools.${vm.guest_state}`) },
    {
      label: t("vm.size"),
      value: t("vm.sizeValue", { cpu: vm.cpu_count, memory: formatSize(Math.round(vm.memory_mb / 1024), "GB") }),
    },
    ...(vm.fqdn ? [{ label: "FQDN", value: mono(vm.fqdn) }] : []),
    ...(vm.template ? [{ label: t("vm.template"), value: mono(vm.template) }] : []),
    ...(vm.component && vm.component !== "vm" ? [{ label: t("vm.component"), value: mono(vm.component) }] : []),
    { label: "ISO", value: vm.iso ? mono(vm.iso) : t("vm.noIso") },
    { label: t("vm.moref"), value: mono(vm.moref) },
  ];
  const props = Object.entries(vm.custom_properties ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const snapshots = vm.snapshots ?? [];
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={name}
      description={t("vm.description", { vcenter: vm.vcenter })}
    >
      <KeyValues items={summary} />
      {vm.vsphere_url ? (
        <a
          href={vm.vsphere_url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1.5 self-start text-accent underline underline-offset-2"
        >
          {t("vm.openVsphere", { name })}
          <ExternalLink className="size-3.5" aria-hidden />
          <span className="sr-only">{t("vm.newTab")}</span>
        </a>
      ) : (
        <p className="text-secondary text-muted">{t("vm.noLink", { vcenter: vm.vcenter })}</p>
      )}
      <Section id={id("nics")} title={t("vm.adapters")}>
        {vm.nics.length === 0 ? (
          <p className="text-secondary text-muted">{t("vm.noAdapter")}</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {vm.nics.map((n, i) => (
              <Adapter key={n.label ?? i} nic={n} index={i} />
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("disks")} title={t("vm.disks")}>
        {vm.disks.length === 0 ? (
          <p className="text-secondary text-muted">{t("vm.noDisk")}</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {vm.disks.map((d) => (
              <li key={d.label} className="flex flex-wrap items-baseline justify-between gap-x-3">
                <span>
                  <span className="font-medium">{d.label}</span>{" "}
                  <span className="tnum text-muted">{formatSize(d.capacity_gb, "GB")}</span>
                </span>
                <span className="text-caption text-faint">
                  {[d.datastore, d.provisioning && t(`vm.provisioning.${d.provisioning}`)]
                    .filter(Boolean)
                    .join(" · ") || t("vm.datastoreNotKnown")}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("snapshots")} title={t("vm.snapshots")}>
        {snapshots.length === 0 ? (
          <p className="text-secondary text-muted">{t("vm.noSnapshots")}</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {snapshots.map((s) => (
              <li key={s.name + s.created_at} className="flex flex-col">
                <span className="flex flex-wrap items-baseline justify-between gap-x-3">
                  <span className="font-medium">{s.name}</span>
                  <Time iso={s.created_at} className="text-caption text-faint" />
                </span>
                {s.description && <span className="text-caption text-muted">{s.description}</span>}
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("placement")} title={t("vm.placement")}>
        <KeyValues items={placement} />
      </Section>
      <Section id={id("tags")} title={t("vm.tags")}>
        {vm.tags === undefined ? (
          <p className="text-secondary text-muted">{t("vm.tagsNotRead")}</p>
        ) : vm.tags.length === 0 ? (
          <p className="text-secondary text-muted">{t("vm.noTags")}</p>
        ) : (
          <ul className="flex flex-wrap gap-x-3 gap-y-1">
            {vm.tags.map((tag) => (
              <li key={tag}>{mono(tag)}</li>
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("props")} title={t("vm.custom")}>
        {props.length === 0 ? (
          <p className="text-secondary text-muted">{t("vm.noCustom")}</p>
        ) : (
          <>
            <KeyValues items={props.map(([k, v]) => ({ label: k, value: mono(v) }))} />
            {props.some(([, v]) => v === MASKED) && (
              <p className="text-caption text-muted">{t("vm.maskedNote", { masked: MASKED })}</p>
            )}
          </>
        )}
      </Section>
    </Sheet>
  );
}
