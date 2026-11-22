import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { api, call, isApiError } from "@/lib/api/client";
import type { Me, ServiceRequest, Step } from "@/lib/api/types";
import { can } from "@/lib/can";
import { keys } from "@/lib/query";

/**
 * Retry from the failed step, or roll back what a failed provisioning request
 * built. Once rolled back (its `rollback` step done: by hand, or by a
 * composite's rollback_on_failure), there is nothing left to roll back, and
 * a retry runs every step after the approval again, as the server does.
 *
 * The box is an alert, read out when it appears, unless `announce` is false
 * when it mounts: a page that moves it (a run's, with the selection) has it
 * read once. Without `mayAct` (an auditor's), it says only what failed.
 */
export function FailureActions({
  request,
  rolledBack,
  announce = true,
  mayAct = true,
}: {
  request: ServiceRequest;
  rolledBack?: Step;
  announce?: boolean;
  mayAct?: boolean;
}) {
  const { t } = useTranslation("requests");
  const undone = request.kind === "provision" && rolledBack;
  const [alert] = useState(announce);
  const qc = useQueryClient();
  const [confirmRollback, setConfirmRollback] = useState(false);
  const done = (r: ServiceRequest, what: string) => {
    qc.setQueryData(keys.requests.detail(r.number), r);
    void qc.invalidateQueries({ queryKey: keys.requests.steps(r.id) });
    if (r.deployment_ids?.[0]) void qc.invalidateQueries({ queryKey: keys.deployments.detail(r.deployment_ids[0]) });
    toast.success(t("failure.toast", { number: r.number, what }));
  };
  const failed = (e: unknown) =>
    toast.error(isApiError(e) ? e.problem.title : t("failure.didNotWork"), {
      description: isApiError(e) ? e.problem.detail : undefined,
    });
  const params = { params: { path: { requestId: request.id } } };
  const retry = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/retry", params)),
    onSuccess: (r) => done(r, undone ? t("failure.runningAgain") : t("failure.runningAgainFromFailed")),
    onError: failed,
  });
  const rollback = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/rollback", params)),
    onSuccess: (r) => {
      setConfirmRollback(false);
      done(r, t("failure.rollingBack"));
    },
    onError: failed,
  });
  return (
    <div
      role={alert ? "alert" : "group"}
      aria-label={alert ? undefined : t("failure.failed", { number: request.number })}
      className="mt-1 flex flex-col gap-2 rounded-lg border border-bad bg-bad-soft px-3.5 py-3"
    >
      <span className="font-semibold text-bad">{request.error?.message}</span>
      {mayAct && (
        <>
          <span className="text-secondary">
            {request.kind !== "provision"
              ? t("failure.retryKeeps")
              : undone
                ? t("failure.rolledBack", { detail: undone.detail || t("failure.createdDeleted") })
                : t("failure.keptForLook")}
          </span>
          <div className="flex flex-wrap gap-2">
            <Button size="sm" variant="primary" onClick={() => retry.mutate()} disabled={retry.isPending}>
              {undone ? t("failure.retry") : t("failure.retryFromFailed")}
            </Button>
            {request.kind === "provision" && !undone && (
              <>
                <Button size="sm" variant="danger" onClick={() => setConfirmRollback(true)}>
                  {t("failure.rollBack")}
                </Button>
                <ConfirmDialog
                  open={confirmRollback}
                  onOpenChange={setConfirmRollback}
                  title={t("failure.confirmTitle", { number: request.number })}
                  description={t("failure.confirmBody")}
                  confirmLabel={t("failure.rollBack")}
                  cancelLabel={t("failure.keep")}
                  pending={rollback.isPending}
                  onConfirm={() => rollback.mutate()}
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
