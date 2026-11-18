// Day-2 actions (ADR-0013): the menu of what may run on a deployment or a
// VM, and the dialog that confirms, asks for inputs and starts the request.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, Ellipsis, ShieldCheck } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import {
  Menu,
  MenuContent,
  MenuItem,
  MenuLabel,
  MenuSeparator,
  MenuSub,
  MenuSubContent,
  MenuSubTrigger,
  MenuTrigger,
} from "@/components/ui/dropdown-menu";
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
  automation: "Automation",
  access: "Access",
  lifecycle: "Lifecycle",
};

const groupOrder = Object.keys(groupLabel) as Day2Action["group"][];

/** Actions that run without asking anything: no form, no confirmation, no approval, nothing lost. */
export const runsDirectly = (a: Day2Action) => !a.form && !a.confirm && !a.approval.required && !a.destructive;

/** Toast for an action that failed to start without a dialog to show it in. */
export function actionFailed(e: unknown) {
  toast.error(isApiError(e) ? e.problem.title : "The action could not start", {
    description: isApiError(e) ? e.problem.detail : undefined,
  });
}

/**
 * Start a day-2 action; the toast links to its request. A list, which has no
 * one deployment, passes each action's with it.
 */
export function useRunAction(deploymentId?: string) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: ({ draft, key, deployment = deploymentId }: { draft: Day2Draft; key: string; deployment?: string }) =>
      call(
        api.POST("/deployments/{deploymentId}/actions", {
          params: { path: { deploymentId: deployment ?? "" }, header: { "Idempotency-Key": key } },
          body: draft,
        }),
      ),
    onSuccess: (r, { deployment = deploymentId ?? "" }) => {
      void qc.invalidateQueries({ queryKey: keys.deployments.actions(deployment) });
      void qc.invalidateQueries({ queryKey: keys.deployments.history(deployment) });
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
        description={
          action.confirm?.message ||
          (action.destructive
            ? `${action.name} runs on ${target ?? deployment.name} as soon as you confirm.`
            : undefined)
        }
        className="w-[min(560px,calc(100vw-32px))]"
        onOpenAutoFocus={(e) => {
          // What it asks first (a field, the reason, the name to type) rather than Close.
          const field = (e.target as HTMLElement).querySelector<HTMLElement>(
            "form :is(input, textarea, select, [role=combobox]):not([disabled]):not([readonly])",
          );
          if (field) {
            e.preventDefault();
            field.focus();
          }
        }}
      >
        <form
          className="flex flex-col gap-3 px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (ready) submit();
          }}
        >
          {action.approval.required && (
            <p
              className="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-secondary text-warn"
              role="note"
            >
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
            <div className="flex flex-col gap-1.5 text-secondary">
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
            <div className="flex flex-col gap-1.5 text-secondary">
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
            <p key={m} className="text-secondary text-bad" role="alert">
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

/**
 * Every action of a deployment in one menu: its own and its VM's for one VM;
 * for several, its own, then a submenu per VM.
 */
export function DeploymentActionsMenu({
  deploymentId,
  actions,
  loading,
  failed,
  onRetry,
  onPick,
}: {
  deploymentId: string;
  actions: Day2Action[];
  loading: boolean;
  /** The actions couldn't be read: the menu says so, and offers to read them again. */
  failed?: boolean;
  onRetry?: () => void;
  onPick: (a: Day2Action) => void;
}) {
  const vms = [
    ...new Map(
      actions.filter((a) => a.resource_id).map((a) => [a.resource_id!, a.resource_name ?? a.resource_id!]),
    ).entries(),
  ];
  const single = vms.length === 1 ? vms[0]![0] : undefined;
  const top = single ? actions : actions.filter((a) => !a.resource_id);
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button>
          Actions <ChevronDown />
        </Button>
      </MenuTrigger>
      <MenuContent className="min-w-64">
        {loading ? (
          <MenuLabel>Loading…</MenuLabel>
        ) : failed ? (
          <>
            <MenuLabel>The actions could not be loaded</MenuLabel>
            {onRetry && <MenuItem onSelect={onRetry}>Try again</MenuItem>}
          </>
        ) : (
          <>
            {(top.length > 0 || vms.length === 0) && (
              <ActionItems
                actions={top}
                onPick={onPick}
                consoleLink={single ? { deploymentId, resourceId: single } : undefined}
              />
            )}
            {!single && vms.length > 0 && (
              <>
                {top.length > 0 && <MenuSeparator />}
                <MenuLabel>Virtual machines</MenuLabel>
                {vms.map(([id, name]) => (
                  <MenuSub key={id}>
                    <MenuSubTrigger>{name}</MenuSubTrigger>
                    <MenuSubContent className="min-w-60">
                      <ActionItems
                        actions={actions.filter((a) => a.resource_id === id)}
                        onPick={onPick}
                        consoleLink={{ deploymentId, resourceId: id }}
                      />
                    </MenuSubContent>
                  </MenuSub>
                ))}
              </>
            )}
          </>
        )}
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
                    {a.approval.required && <span className="ml-1.5 text-caption text-warn">approval</span>}
                  </span>
                  {!a.enabled && a.disabled_reason && (
                    <span className="text-caption leading-snug text-faint">{a.disabled_reason}</span>
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

/**
 * A deployment's actions, for a list row's ⋯ menu: they load as it opens.
 * One VM: its actions too; several: only the deployment's (each VM's are on
 * its page).
 */
export function RowActionItems({
  deployment,
  onPick,
}: {
  deployment: Deployment;
  onPick: (deployment: Deployment, a: Day2Action) => void;
}) {
  const list = useQuery({ ...q.deploymentActions(deployment.id), staleTime: 0 });
  if (list.isPending) return <MenuLabel>Loading…</MenuLabel>;
  if (list.isError) return <MenuLabel>The actions could not be loaded</MenuLabel>;
  const vms = new Set(list.data.items.filter((a) => a.resource_id).map((a) => a.resource_id));
  const actions = list.data.items.filter((a) => !a.resource_id || vms.size === 1);
  const vm = actions.find((a) => a.resource_id)?.resource_id;
  return (
    <div className="min-w-56">
      <ActionItems
        actions={actions}
        consoleLink={vm ? { deploymentId: deployment.id, resourceId: vm } : undefined}
        onPick={(a) => onPick(deployment, a)}
      />
    </div>
  );
}

/** What a list's row menus picked: it runs at once when it asks nothing, else through its dialog. */
export function usePickedAction() {
  const [picked, setPicked] = useState<{ deployment: Deployment; action: Day2Action } | null>(null);
  const run = useRunAction();
  const pick = (deployment: Deployment, a: Day2Action) => {
    if (!runsDirectly(a)) return setPicked({ deployment, action: a });
    run.mutate(
      {
        deployment: deployment.id,
        draft: { action: a.key, ...(a.resource_id ? { resource_id: a.resource_id } : {}), inputs: {} },
        key: newIdempotencyKey(),
      },
      { onError: actionFailed },
    );
  };
  const dialog = picked && (
    <ActionDialog deployment={picked.deployment} action={picked.action} onClose={() => setPicked(null)} />
  );
  return { pick, dialog };
}
