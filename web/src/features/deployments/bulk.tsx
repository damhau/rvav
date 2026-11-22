// Bulk day-2 actions on the deployments list: one request per VM (or per
// deployment for deployment actions), skipping those it doesn't apply to.
import { useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { api, call, isApiError } from "@/lib/api/client";
import type { Day2Action, Deployment } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { newIdempotencyKey } from "@/lib/utils";

interface Bulk {
  /** Its label and message are `bulk.<key>` in the deployments namespace. */
  key: "power_on" | "shutdown_guest" | "power_off" | "snapshot_create" | "delete";
  /** Acts on the whole deployment rather than on each VM. */
  deployment?: boolean;
  destructive?: boolean;
}

export const bulkActions: Bulk[] = [
  { key: "power_on" },
  { key: "shutdown_guest" },
  { key: "power_off", destructive: true },
  { key: "snapshot_create" },
  { key: "delete", deployment: true, destructive: true },
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
  const { t } = useTranslation(["deployments", "common"]);
  const qc = useQueryClient();
  const [snapshot, setSnapshot] = useState(() =>
    t("bulk.snapshotName", { date: new Date().toISOString().slice(0, 16).replace("T", " ") }),
  );
  const [typed, setTyped] = useState("");
  const [running, setRunning] = useState(false);
  const [outcomes, setOutcomes] = useState<Outcome[] | null>(null);
  const confirmText = t("bulk.confirmText", { n: deployments.length });
  const label = t(`bulk.${bulk.key}.label`);
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
      if (targets.length === 0) out.push({ name: d.name, result: t("bulk.doesNotApply"), ok: false });
      for (const a of targets) {
        const name = a.resource_id ? (a.resource_name ?? d.name) : d.name;
        if (!a.enabled) {
          out.push({ name, result: a.disabled_reason ?? t("bulk.notAvailable"), ok: false });
          continue;
        }
        // Going on despite a soft policy is each requester's choice, one deployment at a time.
        if (a.warnings?.length) {
          out.push({ name, result: t("bulk.warned", { warning: a.warnings[0]!.message }), ok: false });
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
            result: t(r.status === "pending_approval" ? "bulk.waits" : "bulk.started", { number: r.number }),
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
    toast.success(t("bulk.toast", { action: label, count: started }), {
      description: started < out.length ? t("bulk.skipped", { count: out.length - started }) : undefined,
    });
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose(outcomes !== null)}>
      <DialogContent
        title={t("bulk.title", { action: label, count: deployments.length })}
        description={t(`bulk.${bulk.key}.message`)}
        className="w-[min(560px,calc(100vw-32px))]"
      >
        <div className="flex flex-col gap-3 px-4 py-3">
          {outcomes ? (
            <ul className="flex max-h-72 flex-col overflow-auto text-secondary" aria-label={t("bulk.results")}>
              {outcomes.map((o, i) => (
                <li key={i} className="flex justify-between gap-3 border-b border-line py-1.5 last:border-0">
                  <span className="font-medium">{o.name}</span>
                  <span className={o.ok ? "text-muted" : "text-faint"}>{o.result}</span>
                </li>
              ))}
            </ul>
          ) : (
            <>
              <p className="text-secondary text-muted">{deployments.map((d) => d.name).join(", ")}</p>
              {bulk.key === "snapshot_create" && (
                <div className="flex flex-col gap-1.5 text-secondary">
                  <label htmlFor="bulk-snapshot" className="font-medium">
                    {t("bulk.snapshotLabel")}
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
                <div className="flex flex-col gap-1.5 text-secondary">
                  <label htmlFor="bulk-confirm">
                    <Trans
                      t={t}
                      i18nKey="typeToConfirm"
                      values={{ text: confirmText }}
                      components={{ text: <span className="font-mono font-medium" /> }}
                    />
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
                {t("common:actions.close")}
              </Button>
            ) : (
              <>
                <Button onClick={() => onClose(false)}>{t("common:actions.cancel")}</Button>
                <Button
                  variant={bulk.destructive ? "danger-solid" : "primary"}
                  disabled={!ready || running || (bulk.key === "snapshot_create" && !snapshot.trim())}
                  onClick={() => void runAll()}
                >
                  {running ? t("bulk.starting") : label}
                </Button>
              </>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
