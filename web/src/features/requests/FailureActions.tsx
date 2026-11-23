import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { catchFocus } from "@/components/ui/opener";
import { api, call, isApiError } from "@/lib/api/client";
import type { Me, ServiceRequest, Step } from "@/lib/api/types";
import { can } from "@/lib/can";
import { keys } from "@/lib/query";
import { failedRollbackOf, rollbackOf, vmsMade } from "./run";

/**
 * Retry from the failed step, or roll back what a failed provisioning request
 * built. Once rolled back (its `rollback` step done: by hand, or by a
 * composite's rollback_on_failure), there is nothing left to roll back, and
 * a retry runs every step after the approval again, as the server does.
 *
 * The box is an alert, read out when it appears, unless `announce` is false
 * when it mounts: a page that moves it (a run's, with the selection) has it
 * read once. Without `mayAct` (an auditor's), it says only what failed.
 *
 * A retry takes the box away with the failure: the focus goes to `standIn`,
 * what stays (the request's status), and the toast says what runs. A
 * rollback leaves it failed: the focus comes back to the box, which says
 * once it is done that it was rolled back, its Roll back gone.
 *
 * What a rollback deletes is named for a VM item: its VMs and what its steps
 * made for them. A composite makes more than its VMs (custom resources, NSX
 * objects), and a request that made no VM can't say: what it created.
 */
export function FailureActions({
  request,
  steps,
  standIn,
  announce = true,
  mayAct = true,
}: {
  request: ServiceRequest;
  steps: Step[];
  standIn?: () => HTMLElement | null | undefined;
  announce?: boolean;
  mayAct?: boolean;
}) {
  const { t } = useTranslation("requests");
  const undone = request.kind === "provision" && rollbackOf(steps);
  const rollbackFailed = failedRollbackOf(steps);
  const vms = vmsMade(steps);
  const made =
    request.catalog_item.type === "composite" || vms.vms === 0
      ? t("failure.made.general")
      : vms.records
        ? t("failure.made.vmsAddressesRecords", { count: vms.vms })
        : vms.addresses
          ? t("failure.made.vmsAddresses", { count: vms.vms })
          : t("failure.made.vms", { count: vms.vms });
  const [alert] = useState(announce);
  const box = useRef<HTMLDivElement>(null);
  // Read as the dialog closes: the mutation's own success comes a render later.
  const rolledBack = useRef(false);
  const qc = useQueryClient();
  const [confirmRollback, setConfirmRollback] = useState(false);
  const done = (r: ServiceRequest, what: string) => {
    qc.setQueryData(keys.requests.detail(r.number), r);
    const refetched = qc.invalidateQueries({ queryKey: keys.requests.steps(r.id) });
    if (r.deployment_ids?.[0]) void qc.invalidateQueries({ queryKey: keys.deployments.detail(r.deployment_ids[0]) });
    toast.success(t("failure.toast", { number: r.number, what }));
    return refetched;
  };
  const failed = (e: unknown) =>
    toast.error(isApiError(e) ? e.problem.title : t("failure.didNotWork"), {
      description: isApiError(e) ? e.problem.detail : undefined,
    });
  const params = { params: { path: { requestId: request.id } } };
  const retry = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/retry", params)),
    onSuccess: (r) =>
      void catchFocus(done(r, undone ? t("failure.runningAgain") : t("failure.runningAgainFromFailed")), () =>
        standIn?.(),
      ),
    onError: failed,
  });
  const rollback = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/rollback", params)),
    onSuccess: (r) => {
      rolledBack.current = true;
      setConfirmRollback(false);
      void done(r, t("failure.rollingBack"));
    },
    onError: failed,
  });
  return (
    <div
      ref={box}
      role={alert ? "alert" : "group"}
      aria-label={alert ? undefined : t("failure.failed", { number: request.number })}
      tabIndex={-1}
      className="mt-1 flex flex-col gap-2 rounded-lg border border-bad bg-bad-soft px-3.5 py-3 outline-none focus-visible:ring-2 focus-visible:ring-accent"
    >
      <span lang="en" className="font-semibold text-bad">
        {request.error?.message}
      </span>
      {mayAct && (
        <>
          <span className="text-secondary">
            {request.kind !== "provision"
              ? t("failure.retryKeeps")
              : undone
                ? t("failure.rolledBack", { detail: undone.detail || t("failure.createdDeleted") })
                : rollbackFailed
                  ? t("failure.rollbackFailed", { error: rollbackFailed.error ?? "" })
                  : t("failure.keptForLook", { what: made })}
          </span>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="primary" onClick={() => retry.mutate()} disabled={retry.isPending}>
              {undone ? t("failure.retry") : t("failure.retryFromFailed")}
            </Button>
            {request.kind === "provision" && !undone && (
              <>
                <Button
                  size="sm"
                  variant="danger"
                  onClick={() => {
                    rolledBack.current = false;
                    setConfirmRollback(true);
                  }}
                >
                  {t("failure.rollBack")}
                </Button>
                <ConfirmDialog
                  open={confirmRollback}
                  onOpenChange={setConfirmRollback}
                  title={t("failure.confirmTitle", { number: request.number })}
                  description={t("failure.confirmBody", { what: made })}
                  confirmLabel={t("failure.rollBack")}
                  cancelLabel={t("failure.keep")}
                  pending={rollback.isPending}
                  onConfirm={() => rollback.mutate()}
                  returnFocus={() => (rolledBack.current ? box.current : null)}
                />
              </>
            )}
          </div>
        </>
      )}
    </div>
  );
}

/**
 * Whether `me` may retry or roll back the request, as core.failedRequest
 * decides: its requester, its owner or a project admin. A day-2 request's
 * deployment may have moved to another owner or project since: the server
 * decides those.
 */
export function mayActOnFailure(me: Me, request: ServiceRequest): boolean {
  return (
    request.requester.id === me.user.id ||
    request.on_behalf_of?.id === me.user.id ||
    Boolean(request.deployment_id) ||
    can(me, "request.manage", request.project.id)
  );
}
