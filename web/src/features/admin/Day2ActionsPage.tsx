import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Wrench } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { DataTable, type Column } from "@/components/ui/data-table";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { AwxAction, AwxActionUpdate } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { fullDate, relative } from "@/lib/utils";
import { failed, fieldErrors } from "./governance/common";

type Target = AwxAction["types"][number];

const targetLabel: Record<Target, string> = { "vsphere.vm": "VMs", deployment: "Deployments" };

function Status({ a }: { a: AwxAction }) {
  if (a.gone) return <Badge tone="neutral">Not in AWX</Badge>;
  if (!a.enabled) return <Badge tone="neutral">Off</Badge>;
  if (a.warnings.length > 0) return <Badge tone="warn">Check settings</Badge>;
  return <Badge tone="ok">Offered</Badge>;
}

function usePatch() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ a, body }: { a: AwxAction; body: AwxActionUpdate; done: string }) =>
      call(api.PATCH("/admin/awx/actions/{key}", { params: { path: { key: a.key } }, body })),
    onSuccess: (saved, { done }) => {
      qc.setQueryData(keys.awxActions, (old: { items: AwxAction[] } | undefined) =>
        old ? { items: old.items.map((x) => (x.key === saved.key ? saved : x)) } : old,
      );
      toast.success(done);
    },
  });
}

function ActionSheet({ a, onClose }: { a: AwxAction; onClose: () => void }) {
  const patch = usePatch();
  const [name, setName] = useState(a.name);
  const [types, setTypes] = useState<Target[]>(a.types);
  const [condition, setCondition] = useState(a.condition);
  const [enabled, setEnabled] = useState(a.enabled);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const toggle = (t: Target, on: boolean) => setTypes((cur) => (on ? [...cur, t] : cur.filter((x) => x !== t)));
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={patch.isPending}
      title={`Change ${a.name}`}
      description={
        <>
          AWX {a.workflow ? "workflow" : "job template"} <span className="font-mono">{a.template}</span>. Its survey is
          the form people fill in.
        </>
      }
      submitLabel={patch.isPending ? "Saving…" : "Save action"}
      submitDisabled={types.length === 0 || name.trim() === ""}
      onSubmit={() => {
        setErrors({});
        patch.mutate(
          { a, body: { name, types, condition, enabled }, done: `${name} saved` },
          {
            onSuccess: onClose,
            onError: (e) => {
              setErrors(fieldErrors(e));
              failed(e, "Not saved");
            },
          },
        );
      }}
    >
      <Field
        label="Name"
        htmlFor="awx-action-name"
        hint={
          name.trim() === ""
            ? "What the action menu says: the action needs one to be saved"
            : "What the action menu says"
        }
        error={errors["/name"]}
      >
        <Input id="awx-action-name" value={name} maxLength={80} onChange={(e) => setName(e.target.value)} />
      </Field>
      <fieldset
        className="flex flex-col gap-1.5"
        aria-describedby={errors["/types"] ? "awx-action-types-error" : undefined}
      >
        <legend className="mb-1 text-secondary font-medium">Runs on</legend>
        {(Object.keys(targetLabel) as Target[]).map((t) => (
          <div key={t} className="flex items-center gap-2 text-body">
            <Checkbox id={`awx-action-${t}`} checked={types.includes(t)} onChange={(on) => toggle(t, on)} />
            <label htmlFor={`awx-action-${t}`}>{targetLabel[t]}</label>
            <span className="text-secondary text-faint">
              {t === "deployment" ? "every VM of the deployment at once" : "one VM at a time"}
            </span>
          </div>
        ))}
        {types.length === 0 && <p className="text-secondary text-muted">Choose where it runs.</p>}
        {errors["/types"] && (
          <p id="awx-action-types-error" className="text-secondary text-bad">
            {errors["/types"]}
          </p>
        )}
      </fieldset>
      <Field
        label="Condition"
        htmlFor="awx-action-condition"
        error={errors["/condition"]}
        hint={
          <>
            Offered only where this CEL expression is true, for example{" "}
            <span className="font-mono">resource.os_family == &apos;linux&apos;</span>. Empty: everywhere.
          </>
        }
      >
        <Textarea
          id="awx-action-condition"
          className="font-mono text-secondary"
          value={condition}
          onChange={(e) => setCondition(e.target.value)}
        />
      </Field>
      <label htmlFor="awx-action-enabled" className="flex items-center gap-2 text-body">
        <Checkbox id="awx-action-enabled" checked={enabled} onChange={setEnabled} />
        Offered on deployments
      </label>
    </Sheet>
  );
}

const columns: Column<AwxAction>[] = [
  {
    id: "action",
    header: "Action",
    sortValue: (a) => a.name,
    cell: (a) => (
      <>
        <div className="font-medium">{a.name}</div>
        <div className="font-mono text-caption text-faint">{a.key}</div>
      </>
    ),
  },
  {
    id: "template",
    header: "AWX template",
    sortValue: (a) => a.template,
    cell: (a) => (
      <>
        <div className="font-mono text-secondary">{a.template}</div>
        <div className="text-caption text-faint">
          {a.workflow ? "Workflow" : "Job template"} #{a.template_id}
          {a.fields && a.fields.length > 0 ? `, asks for ${a.fields.join(", ")}` : ", no survey"}
        </div>
      </>
    ),
  },
  {
    id: "offered",
    header: "Offered on",
    cell: (a) => (
      <div className="text-secondary">
        <div>{a.types.map((t) => targetLabel[t]).join(", ")}</div>
        <div className="mt-0.5 font-mono text-caption text-muted">{a.condition || "Everywhere"}</div>
      </div>
    ),
  },
  {
    id: "status",
    header: "Status",
    cell: (a) => (
      <>
        <Status a={a} />
        {a.warnings.length > 0 && (
          <ul aria-label={`Warnings for ${a.name}`} className="mt-1 flex flex-col gap-0.5 text-secondary text-muted">
            {a.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        )}
      </>
    ),
  },
  {
    id: "synced",
    header: "Synced",
    align: "end",
    sortValue: (a) => a.synced_at,
    cell: (a) => (
      <span className="text-secondary text-muted" title={fullDate(a.synced_at)}>
        {relative(a.synced_at)}
      </span>
    ),
  },
];

export function Day2ActionsPage() {
  const qc = useQueryClient();
  const actions = useQuery(q.awxActions());
  const patch = usePatch();
  const [editing, setEditing] = useState<AwxAction | null>(null);
  const sync = useMutation({
    mutationFn: () => call(api.POST("/admin/awx/templates/sync")),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: keys.awxActions });
      void qc.invalidateQueries({ queryKey: keys.awxTemplates });
      toast.success("AWX templates synced");
    },
    onError: (e) => failed(e, "The sync failed"),
  });
  const syncButton = (
    <Button size="sm" disabled={sync.isPending} onClick={() => sync.mutate()}>
      {sync.isPending ? "Syncing…" : "Sync now"}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Day-2 actions"
        description={
          <>
            What people may run on a deployment or a VM besides the built-in actions. Who may run which is set by the{" "}
            <Link to="/admin/policies" search={{ tab: "day2" }} className="text-accent underline underline-offset-2">
              day-2 policies
            </Link>
            .
          </>
        }
      />
      <Panel
        title="From AWX"
        description="Templates of rvav's AWX organisation with the day2 label become actions here, every few minutes or when you sync. Their survey is the form people fill in."
        flush
        actions={actions.data?.items.length ? syncButton : undefined}
      >
        <DataTable
          label="AWX day-2 actions"
          columns={columns}
          rows={actions.data?.items}
          rowKey={(a) => a.key}
          minWidth={880}
          loading={actions.isPending}
          error={actions.isError && <ErrorState error={actions.error} onRetry={() => void actions.refetch()} />}
          empty={
            <EmptyState
              icon={Wrench}
              title="No AWX actions yet"
              body="Give a job template or workflow the day2 label in AWX, then sync: it becomes an action here."
              action={syncButton}
            />
          }
          rowMenuLabel={(a) => `Actions for ${a.name}`}
          rowMenu={(a) => (
            <>
              <MenuItem onSelect={() => setEditing(a)}>Change</MenuItem>
              <MenuSeparator />
              <MenuItem
                disabled={patch.isPending || (a.gone && !a.enabled)}
                onSelect={() =>
                  patch.mutate(
                    {
                      a,
                      body: { enabled: !a.enabled },
                      done: a.enabled ? `${a.name} is no longer offered` : `${a.name} is offered`,
                    },
                    { onError: (e) => failed(e, "Not saved") },
                  )
                }
              >
                {a.enabled ? "Stop offering" : "Offer on deployments"}
              </MenuItem>
            </>
          )}
        />
      </Panel>
      {editing && <ActionSheet a={editing} onClose={() => setEditing(null)} />}
    </Page>
  );
}
