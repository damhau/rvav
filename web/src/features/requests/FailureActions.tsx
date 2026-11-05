import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogClose, DialogContent } from "@/components/ui/dialog";
import { api, call, isApiError } from "@/lib/api/client";
import type { ServiceRequest, Step } from "@/lib/api/types";
import { keys } from "@/lib/query";

/**
 * Retry from the failed step, or roll back what a failed provisioning request
 * built. Once rolled back (its `rollback` step done: by hand, or by a
 * composite's rollback_on_failure), there is nothing left to roll back, and
 * a retry runs every step after the approval again, as the server does.
 *
 * The box is an alert, read out when it appears, unless `announce` is false
 * when it mounts: a page that moves it (a run's, with the selection) has it
 * read once.
 */
export function FailureActions({
  request,
  rolledBack,
  announce = true,
}: {
  request: ServiceRequest;
  rolledBack?: Step;
  announce?: boolean;
}) {
  const undone = request.kind !== "day2" && rolledBack;
  const [alert] = useState(announce);
  const qc = useQueryClient();
  const [confirmRollback, setConfirmRollback] = useState(false);
  const done = (r: ServiceRequest, what: string) => {
    qc.setQueryData(keys.requests.detail(r.number), r);
    void qc.invalidateQueries({ queryKey: keys.requests.steps(r.id) });
    if (r.deployment_ids?.[0]) void qc.invalidateQueries({ queryKey: keys.deployments.detail(r.deployment_ids[0]) });
    toast.success(`${r.number}: ${what}`);
  };
  const failed = (e: unknown) =>
    toast.error(isApiError(e) ? e.problem.title : "That didn't work", {
      description: isApiError(e) ? e.problem.detail : undefined,
    });
  const params = { params: { path: { requestId: request.id } } };
  const retry = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/retry", params)),
    onSuccess: (r) => done(r, undone ? "running again" : "running again from the failed step"),
    onError: failed,
  });
  const rollback = useMutation({
    mutationFn: () => call(api.POST("/requests/{requestId}/rollback", params)),
    onSuccess: (r) => {
      setConfirmRollback(false);
      done(r, "rolling back");
    },
    onError: failed,
  });
  return (
    <div
      role={alert ? "alert" : "group"}
      aria-label={alert ? undefined : `${request.number} failed`}
      className="mt-1 flex flex-col gap-2 rounded-lg border border-bad bg-bad-soft px-3.5 py-3"
    >
      <span className="font-semibold text-bad">{request.error?.message}</span>
      <span className="text-xs">
        {request.kind === "day2"
          ? "Retrying runs the failed step and the ones after it; the steps that were done are kept."
          : undone
            ? `It was rolled back: ${undone.detail || "what it created was deleted"}. Retry runs it again from its first step, without asking for approval again.`
            : "What was created is kept so you can look at it. Retry runs the failed step and the ones after it; Roll back deletes the VM, its IP address and DNS records."}
      </span>
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="primary" onClick={() => retry.mutate()} disabled={retry.isPending}>
          {undone ? "Retry" : "Retry from the failed step"}
        </Button>
        {request.kind !== "day2" && !undone && (
          <Dialog open={confirmRollback} onOpenChange={setConfirmRollback}>
            <Button size="sm" variant="danger" onClick={() => setConfirmRollback(true)}>
              Roll back
            </Button>
            <DialogContent
              title={`Roll back ${request.number}?`}
              description="Deletes what it created: the VM, its IP address and DNS records. You can retry it afterwards."
            >
              <div className="flex justify-end gap-2 px-4 py-3">
                <DialogClose asChild>
                  <Button>Keep it</Button>
                </DialogClose>
                <Button variant="danger-solid" onClick={() => rollback.mutate()} disabled={rollback.isPending}>
                  Roll back
                </Button>
              </div>
            </DialogContent>
          </Dialog>
        )}
      </div>
    </div>
  );
}
