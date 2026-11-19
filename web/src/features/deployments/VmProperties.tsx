import { ExternalLink } from "lucide-react";
import { Time } from "@/components/common/misc";
import { KeyValues } from "@/components/common/page";
import { Sheet } from "@/components/ui/sheet";
import type { VmNic, VmResource } from "@/lib/api/types";

const power = { on: "On", off: "Off", suspended: "Suspended" } as const;
const tools = { running: "Running", not_running: "Not running", unknown: "Not known" } as const;
const provisioning = {
  thin: "Thin",
  thick_lazy_zeroed: "Thick, lazily zeroed",
  thick_eager_zeroed: "Thick, eagerly zeroed",
} as const;

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
const assignment = (n: VmNic) => (n.dhcp === undefined ? "Not known" : n.dhcp ? "DHCP" : "Fixed");

function Adapter({ nic, index }: { nic: VmNic; index: number }) {
  const ips = nic.ip_addresses ?? [];
  const v4 = ips.filter((ip) => !ip.includes(":"));
  const v6 = ips.filter((ip) => ip.includes(":"));
  const items = [
    { label: "Network", value: mono(nic.network) },
    { label: "MAC address", value: nic.mac ? mono(nic.mac) : "Not known" },
    { label: "IPv4", value: v4.length ? mono(v4.join(", ")) : "None reported" },
    { label: "IPv6", value: v6.length ? mono(v6.join(", ")) : "None reported" },
    { label: "Address from", value: assignment(nic) },
  ];
  return (
    <li className="flex flex-col gap-1">
      <span className="font-medium">{nic.label ?? `Network adapter ${index + 1}`}</span>
      <KeyValues items={items} />
    </li>
  );
}

/** A VM's properties, read only: what vCenter reports, where it runs, and what rvav built it with. */
export function VmPropertiesSheet({ resource, onClose }: { resource: VmResource; onClose: () => void }) {
  const { name, vm } = resource;
  const id = (s: string) => `vm-${resource.id}-${s}`;
  const datastores = [...new Set(vm.disks.map((d) => d.datastore).filter((d): d is string => Boolean(d)))];
  const placement = [
    { label: "vCenter", value: mono(vm.vcenter) },
    { label: "Cluster", value: vm.cluster ? mono(vm.cluster) : "Not known" },
    { label: "Host", value: vm.host ? mono(vm.host) : "Not known" },
    { label: "Resource pool", value: vm.resource_pool ? mono(vm.resource_pool) : "The cluster's root pool" },
    { label: "Folder", value: vm.folder ? mono(vm.folder) : "Not known" },
    {
      label: datastores.length > 1 ? "Datastores" : "Datastore",
      value: datastores.length ? mono(datastores.join(", ")) : "Not known",
    },
  ];
  const summary = [
    { label: "Guest OS", value: vm.guest_os ?? "Not reported" },
    { label: "Power", value: power[vm.power_state] },
    { label: "VMware Tools", value: tools[vm.guest_state] },
    { label: "Size", value: `${vm.cpu_count} vCPU · ${Math.round(vm.memory_mb / 1024)} GB memory` },
    ...(vm.fqdn ? [{ label: "FQDN", value: mono(vm.fqdn) }] : []),
    ...(vm.template ? [{ label: "Template", value: mono(vm.template) }] : []),
    ...(vm.component && vm.component !== "vm" ? [{ label: "Component", value: mono(vm.component) }] : []),
    { label: "ISO", value: vm.iso ? mono(vm.iso) : "None mounted" },
    { label: "Managed object", value: mono(vm.moref) },
  ];
  const props = Object.entries(vm.custom_properties ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const snapshots = vm.snapshots ?? [];
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      title={name}
      description={`Properties of the VM in ${vm.vcenter}`}
    >
      <KeyValues items={summary} />
      {vm.vsphere_url ? (
        <a
          href={vm.vsphere_url}
          target="_blank"
          rel="noreferrer"
          className="inline-flex items-center gap-1.5 self-start text-accent underline underline-offset-2"
        >
          Open {name} in the vSphere Client
          <ExternalLink className="size-3.5" aria-hidden />
          <span className="sr-only">(opens in a new tab)</span>
        </a>
      ) : (
        <p className="text-secondary text-muted">
          No vSphere Client link yet: it needs the instance UUID of {vm.vcenter}, which rvav reads at its next inventory
          sync.
        </p>
      )}
      <Section id={id("nics")} title="Network adapters">
        {vm.nics.length === 0 ? (
          <p className="text-secondary text-muted">No network adapter.</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {vm.nics.map((n, i) => (
              <Adapter key={n.label ?? i} nic={n} index={i} />
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("disks")} title="Disks">
        {vm.disks.length === 0 ? (
          <p className="text-secondary text-muted">No disk.</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {vm.disks.map((d) => (
              <li key={d.label} className="flex flex-wrap items-baseline justify-between gap-x-3">
                <span>
                  <span className="font-medium">{d.label}</span>{" "}
                  <span className="tnum text-muted">{d.capacity_gb} GB</span>
                </span>
                <span className="text-caption text-faint">
                  {[d.datastore, d.provisioning && provisioning[d.provisioning]].filter(Boolean).join(" · ") ||
                    "Datastore not known"}
                </span>
              </li>
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("snapshots")} title="Snapshots">
        {snapshots.length === 0 ? (
          <p className="text-secondary text-muted">No snapshots.</p>
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
      <Section id={id("placement")} title="Placement">
        <KeyValues items={placement} />
      </Section>
      <Section id={id("tags")} title="vSphere tags">
        {vm.tags === undefined ? (
          <p className="text-secondary text-muted">Not read yet: rvav reads them at its next inventory sync.</p>
        ) : vm.tags.length === 0 ? (
          <p className="text-secondary text-muted">No tags.</p>
        ) : (
          <ul className="flex flex-wrap gap-x-3 gap-y-1">
            {vm.tags.map((t) => (
              <li key={t}>{mono(t)}</li>
            ))}
          </ul>
        )}
      </Section>
      <Section id={id("props")} title="Custom properties">
        {props.length === 0 ? (
          <p className="text-secondary text-muted">
            None: it was built without custom properties, or before rvav kept them.
          </p>
        ) : (
          <>
            <KeyValues items={props.map(([k, v]) => ({ label: k, value: mono(v) }))} />
            {props.some(([, v]) => v === MASKED) && (
              <p className="text-caption text-muted">
                {MASKED} marks a property that reads a secret input: its value isn't kept.
              </p>
            )}
          </>
        )}
      </Section>
    </Sheet>
  );
}
