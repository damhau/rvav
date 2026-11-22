// What a multi-VM blueprint builds (ADR-0024): each component's VMs, their
// size, placement and network, as the preview says, what its constraint
// tags chose or ruled out (ADR-0035), and the vCenters its catalogue
// template isn't in (ADR-0037).
import { useTranslation } from "react-i18next";
import { Badge } from "@/components/ui/badge";
import type { ComponentPlacement, PlacementConstraint, PlacementImage, PreviewComponent } from "@/lib/api/types";
import { formatSize } from "@/lib/format";
import { i18n } from "@/lib/i18n";

import { constraintText } from "./constraints";

const osLabel = { linux: "Linux", windows: "Windows" } as const;

/** Where the VMs go: the cluster and its resource pool, the datastore and its datastore cluster (ADR-0036). */
export function placedAt(p?: ComponentPlacement): string {
  if (!p) return "";
  return [
    p.vcenter,
    p.cluster && (p.resource_pool ? `${p.cluster}/${p.resource_pool}` : p.cluster),
    p.hosts?.length && i18n.t("requests:preview.hosts", { hosts: p.hosts.join(", ") }),
    p.datastore &&
      (p.datastore_cluster
        ? i18n.t("requests:preview.datastoreIn", { datastore: p.datastore, cluster: p.datastore_cluster })
        : p.datastore),
  ]
    .filter(Boolean)
    .join(" · ");
}

/** Who chose the cluster, when rvav did: a strategy other than the default says its name. */
export function chosenBy(p?: ComponentPlacement): string {
  if (!p?.chosen) return "";
  if (p.strategy === "binpack") return i18n.t("requests:preview.chosenBinpack");
  if (p.strategy === "spread") return i18n.t("requests:preview.chosenSpread");
  return i18n.t("requests:preview.chosen");
}

/** The vCenters ruled out because the catalogue template has no template there. */
export function imageText(image?: PlacementImage): string {
  if (!image?.excluded.length) return "";
  const where = i18n.t("requests:preview.vcenters", { count: image.excluded.length, list: image.excluded.join(", ") });
  return i18n.t("requests:preview.noTemplate", { label: image.label, where });
}

/** What constraint tags chose or ruled out, one line each, after the vCenters the template isn't in. */
export function PlacedBy({ constraints, image }: { constraints?: PlacementConstraint[]; image?: PlacementImage }) {
  const { t } = useTranslation("requests");
  const ruledOut = imageText(image);
  if (!constraints?.length && !ruledOut) return null;
  return (
    <ul className="flex flex-col text-secondary text-muted" aria-label={t("preview.placedBy")}>
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
  const { t } = useTranslation("requests");
  const total = components.reduce((n, c) => n + c.count, 0);
  return (
    <div
      className="flex flex-col gap-2.5 border-t border-line px-4 py-3"
      aria-label={t("preview.whatItBuilds")}
      role="group"
    >
      <span className="label">{t("preview.builds", { count: total })}</span>
      <ul className="flex flex-col gap-3">
        {components.map((c) => {
          const disks = c.disks.map((d) => formatSize(d.size_gb, "GB")).join(" + ");
          const where = placedAt(c.placement);
          const by = chosenBy(c.placement);
          return (
            <li key={c.key} className="flex flex-col gap-0.5" aria-label={c.title}>
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="font-medium">{c.title}</span>
                <span className="text-secondary text-muted">
                  {c.count === 0 ? t("preview.none") : t("preview.vms", { count: c.count })}
                  {c.os_family ? ` · ${osLabel[c.os_family]}` : ""}
                </span>
                {c.anti_affinity && <Badge tone="accent">{t("preview.separateHosts")}</Badge>}
              </span>
              {c.count > 0 && (
                <>
                  {c.names.length > 0 && (
                    <span className="font-mono text-secondary break-words">{c.names.join(", ")}</span>
                  )}
                  <span className="text-secondary text-muted">
                    {t("preview.size", { vcpu: c.vcpu, memory: formatSize(c.memory_gb, "GB"), disks })}
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
                    <span className="text-caption text-faint">{t("preview.tags", { tags: c.tags.join(", ") })}</span>
                  )}
                  {/* NSX security groups (ADR-0028). */}
                  {c.nsx_groups && c.nsx_groups.length > 0 && (
                    <span className="text-caption text-faint">
                      {t("preview.securityGroups", { groups: c.nsx_groups.join(", ") })}
                    </span>
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
