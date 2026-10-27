// Bulk day-2 actions on the deployments list: one request per VM (or per
// deployment for deployment actions), skipping those it doesn't apply to.
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { Day2Action, Deployment } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { newIdempotencyKey } from "@/lib/utils";

interface Bulk {
  key: string;
  label: string;
  /** Acts on the whole deployment rather than on each VM. */
  deployment?: boolean;
  destructive?: boolean;
  message: string;
}

export const bulkActions: Bulk[] = [
  { key: "power_on", label: "Power on", message: "Powers on the VMs that are off." },
  {
    key: "shutdown_guest",
    label: "Shut down guest",
    message: "Asks each guest OS to shut down, through VMware Tools.",
  },
  {
    key: "power_off",
    label: "Power off",
    destructive: true,
    message: "Powers the VMs off without asking the guest OS: unsaved work is lost.",
  },
  { key: "snapshot_create", label: "Take a snapshot", message: "Takes a snapshot of each VM, without its memory." },
  {
    key: "delete",
    label: "Delete",
    deployment: true,
    destructive: true,
    message: "Deletes the deployments, their VMs, IP addresses and DNS records. Production ones wait for approval.",
  },
];

interface Outcome {
  name: string;
  result: string;
  ok: boolean;
}

export function BulkDialog({
  bulk,
  deployments,
  onClose,
}: {
  bulk: Bulk;
  deployments: Deployment[];
  onClose: (done: boolean) => void;
}) {
  const qc = useQueryClient();
  const [snapshot, setSnapshot] = useState(() => `Bulk ${new Date().toISOString().slice(0, 16).replace("T", " ")}`);
  const [typed, setTyped] = useState("");
  const [running, setRunning] = useState(false);
  const [outcomes, setOutcomes] = useState<Outcome[] | null>(null);
  const confirmText = `delete ${deployments.length}`;
  const ready = bulk.key !== "delete" || typed === confirmText;

  const runAll = async () => {
    setRunning(true);
    const out: Outcome[] = [];
    for (const d of deployments) {
      let actions: Day2Action[];
      try {
        actions = (await qc.fetchQuery({ ...q.deploymentActions(d.id), staleTime: 0 })).items;
      } catch (e) {
        out.push({ name: d.name, result: isApiError(e) ? e.problem.title : String(e), ok: false });
        continue;
      }
      const targets = actions.filter((a) => a.key === bulk.key && Boolean(a.resource_id) !== Boolean(bulk.deployment));
      if (targets.length === 0) out.push({ name: d.name, result: "Does not apply", ok: false });
      for (const a of targets) {
        const name = a.resource_id ? (a.resource_name ?? d.name) : d.name;
        if (!a.enabled) {
          out.push({ name, result: a.disabled_reason ?? "Not available", ok: false });
          continue;
        }
        try {
          const r = await call(
            api.POST("/deployments/{deploymentId}/actions", {
              params: { path: { deploymentId: d.id }, header: { "Idempotency-Key": newIdempotencyKey() } },
              body: {
                action: a.key,
                ...(a.resource_id ? { resource_id: a.resource_id } : {}),
                inputs: bulk.key === "snapshot_create" ? { name: snapshot } : {},
              },
            }),
          );
          out.push({
            name,
            result: r.status === "pending_approval" ? `${r.number} waits for approval` : `${r.number} started`,
            ok: true,
          });
        } catch (e) {
          out.push({ name, result: isApiError(e) ? e.problem.title : String(e), ok: false });
        }
      }
      void qc.invalidateQueries({ queryKey: keys.deployments.actions(d.id) });
    }
    void qc.invalidateQueries({ queryKey: keys.requests.lists() });
    setOutcomes(out);
    setRunning(false);
    const started = out.filter((o) => o.ok).length;
    toast.success(`${bulk.label}: ${started} started`, {
      description: started < out.length ? `${out.length - started} skipped.` : undefined,
    });
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose(outcomes !== null)}>
      <DialogContent
        title={`${bulk.label} · ${deployments.length} deployment${deployments.length === 1 ? "" : "s"}`}
        description={bulk.message}
        className="w-[min(560px,calc(100vw-32px))]"
      >
        <div className="flex flex-col gap-3 px-4 py-3">
          {outcomes ? (
            <ul className="flex max-h-72 flex-col overflow-auto text-xs" aria-label="Results">
              {outcomes.map((o, i) => (
                <li key={i} className="flex justify-between gap-3 border-b border-line py-1.5 last:border-0">
                  <span className="font-medium">{o.name}</span>
                  <span className={o.ok ? "text-muted" : "text-faint"}>{o.result}</span>
                </li>
              ))}
            </ul>
          ) : (
            <>
              <p className="text-xs text-muted">{deployments.map((d) => d.name).join(", ")}</p>
              {bulk.key === "snapshot_create" && (
                <div className="flex flex-col gap-1.5 text-xs">
                  <label htmlFor="bulk-snapshot" className="font-medium">
                    Snapshot name
                  </label>
                  <Input
                    id="bulk-snapshot"
                    value={snapshot}
                    onChange={(e) => setSnapshot(e.target.value)}
                    maxLength={80}
                  />
                </div>
              )}
              {bulk.key === "delete" && (
                <div className="flex flex-col gap-1.5 text-xs">
                  <label htmlFor="bulk-confirm">
                    Type <span className="font-mono font-medium">{confirmText}</span> to confirm
                  </label>
                  <Input
                    id="bulk-confirm"
                    value={typed}
                    onChange={(e) => setTyped(e.target.value)}
                    autoComplete="off"
                  />
                </div>
              )}
            </>
          )}
          <div className="flex justify-end gap-2 pt-1">
            {outcomes ? (
              <Button variant="primary" onClick={() => onClose(true)}>
                Close
              </Button>
            ) : (
              <>
                <Button onClick={() => onClose(false)}>Cancel</Button>
                <Button
                  variant={bulk.destructive ? "danger-solid" : "primary"}
                  disabled={!ready || running || (bulk.key === "snapshot_create" && !snapshot.trim())}
                  onClick={() => void runAll()}
                >
                  {running ? "Starting…" : bulk.label}
                </Button>
              </>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
