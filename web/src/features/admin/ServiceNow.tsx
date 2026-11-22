// ServiceNow's integration (I1.5, ADR-0065): the token ServiceNow requests
// on people's behalf with, and the CIs rvav keeps in its CMDB.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { ExternalLink } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { CopyText, Time } from "@/components/common/misc";
import { KeyValues } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Panel, Section } from "@/components/ui/panel";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { fullDate, relative } from "@/lib/format";
import { adm } from "@/lib/query";
import { failed } from "./governance/common";

type S = components["schemas"];

export function ServiceNowToken() {
  const qc = useQueryClient();
  const q = useQuery(adm.serviceNowToken());
  const [asking, setAsking] = useState(false);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["admin", "servicenow-token"] });
  const make = useMutation({
    mutationFn: () => call(api.POST("/admin/integrations/servicenow/token")),
    onSuccess: () => {
      refresh();
      toast.success("ServiceNow's token is made");
    },
    onError: (e) => failed(e, "No token made"),
  });
  const revoke = useMutation({
    mutationFn: () => call(api.DELETE("/admin/integrations/servicenow/token")),
    onSuccess: () => {
      setAsking(false);
      make.reset();
      refresh();
      toast.success("ServiceNow's token is revoked");
    },
    onError: (e) => failed(e, "The token wasn't revoked"),
  });
  if (q.isPending) return <Skeleton className="h-40" />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const { token: t, allowed_sources: sources, requests_per_minute: perMinute } = q.data;
  const done = make.data;
  return (
    <Panel title="ServiceNow's token" actions={!t ? <Badge tone="warn">None made</Badge> : undefined}>
      <div className="flex max-w-3xl flex-col gap-3">
        <p className="text-body text-muted">
          ServiceNow calls rvav with an API token that only ServiceNow&apos;s endpoints take (
          <span className="font-mono">/api/v1/servicenow/</span>): it reads the catalog and requests on people&apos;s
          behalf what they may request themselves, nothing more. rvav makes it for a user of its own, valid a year;
          making a new one revokes the one before.
        </p>
        <p className="text-secondary">
          {t ? (
            <>
              Made <time dateTime={t.created_at}>{fullDate(t.created_at)}</time>
              {t.expires_at && (
                <>
                  , expires <time dateTime={t.expires_at}>{fullDate(t.expires_at)}</time>
                </>
              )}
              ; {t.last_used_at ? `ServiceNow last called rvav ${relative(t.last_used_at)}` : "not used yet"}.
            </>
          ) : (
            "None made: ServiceNow can't call rvav."
          )}
        </p>
        <p className="text-secondary text-muted">
          Taken from {sources.length ? <span className="font-mono">{sources.join(", ")}</span> : "any address"}, up to{" "}
          {perMinute} requests a minute: set them in the connection above.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button onClick={() => make.mutate()} disabled={make.isPending}>
            {make.isPending ? "Making…" : t ? "Make a new token" : "Make a token"}
          </Button>
          {t && (
            <Button variant="danger" onClick={() => setAsking(true)}>
              Revoke
            </Button>
          )}
        </div>
        {done && (
          <section
            aria-label="ServiceNow's token made"
            aria-live="polite"
            className="flex flex-col gap-1.5 rounded-md border border-line p-3 text-secondary"
          >
            <p>Put it in the API Key credential of ServiceNow&apos;s rvav alias. rvav shows it this once.</p>
            <CopyText value={done.secret} className="max-w-full" />
            {done.revoked ? <p>The token made before is revoked.</p> : null}
          </section>
        )}
      </div>
      <ConfirmDialog
        open={asking}
        onOpenChange={setAsking}
        title="Revoke ServiceNow's token?"
        description="ServiceNow can no longer call rvav: its catalog import, order forms and orders fail until you make a new token and put it in ServiceNow."
        consequences="Requests already made go on, and rvav still writes their progress to ServiceNow."
        confirmLabel="Revoke token"
        pending={revoke.isPending}
        onConfirm={() => revoke.mutate()}
      />
    </Panel>
  );
}

const failureColumns: Column<S["ServiceNowFailure"]>[] = [
  {
    id: "deployment",
    header: "Deployment",
    cell: (f) => (
      <Link
        to="/deployments/$deploymentId"
        params={{ deploymentId: f.deployment_id }}
        className="text-accent hover:underline"
      >
        {f.deployment_name || f.deployment_id}
      </Link>
    ),
  },
  { id: "error", header: "Why", cell: (f) => <span className="break-words">{f.error}</span> },
  { id: "attempts", header: "Attempts", cell: (f) => f.attempts, align: "end", className: "tnum" },
  { id: "next", header: "Next try", cell: (f) => <Time iso={f.next_attempt_at} /> },
];

const orphanColumns: Column<S["ServiceNowOrphan"]>[] = [
  {
    id: "name",
    header: "CI",
    cell: (o) => (
      <a
        href={o.url}
        target="_blank"
        rel="noreferrer"
        className="inline-flex items-center gap-1 text-accent underline underline-offset-2"
      >
        {o.name}
        <ExternalLink className="size-3" aria-hidden />
        <span className="sr-only">(opens in a new tab)</span>
      </a>
    ),
  },
  {
    id: "uuid",
    header: "Instance UUID",
    cell: (o) => <span className="font-mono text-secondary">{o.instance_uuid ?? "—"}</span>,
  },
  { id: "updated", header: "Changed in ServiceNow", cell: (o) => (o.updated_at ? `${o.updated_at} UTC` : "—") },
];

export function ServiceNowCmdb() {
  const qc = useQueryClient();
  const q = useQuery(adm.serviceNowCmdb());
  const reconcile = useMutation({
    mutationFn: () => call(api.POST("/admin/integrations/servicenow/cmdb/reconcile")),
    onSuccess: (r) => {
      qc.setQueryData(["admin", "servicenow-cmdb"], r);
      toast.success("rvav reconciles within seconds", { description: "Refresh the report in a minute." });
    },
    onError: (e) => failed(e, "No reconcile asked for"),
  });
  if (q.isPending) return <Skeleton className="h-48" />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const c = q.data;
  return (
    <Panel
      title="CMDB"
      description="Each VM of the projects chosen is a CI, made when built, updated after day-2 actions and drift, and retired when it goes."
      actions={
        <>
          {!c.enabled && <Badge tone="neutral">Off</Badge>}
          <Button variant="secondary" onClick={() => void q.refetch()} disabled={q.isFetching}>
            Refresh
          </Button>
          <Button onClick={() => reconcile.mutate()} disabled={!c.enabled || reconcile.isPending}>
            {reconcile.isPending ? "Asking…" : "Reconcile now"}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!c.enabled && (
          <p className="text-secondary text-muted">
            Tick Keep VMs as CIs in the connection above, and save, for rvav to keep VMs in ServiceNow&apos;s CMDB.
          </p>
        )}
        <KeyValues
          items={[
            { label: "CI class", value: <span className="font-mono text-secondary">{c.class}</span> },
            { label: "Discovery source", value: <span className="font-mono text-secondary">{c.data_source}</span> },
            {
              label: "Projects",
              value: c.projects.length ? c.projects.map((p) => p.name).join(", ") : "Every project",
            },
            { label: "CIs kept", value: <span className="tnum">{c.kept}</span> },
            { label: "Retired", value: <span className="tnum">{c.retired}</span> },
            {
              label: "Waiting",
              value: (
                <span className="tnum">
                  {c.queued} {c.queued === 1 ? "deployment" : "deployments"}
                  {c.failing > 0 && `, ${c.failing} refused and tried again`}
                </span>
              ),
            },
            {
              label: "Reconciled",
              value: c.reconciled_at ? (
                <>
                  <Time iso={c.reconciled_at} />
                  {c.next_reconcile_at && (
                    <>
                      , next <Time iso={c.next_reconcile_at} />
                    </>
                  )}
                  {c.servicenow !== undefined && <>: ServiceNow has {c.servicenow} CIs of this source, not retired</>}
                </>
              ) : (
                "Not yet"
              ),
            },
          ]}
        />
        {c.reconcile_error && (
          <p role="alert" className="text-secondary text-bad">
            ServiceNow&apos;s CIs couldn&apos;t be read: {c.reconcile_error}
          </p>
        )}
        {c.failures.length > 0 && (
          <Section title="Deployments ServiceNow refused" level={3}>
            <DataTable
              label="Deployments ServiceNow refused"
              columns={failureColumns}
              rows={c.failures}
              rowKey={(f) => f.deployment_id}
              minWidth={640}
            />
          </Section>
        )}
        {c.reconciled_at && (
          <Section title="CIs rvav no longer has" level={3}>
            <p className="text-secondary text-muted">
              {c.orphans_total === 0
                ? "None: every CI of this source that isn't retired is a VM rvav keeps."
                : `CIs of this source, not retired, with no VM in rvav${c.orphans_total > c.orphans.length ? ` (the first ${c.orphans.length} of ${c.orphans_total})` : ""}. rvav doesn't retire them: decide in ServiceNow.`}
            </p>
            {c.orphans.length > 0 && (
              <DataTable
                label="CIs rvav no longer has"
                columns={orphanColumns}
                rows={c.orphans}
                rowKey={(o) => o.sys_id}
                minWidth={640}
              />
            )}
          </Section>
        )}
      </div>
    </Panel>
  );
}
