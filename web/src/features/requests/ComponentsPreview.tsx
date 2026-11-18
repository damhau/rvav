// What a multi-VM blueprint builds (ADR-0024): each component's VMs, their
// size, placement and network, as the preview says, what its constraint
// tags chose or ruled out (ADR-0035), and the vCenters its catalogue
// template isn't in (ADR-0037).
import { Badge } from "@/components/ui/badge";
import type { ComponentPlacement, PlacementConstraint, PlacementImage, PreviewComponent } from "@/lib/api/types";

import { constraintText } from "./constraints";

const osLabel = { linux: "Linux", windows: "Windows" } as const;

/** Where the VMs go: the cluster and its resource pool, the datastore and its datastore cluster (ADR-0036). */
export function placedAt(p?: ComponentPlacement): string {
  if (!p) return "";
  return [
    p.vcenter,
    p.cluster && (p.resource_pool ? `${p.cluster}/${p.resource_pool}` : p.cluster),
    p.hosts?.length && `hosts ${p.hosts.join(", ")}`,
    p.datastore && (p.datastore_cluster ? `${p.datastore} in ${p.datastore_cluster}` : p.datastore),
  ]
    .filter(Boolean)
    .join(" · ");
}

/** Who chose the cluster, when rvav did: a strategy other than the default says its name. */
export function chosenBy(p?: ComponentPlacement): string {
  if (!p?.chosen) return "";
  if (p.strategy === "binpack") return "chosen by rvav, binpack";
  if (p.strategy === "spread") return "chosen by rvav, spread";
  return "chosen by rvav";
}

/** The vCenters ruled out because the catalogue template has no template there. */
export function imageText(image?: PlacementImage): string {
  if (!image?.excluded.length) return "";
  const where = image.excluded.length === 1 ? `vCenter ${image.excluded[0]}` : `vCenters ${image.excluded.join(", ")}`;
  return `${image.label}: no template in ${where}, ruled out`;
}

/** What constraint tags chose or ruled out, one line each, after the vCenters the template isn't in. */
export function PlacedBy({ constraints, image }: { constraints?: PlacementConstraint[]; image?: PlacementImage }) {
  const ruledOut = imageText(image);
  if (!constraints?.length && !ruledOut) return null;
  return (
    <ul className="flex flex-col text-secondary text-muted" aria-label="Placed by tags">
      {ruledOut && <li className="break-words">{ruledOut}</li>}
      {constraints?.map((k, i) => (
        <li key={i} className="break-words">
          {constraintText(k)}
        </li>
      ))}
    </ul>
  );
}

export function ComponentsPreview({ components }: { components: PreviewComponent[] }) {
  const total = components.reduce((n, c) => n + c.count, 0);
  return (
    <div className="flex flex-col gap-2.5 border-t border-line px-4 py-3" aria-label="What it builds" role="group">
      <span className="label">
        What it builds · {total} VM{total === 1 ? "" : "s"}
      </span>
      <ul className="flex flex-col gap-3">
        {components.map((c) => {
          const disks = c.disks.map((d) => `${d.size_gb} GB`).join(" + ");
          const where = placedAt(c.placement);
          const by = chosenBy(c.placement);
          return (
            <li key={c.key} className="flex flex-col gap-0.5" aria-label={c.title}>
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="font-medium">{c.title}</span>
                <span className="text-secondary text-muted">
                  {c.count === 0 ? "none" : `${c.count} VM${c.count === 1 ? "" : "s"}`}
                  {c.os_family ? ` · ${osLabel[c.os_family]}` : ""}
                </span>
                {c.anti_affinity && <Badge tone="accent">On separate hosts</Badge>}
              </span>
              {c.count > 0 && (
                <>
                  {c.names.length > 0 && (
                    <span className="font-mono text-secondary break-words">{c.names.join(", ")}</span>
                  )}
                  <span className="text-secondary text-muted">
                    {c.vcpu} vCPU · {c.memory_gb} GB · disks {disks}
                    {where ? ` · ${where}` : ""}
                    {by ? ` (${by})` : ""}
                  </span>
                  <span className="font-mono text-caption break-words text-faint">
                    {c.nics
                      .map((n) =>
                        [n.network, n.subnet, n.ipam === "dhcp" ? "DHCP" : n.address].filter(Boolean).join(" "),
                      )
                      .join(" · ")}
                  </span>
                  <PlacedBy constraints={c.placement?.constraints} image={c.placement?.image} />
                  {c.tags && c.tags.length > 0 && (
                    <span className="text-caption text-faint">Tags {c.tags.join(", ")}</span>
                  )}
                  {/* NSX security groups (ADR-0028). */}
                  {c.nsx_groups && c.nsx_groups.length > 0 && (
                    <span className="text-caption text-faint">Security groups {c.nsx_groups.join(", ")}</span>
                  )}
                </>
              )}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
