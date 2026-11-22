// Day-2 actions (ADR-0013): the menu of what may run on a deployment or a
// VM, and the dialog that confirms, asks for inputs and starts the request.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, Ellipsis, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import {
  acceptedWarnings,
  allWarnings,
  onlyWarned,
  PolicyWarnings,
  refusedWarnings,
  type PolicyWarning,
} from "@/components/common/warnings";
import { Input, Textarea } from "@/components/ui/input";
import { focusInvalid } from "@/components/ui/opener";
import { FormRenderer } from "@/forms/FormRenderer";
import { sentInputs } from "@/forms/defaults";
import { mapFieldErrors, type MappedErrors } from "@/forms/errors";
import type { FormContext } from "@/forms/useFieldValues";
import { api, call, isApiError } from "@/lib/api/client";
import type { Day2Action, Day2Draft, Deployment, RequestForm, ServiceRequest } from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { i18n } from "@/lib/i18n";
import { keys, q } from "@/lib/query";
import { newIdempotencyKey } from "@/lib/utils";
import { day2Name } from "@/lib/i18n/names";

const groupOrder: Day2Action["group"][] = ["power", "configuration", "snapshots", "automation", "access", "lifecycle"];

/** Actions that run without asking anything: no form, no confirmation, no approval, no warning, nothing lost. */
export const runsDirectly = (a: Day2Action) =>
  !a.form && !a.confirm && !a.approval.required && !a.destructive && !a.warnings?.length;

/** Toast for an action that failed to start without a dialog to show it in. */
export function actionFailed(e: unknown) {
  toast.error(isApiError(e) ? e.problem.title : i18n.t("deployments:actions.couldNotStart"), {
    description: isApiError(e) ? e.problem.detail : undefined,
  });
}

/**
 * Start a day-2 action; the toast links to its request. A list, which has no
 * one deployment, passes each action's with it.
 */
export function useRunAction(deploymentId?: string) {
  const { t } = useTranslation("deployments");
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
      void qc.invalidateQueries({ queryKey: keys.vms.all });
      toast.success(r.title, {
        description: t(r.status === "pending_approval" ? "actions.waits" : "actions.started", { number: r.number }),
        action: {
          label: t("actions.follow"),
          onClick: () => void navigate({ to: "/requests/$requestId", params: { requestId: r.number } }),
        },
      });
    },
  });
}

function asForm(a: Day2Action): RequestForm | null {
  return a.form ? { item_id: "", version_id: "", ...a.form } : null;
}

/**
 * A designed form's states and problems for the inputs so far, worked out
 * on the target by the server (ADR-0050); nothing for the other forms.
 */
function useActionPreview(deploymentId: string, action: Day2Action, inputs: Record<string, unknown> | null) {
  const body = useMemo(
    () => ({
      action: action.key,
      ...(action.resource_id ? { resource_id: action.resource_id } : {}),
      inputs: inputs ?? {},
    }),
    [action.key, action.resource_id, inputs],
  );
  const debounced = useDebounced(body, 300);
  return useQuery({
    queryKey: keys.deployments.actionPreview(deploymentId, debounced),
    queryFn: ({ signal }) =>
      call(
        api.POST("/deployments/{deploymentId}/actions/preview", {
          params: { path: { deploymentId } },
          body: debounced,
          signal,
        }),
      ),
    enabled: inputs !== null,
    placeholderData: keepPreviousData,
  });
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
  const { t } = useTranslation(["deployments", "common"]);
  const form = useMemo(() => asForm(action), [action]);
  const designed = Boolean(action.form?.designed);
  const [values, setValues] = useState<Record<string, unknown>>(() => ({ ...form?.initial_values }));
  const sent = useMemo(
    () => (form ? sentInputs(form.ui_schema.fields, values, form.initial_values) : values),
    [form, values],
  );
  const preview = useActionPreview(deployment.id, action, designed ? sent : null);
  // A field's live problems show once it was left, as a request form's do.
  const [touched, setTouched] = useState<Set<string>>(new Set());
  const [typed, setTyped] = useState("");
  const [justification, setJustification] = useState("");
  const [errors, setErrors] = useState<MappedErrors | null>(null);
  // Counts the refusals with problems on inputs: each moves the focus to the first one.
  const [refusals, setRefusals] = useState(0);
  const formRef = useRef<HTMLFormElement>(null);
  useEffect(() => {
    if (refusals) focusInvalid(formRef.current);
  }, [refusals]);
  // What the server warned about on submit, such as an extension longer than a soft lease allows, until
  // the inputs change.
  const [refused, setRefused] = useState<PolicyWarning[]>([]);
  const [warnedRefusal, setWarnedRefusal] = useState(0);
  const warnings = allWarnings(action.warnings, refused);
  const [key, setKey] = useState(newIdempotencyKey);
  const run = useRunAction(deployment.id);
  const mustType = action.confirm?.type_to_confirm;
  const ready = !mustType || typed === mustType;

  const submit = () => {
    const draft: Day2Draft = {
      action: action.key,
      ...(action.resource_id ? { resource_id: action.resource_id } : {}),
      inputs: sent,
      ...(justification.trim() ? { justification: justification.trim() } : {}),
      accepted_warnings: acceptedWarnings(warnings),
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
          const warned = refusedWarnings(e);
          if (warned.length) setRefused(warned);
          if (onlyWarned(e)) {
            setErrors(null);
            setWarnedRefusal((n) => n + 1);
            return;
          }
          if (isApiError(e) && e.status === 422) {
            const mapped = mapFieldErrors(e.fieldErrors.filter((f) => f.field !== "/accept_warnings"));
            setErrors(mapped);
            if (Object.keys(mapped.fields).length) setRefusals((n) => n + 1);
            return;
          }
          setErrors({ fields: {}, form: [isApiError(e) ? (e.problem.detail ?? e.problem.title) : String(e)] });
        },
      },
    );
  };

  const target = action.resource_id ? action.resource_name : deployment.name;
  const live = mapFieldErrors(preview.data?.errors).fields;
  const fieldErrors = {
    ...Object.fromEntries(Object.entries(live).filter(([name]) => touched.has(name))),
    ...errors?.fields,
  };
  const ctx: FormContext = designed
    ? {
        versionId: `day2:${action.key}`,
        projectId: deployment.project.id,
        environment: deployment.environment,
        day2: { deploymentId: deployment.id, action: action.key, resourceId: action.resource_id ?? undefined },
      }
    : { versionId: "", projectId: deployment.project.id, environment: deployment.environment };
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        title={`${day2Name(action)} · ${target ?? deployment.name}`}
        description={
          action.confirm?.message ||
          (action.destructive
            ? t("actions.runsOnConfirm", { action: day2Name(action), target: target ?? deployment.name })
            : undefined)
        }
        className="w-[min(560px,calc(100vw-32px))]"
        onOpenAutoFocus={(e) => {
          // What it asks first (a field, the reason, the name to type) rather than Close; a risky
          // action's options, such as Delete's, come after the name to type.
          const root = e.target as HTMLElement;
          const field =
            (mustType ? root.querySelector<HTMLElement>("#action-confirm") : null) ??
            root.querySelector<HTMLElement>(
              "form :is(input, textarea, select, [role=combobox]):not([disabled]):not([readonly])",
            );
          if (field) {
            e.preventDefault();
            field.focus();
          }
        }}
      >
        <form
          ref={formRef}
          className="flex flex-col gap-3 px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (ready && !run.isPending) submit();
          }}
        >
          {action.approval.required && (
            <p
              className="flex items-start gap-2 rounded-md bg-warn-soft px-3 py-2 text-secondary text-warn"
              role="note"
            >
              <ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden />
              <span>{t("actions.needsApproval", { reasons: action.approval.reasons.join("; ") })}</span>
            </p>
          )}
          <PolicyWarnings warnings={warnings} refusal={warnedRefusal} />
          {mustType && (
            <div className="flex flex-col gap-1.5 text-secondary">
              <label htmlFor="action-confirm">
                <Trans
                  t={t}
                  i18nKey="typeToConfirm"
                  values={{ text: mustType }}
                  components={{ text: <span className="font-mono font-medium" /> }}
                />
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
          {form && (
            <FormRenderer
              form={form}
              values={values}
              onChange={(name, value) => {
                setValues((v) => ({ ...v, [name]: value }));
                setErrors(null);
                if (!Object.is(values[name], value)) setRefused([]);
              }}
              onBlur={(name) => setTouched((t) => (t.has(name) ? t : new Set(t).add(name)))}
              errors={fieldErrors}
              ctx={ctx}
              fieldStates={preview.data?.fields}
            />
          )}
          {action.approval.required && (
            <div className="flex flex-col gap-1.5 text-secondary">
              <label htmlFor="action-justification" className="font-medium">
                {t("actions.why")}
              </label>
              <Textarea
                id="action-justification"
                value={justification}
                onChange={(e) => setJustification(e.target.value)}
                rows={2}
              />
            </div>
          )}
          {preview.isError && (
            <p className="text-secondary text-bad" role="alert">
              {t("actions.rulesFailed", {
                error: isApiError(preview.error)
                  ? (preview.error.problem.detail ?? preview.error.problem.title)
                  : String(preview.error),
              })}
            </p>
          )}
          {errors?.form.map((m) => (
            <p key={m} className="text-secondary text-bad" role="alert">
              {m}
            </p>
          ))}
          <div className="flex justify-end gap-2 pt-1">
            <Button type="button" onClick={() => onClose()}>
              {t("common:actions.cancel")}
            </Button>
            <Button
              type="submit"
              variant={action.destructive ? "danger-solid" : "primary"}
              disabled={!ready}
              aria-disabled={run.isPending || undefined}
              className="aria-disabled:opacity-50"
            >
              {action.approval.required
                ? t(warnings.length > 0 ? "actions.askApprovalAnyway" : "actions.askApproval")
                : warnings.length > 0
                  ? t("actions.anyway", { action: day2Name(action) })
                  : day2Name(action)}
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
 * for several, its own, then a submenu per VM. Its custom resources' come
 * last, a submenu each.
 */
export function DeploymentActionsMenu({
  deploymentId,
  actions,
  custom = [],
  loading,
  failed,
  onRetry,
  onPick,
}: {
  deploymentId: string;
  actions: Day2Action[];
  /** Its custom resources' IDs: their actions aren't a VM's. */
  custom?: string[];
  loading: boolean;
  /** The actions couldn't be read: the menu says so, and offers to read them again. */
  failed?: boolean;
  onRetry?: () => void;
  onPick: (a: Day2Action) => void;
}) {
  const { t } = useTranslation(["deployments", "common"]);
  const targets = [
    ...new Map(
      actions.filter((a) => a.resource_id).map((a) => [a.resource_id!, a.resource_name ?? a.resource_id!]),
    ).entries(),
  ];
  const vms = targets.filter(([id]) => !custom.includes(id));
  const resources = targets.filter(([id]) => custom.includes(id));
  const single = vms.length === 1 ? vms[0]![0] : undefined;
  const top = actions.filter((a) => !a.resource_id || a.resource_id === single);
  return (
    <Menu>
      <MenuTrigger asChild>
        <Button>
          {t("actions.menu")} <ChevronDown />
        </Button>
      </MenuTrigger>
      <MenuContent className="min-w-64">
        {loading ? (
          <MenuLabel>{t("common:loadingEllipsis")}</MenuLabel>
        ) : failed ? (
          <>
            <MenuLabel>{t("actions.notLoaded")}</MenuLabel>
            {onRetry && <MenuItem onSelect={onRetry}>{t("common:error.retry")}</MenuItem>}
          </>
        ) : (
          <>
            {(top.length > 0 || vms.length === 0) && (
              <ActionItems
                actions={top}
                onPick={onPick}
                deploymentId={deploymentId}
                consoleLink={single ? { deploymentId, resourceId: single } : undefined}
              />
            )}
            {!single && vms.length > 0 && (
              <>
                {top.length > 0 && <MenuSeparator />}
                <MenuLabel>{t("actions.vms")}</MenuLabel>
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
            {resources.length > 0 && (
              <>
                <MenuSeparator />
                <MenuLabel>{t("actions.custom")}</MenuLabel>
                {resources.map(([id, name]) => (
                  <MenuSub key={id}>
                    <MenuSubTrigger>{name}</MenuSubTrigger>
                    <MenuSubContent className="min-w-60">
                      <ActionItems actions={actions.filter((a) => a.resource_id === id)} onPick={onPick} />
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
  deploymentId,
  consoleLink,
}: {
  actions: Day2Action[];
  onPick: (a: Day2Action) => void;
  /** Where Update goes (it has a page of its own). */
  deploymentId?: string;
  consoleLink?: { deploymentId: string; resourceId: string };
}) {
  const { t } = useTranslation("deployments");
  const navigate = useNavigate();
  const groups = groupOrder
    .map((g) => ({
      g,
      items: actions.filter(
        (a) => a.group === g && (a.key !== "console" || consoleLink) && (a.key !== "update" || deploymentId),
      ),
    }))
    .filter((x) => x.items.length);
  if (groups.length === 0) return <MenuLabel>{t("actions.none")}</MenuLabel>;
  return (
    <>
      {groups.map(({ g, items }, i) => (
        <div key={g}>
          {i > 0 && <MenuSeparator />}
          <MenuLabel>{t(`actions.group.${g}`)}</MenuLabel>
          {items.map((a) =>
            a.key === "console" && consoleLink ? (
              <MenuItem
                key={a.key}
                disabled={!a.enabled}
                onSelect={() =>
                  void navigate({ to: "/deployments/$deploymentId/console/$resourceId", params: consoleLink })
                }
              >
                {day2Name(a)}
              </MenuItem>
            ) : (
              <MenuItem
                key={a.key}
                disabled={!a.enabled}
                danger={a.destructive && a.group === "lifecycle"}
                onSelect={() =>
                  a.key === "update" && deploymentId
                    ? void navigate({ to: "/deployments/$deploymentId/update", params: { deploymentId } })
                    : onPick(a)
                }
                title={a.disabled_reason}
              >
                <span className="flex min-w-0 flex-col">
                  <span>
                    {day2Name(a)}
                    {a.approval.required && (
                      <span className="ml-1.5 text-caption text-warn">{t("actions.approval")}</span>
                    )}
                    {Boolean(a.warnings?.length) && (
                      <span className="ml-1.5 text-caption text-warn">{t("actions.warns")}</span>
                    )}
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
  const { t } = useTranslation(["deployments", "common"]);
  const list = useQuery({ ...q.deploymentActions(deployment.id), staleTime: 0 });
  if (list.isPending) return <MenuLabel>{t("common:loadingEllipsis")}</MenuLabel>;
  if (list.isError) return <MenuLabel>{t("actions.notLoaded")}</MenuLabel>;
  const vms = new Set(list.data.items.filter((a) => a.resource_id).map((a) => a.resource_id));
  const actions = list.data.items.filter((a) => !a.resource_id || vms.size === 1);
  const vm = actions.find((a) => a.resource_id)?.resource_id;
  return (
    <div className="min-w-56">
      <ActionItems
        actions={actions}
        deploymentId={deployment.id}
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
