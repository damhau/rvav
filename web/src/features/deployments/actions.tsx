// Day-2 actions (ADR-0013): the menu of what may run on a deployment or a
// VM, and the dialog that confirms, asks for inputs and starts the request.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Ellipsis, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { Input, Textarea } from "@/components/ui/input";
import { FormRenderer } from "@/forms/FormRenderer";
import { mapFieldErrors, type MappedErrors } from "@/forms/errors";
import { api, call, isApiError } from "@/lib/api/client";
import type { Day2Action, Day2Draft, Deployment, RequestForm, ServiceRequest } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { newIdempotencyKey } from "@/lib/utils";

export const groupLabel: Record<Day2Action["group"], string> = {
  power: "Power",
  configuration: "Configuration",
  snapshots: "Snapshots",
  access: "Access",
  lifecycle: "Lifecycle",
};

const groupOrder = Object.keys(groupLabel) as Day2Action["group"][];

/** Actions that run without asking anything: no form, no confirmation, no approval. */
export const runsDirectly = (a: Day2Action) => !a.form && !a.confirm && !a.approval.required;

/** Toast for an action that failed to start without a dialog to show it in. */
export function actionFailed(e: unknown) {
  toast.error(isApiError(e) ? e.problem.title : "The action could not start", {
    description: isApiError(e) ? e.problem.detail : undefined,
  });
}

/** Start a day-2 action; the toast links to its request. */
export function useRunAction(deploymentId: string) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: ({ draft, key }: { draft: Day2Draft; key: string }) =>
      call(
        api.POST("/deployments/{deploymentId}/actions", {
          params: { path: { deploymentId }, header: { "Idempotency-Key": key } },
          body: draft,
        }),
      ),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: keys.deployments.actions(deploymentId) });
      void qc.invalidateQueries({ queryKey: keys.deployments.history(deploymentId) });
      void qc.invalidateQueries({ queryKey: keys.requests.lists() });
      toast.success(r.title, {
        description: r.status === "pending_approval" ? `${r.number} waits for approval.` : `${r.number} has started.`,
        action: {
          label: "Follow",
          onClick: () => void navigate({ to: "/requests/$requestId", params: { requestId: r.number } }),
        },
      });
    },
  });
}

function asForm(a: Day2Action): RequestForm | null {
  return a.form ? { item_id: "", version_id: "", ...a.form } : null;
}

/** Confirm, fill in and start one action. */
export function ActionDialog({
  deployment,
  action,
  onClose,
}: {
  deployment: Pick<Deployment, "id" | "name" | "project" | "environment">;
  action: Day2Action;
  onClose: (started?: ServiceRequest) => void;
}) {
  const form = asForm(action);
  const [values, setValues] = useState<Record<string, unknown>>(() => ({ ...form?.initial_values }));
  const [typed, setTyped] = useState("");
  const [justification, setJustification] = useState("");
  const [errors, setErrors] = useState<MappedErrors | null>(null);
  const [key, setKey] = useState(newIdempotencyKey);
  const run = useRunAction(deployment.id);
  const mustType = action.confirm?.type_to_confirm;
  const ready = !mustType || typed === mustType;

  const submit = () => {
    const draft: Day2Draft = {
      action: action.key,
      ...(action.resource_id ? { resource_id: action.resource_id } : {}),
      inputs: values,
      ...(justification.trim() ? { justification: justification.trim() } : {}),
    };
    run.mutate(
      { draft, key },
      {
        onSuccess: (r) => {
          setKey(newIdempotencyKey());
          onClose(r);
        },
        onError: (e) => {
          setKey(newIdempotencyKey());
          if (isApiError(e) && e.status === 422) {
            setErrors(mapFieldErrors(e.fieldErrors));
            return;
          }
          setErrors({ fields: {}, form: [isApiError(e) ? (e.problem.detail ?? e.problem.title) : String(e)] });
        },
      },
    );
  };

  const target = action.resource_id ? action.resource_name : deployment.name;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        title={`${action.name} · ${target ?? deployment.name}`}
        description={action.confirm?.message}
        className="w-[min(560px,calc(100vw-32px))]"
      >
        <form
          className="flex flex-col gap-3 px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (ready) submit();
          }}
        >
          {action.approval.required && (
            <p className="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-xs text-warn" role="note">
              <ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              <span>Needs approval: {action.approval.reasons.join("; ")}. It starts once approved.</span>
            </p>
          )}
          {form && (
            <FormRenderer
              form={form}
              values={values}
              onChange={(name, value) => {
                setValues((v) => ({ ...v, [name]: value }));
                setErrors(null);
              }}
              errors={errors?.fields}
              ctx={{ versionId: "", projectId: deployment.project.id, environment: deployment.environment }}
            />
          )}
          {action.approval.required && (
            <div className="flex flex-col gap-1.5 text-xs">
              <label htmlFor="action-justification" className="font-medium">
                Why (for the approvers)
              </label>
              <Textarea
                id="action-justification"
                value={justification}
                onChange={(e) => setJustification(e.target.value)}
                rows={2}
              />
            </div>
          )}
          {mustType && (
            <div className="flex flex-col gap-1.5 text-xs">
              <label htmlFor="action-confirm">
                Type <span className="font-mono font-medium">{mustType}</span> to confirm
              </label>
              <Input
                id="action-confirm"
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                autoComplete="off"
                spellCheck={false}
              />
            </div>
          )}
          {errors?.form.map((m) => (
            <p key={m} className="text-xs text-bad" role="alert">
              {m}
            </p>
          ))}
          <div className="flex justify-end gap-2 pt-1">
            <Button type="button" onClick={() => onClose()}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant={action.destructive ? "danger-solid" : "primary"}
              disabled={!ready || run.isPending}
            >
              {action.approval.required ? "Ask for approval" : action.name}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** The actions of one target, by group, as a menu. Disabled ones say why. */
export function ActionsMenu({
  actions,
  label,
  onPick,
  consoleLink,
}: {
  actions: Day2Action[];
  label: string;
  onPick: (a: Day2Action) => void;
  /** Where the console action goes (it is not a request). */
  consoleLink?: { deploymentId: string; resourceId: string };
}) {
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button size="icon" aria-label={label}>
          <Ellipsis />
        </Button>
      </MenuTrigger>
      <MenuContent className="min-w-60">
        <ActionItems actions={actions} onPick={onPick} consoleLink={consoleLink} />
      </MenuContent>
    </Menu>
  );
}

function ActionItems({
  actions,
  onPick,
  consoleLink,
}: {
  actions: Day2Action[];
  onPick: (a: Day2Action) => void;
  consoleLink?: { deploymentId: string; resourceId: string };
}) {
  const navigate = useNavigate();
  const groups = groupOrder
    .map((g) => ({ g, items: actions.filter((a) => a.group === g && (a.key !== "console" || consoleLink)) }))
    .filter((x) => x.items.length);
  if (groups.length === 0) return <MenuLabel>No actions apply</MenuLabel>;
  return (
    <>
      {groups.map(({ g, items }, i) => (
        <div key={g}>
          {i > 0 && <MenuSeparator />}
          <MenuLabel>{groupLabel[g]}</MenuLabel>
          {items.map((a) =>
            a.key === "console" && consoleLink ? (
              <MenuItem
                key={a.key}
                disabled={!a.enabled}
                onSelect={() =>
                  void navigate({ to: "/deployments/$deploymentId/console/$resourceId", params: consoleLink })
                }
              >
                {a.name}
              </MenuItem>
            ) : (
              <MenuItem
                key={a.key}
                disabled={!a.enabled}
                danger={a.destructive && a.group === "lifecycle"}
                onSelect={() => onPick(a)}
                title={a.disabled_reason}
              >
                <span className="flex min-w-0 flex-col">
                  <span>
                    {a.name}
                    {a.approval.required && <span className="ml-1.5 text-[11px] text-warn">approval</span>}
                  </span>
                  {!a.enabled && a.disabled_reason && (
                    <span className="text-[11px] leading-snug text-faint">{a.disabled_reason}</span>
                  )}
                </span>
              </MenuItem>
            ),
          )}
        </div>
      ))}
    </>
  );
}

/** A deployment's actions menu in a list: its actions load when it opens. */
export function RowActions({ deployment }: { deployment: Deployment }) {
  const [open, setOpen] = useState(false);
  const [picked, setPicked] = useState<Day2Action | null>(null);
  const qc = useQueryClient();
  const run = useRunAction(deployment.id);
  const [actions, setActions] = useState<Day2Action[] | null>(null);
  const load = async () => {
    const list = await qc.fetchQuery({ ...q.deploymentActions(deployment.id), staleTime: 0 });
    // One VM: its actions too; several: only the deployment's (each VM's are on its page).
    const vms = new Set(list.items.filter((a) => a.resource_id).map((a) => a.resource_id));
    setActions(list.items.filter((a) => !a.resource_id || vms.size === 1));
  };
  const vm = actions?.find((a) => a.resource_id)?.resource_id;
  return (
    <>
      <Menu
        open={open}
        onOpenChange={(o) => {
          setOpen(o);
          if (o) void load();
        }}
      >
        <MenuTrigger asChild>
          <Button variant="ghost" size="icon" aria-label={`Actions for ${deployment.name}`}>
            <Ellipsis />
          </Button>
        </MenuTrigger>
        <MenuContent className="min-w-60">
          {actions === null ? (
            <MenuLabel>Loading…</MenuLabel>
          ) : (
            <ActionItems
              actions={actions}
              consoleLink={vm ? { deploymentId: deployment.id, resourceId: vm } : undefined}
              onPick={(a) => {
                if (runsDirectly(a)) {
                  run.mutate(
                    {
                      draft: { action: a.key, ...(a.resource_id ? { resource_id: a.resource_id } : {}), inputs: {} },
                      key: newIdempotencyKey(),
                    },
                    { onError: actionFailed },
                  );
                } else setPicked(a);
              }}
            />
          )}
        </MenuContent>
      </Menu>
      {picked && <ActionDialog deployment={deployment} action={picked} onClose={() => setPicked(null)} />}
    </>
  );
}
