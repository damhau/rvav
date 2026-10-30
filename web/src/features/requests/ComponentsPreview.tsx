// What a multi-VM blueprint builds (ADR-0024): each component's VMs, their
// size, placement and network, as the preview says.
import { Badge } from "@/components/ui/badge";
import type { PreviewComponent } from "@/lib/api/types";

const osLabel = { linux: "Linux", windows: "Windows" } as const;

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
          const where = [c.placement?.vcenter, c.placement?.cluster, c.placement?.datastore]
            .filter(Boolean)
            .join(" · ");
          return (
            <li key={c.key} className="flex flex-col gap-0.5" aria-label={c.title}>
              <span className="flex flex-wrap items-center gap-1.5">
                <span className="font-medium">{c.title}</span>
                <span className="text-xs text-muted">
                  {c.count === 0 ? "none" : `${c.count} VM${c.count === 1 ? "" : "s"}`}
                  {c.os_family ? ` · ${osLabel[c.os_family]}` : ""}
                </span>
                {c.anti_affinity && <Badge tone="accent">On separate hosts</Badge>}
              </span>
              {c.count > 0 && (
                <>
                  {c.names.length > 0 && (
                    <span className="font-mono text-[12px] break-words">{c.names.join(", ")}</span>
                  )}
                  <span className="text-xs text-muted">
                    {c.vcpu} vCPU · {c.memory_gb} GB · disks {disks}
                    {where ? ` · ${where}` : ""}
                    {c.placement?.chosen ? " (chosen by rvav)" : ""}
                  </span>
                  <span className="font-mono text-[11px] break-words text-faint">
                    {c.nics
                      .map((n) =>
                        [n.network, n.subnet, n.ipam === "dhcp" ? "DHCP" : n.address].filter(Boolean).join(" "),
                      )
                      .join(" · ")}
                  </span>
                  {c.tags && c.tags.length > 0 && (
                    <span className="text-[11px] text-faint">Tags {c.tags.join(", ")}</span>
                  )}
                  {/* NSX security groups (ADR-0028). */}
                  {c.nsx_groups && c.nsx_groups.length > 0 && (
                    <span className="text-[11px] text-faint">Security groups {c.nsx_groups.join(", ")}</span>
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
