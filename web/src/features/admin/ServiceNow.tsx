// ServiceNow's integration (I1.5, ADR-0065): the token ServiceNow requests
// on people's behalf with, and the CIs rvav keeps in its CMDB.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { ExternalLink } from "lucide-react";
import { useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
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
import { formatNumber, fullDate, relative } from "@/lib/format";
import { adm } from "@/lib/query";
import { failed } from "./governance/common";

type S = components["schemas"];

export function ServiceNowToken() {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const q = useQuery(adm.serviceNowToken());
  const [asking, setAsking] = useState(false);
  const [renewing, setRenewing] = useState(false);
  const makeButton = useRef<HTMLButtonElement>(null);
  const refresh = () => void qc.invalidateQueries({ queryKey: ["admin", "servicenow-token"] });
  const make = useMutation({
    mutationFn: () => call(api.POST("/admin/integrations/servicenow/token")),
    onSuccess: () => {
      setRenewing(false);
      refresh();
      toast.success(t("serviceNow.token.madeToast"));
    },
    onError: (e) => failed(e, t("serviceNow.token.notMade")),
  });
  const revoke = useMutation({
    mutationFn: () => call(api.DELETE("/admin/integrations/servicenow/token")),
    onSuccess: () => {
      setAsking(false);
      make.reset();
      refresh();
      toast.success(t("serviceNow.token.revokedToast"));
    },
    onError: (e) => failed(e, t("serviceNow.token.notRevoked")),
  });
  if (q.isPending) return <Skeleton className="h-40" />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const { token, allowed_sources: sources, requests_per_minute: perMinute } = q.data;
  const done = make.data;
  const used = token?.last_used_at
    ? t("serviceNow.token.lastCalled", { when: relative(token.last_used_at) })
    : t("serviceNow.token.notUsed");
  return (
    <Panel
      title={t("serviceNow.token.title")}
      actions={!token ? <Badge tone="warn">{t("serviceNow.token.noneMade")}</Badge> : undefined}
    >
      <div className="flex max-w-3xl flex-col gap-3">
        <p className="text-body text-muted">
          <Trans t={t} i18nKey="serviceNow.token.about" components={{ path: <span className="font-mono" /> }} />
        </p>
        <p className="text-secondary">
          {token ? (
            <Trans
              t={t}
              i18nKey={token.expires_at ? "serviceNow.token.madeExpires" : "serviceNow.token.made"}
              values={{
                made: fullDate(token.created_at),
                expires: token.expires_at ? fullDate(token.expires_at) : "",
                used,
              }}
              components={{
                made: <time dateTime={token.created_at} />,
                expires: <time dateTime={token.expires_at} />,
              }}
            />
          ) : (
            t("serviceNow.token.none")
          )}
        </p>
        <p className="text-secondary text-muted">
          {sources.length ? (
            <Trans
              t={t}
              i18nKey="serviceNow.token.takenFrom"
              count={perMinute}
              values={{ sources: sources.join(", ") }}
              components={{ sources: <span className="font-mono" /> }}
            />
          ) : (
            t("serviceNow.token.takenAny", { count: perMinute })
          )}
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            ref={makeButton}
            onClick={() => (token ? setRenewing(true) : make.mutate())}
            disabled={make.isPending}
          >
            {make.isPending
              ? t("serviceNow.token.making")
              : token
                ? t("serviceNow.token.makeNew")
                : t("serviceNow.token.make")}
          </Button>
          {token && (
            <Button variant="danger" onClick={() => setAsking(true)}>
              {t("serviceNow.token.revoke")}
            </Button>
          )}
        </div>
        {/* Live before the token comes, for it to be read out as it does. */}
        <div aria-live="polite">
          {done && (
            <section
              aria-label={t("serviceNow.token.madeLabel")}
              className="flex flex-col gap-1.5 rounded-md border border-line p-3 text-secondary"
            >
              <p>{t("serviceNow.token.putIt")}</p>
              <CopyText value={done.secret} className="max-w-full" />
              {done.revoked ? <p>{t("serviceNow.token.revoked")}</p> : null}
            </section>
          )}
        </div>
      </div>
      <ConfirmDialog
        open={renewing}
        onOpenChange={setRenewing}
        title={t("serviceNow.token.renewTitle")}
        description={t("serviceNow.token.renewDescription")}
        consequences={t("serviceNow.token.renewConsequences")}
        confirmLabel={t("serviceNow.token.makeNew")}
        pending={make.isPending}
        onConfirm={() => make.mutate()}
      />
      <ConfirmDialog
        open={asking}
        onOpenChange={setAsking}
        title={t("serviceNow.token.revokeTitle")}
        description={t("serviceNow.token.revokeDescription")}
        consequences={t("serviceNow.token.revokeConsequences")}
        confirmLabel={t("serviceNow.token.revokeConfirm")}
        pending={revoke.isPending}
        onConfirm={() => revoke.mutate()}
        returnFocus={() => makeButton.current}
      />
    </Panel>
  );
}

const failureColumns = (t: TFunction<"admin">): Column<S["ServiceNowFailure"]>[] => [
  {
    id: "deployment",
    header: t("serviceNow.cmdb.deployment"),
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
  { id: "error", header: t("serviceNow.cmdb.why"), cell: (f) => <span className="break-words">{f.error}</span> },
  { id: "attempts", header: t("serviceNow.cmdb.attempts"), cell: (f) => f.attempts, align: "end", className: "tnum" },
  { id: "next", header: t("serviceNow.cmdb.nextTry"), cell: (f) => <Time iso={f.next_attempt_at} /> },
];

const orphanColumns = (t: TFunction<"admin">): Column<S["ServiceNowOrphan"]>[] => [
  {
    id: "name",
    header: t("serviceNow.cmdb.ci"),
    cell: (o) => (
      <a
        href={o.url}
        target="_blank"
        rel="noreferrer"
        className="inline-flex items-center gap-1 text-accent underline underline-offset-2"
      >
        {o.name}
        <ExternalLink className="size-3" aria-hidden />
        <span className="sr-only">{t("serviceNow.cmdb.newTab")}</span>
      </a>
    ),
  },
  {
    id: "uuid",
    header: t("serviceNow.cmdb.uuid"),
    cell: (o) => <span className="font-mono text-secondary">{o.instance_uuid ?? "—"}</span>,
  },
  {
    id: "updated",
    header: t("serviceNow.cmdb.changed"),
    cell: (o) => <Changed at={o.updated_at} />,
  },
];

/** A time as ServiceNow writes it, "2026-11-22 14:05:31" in UTC, shown as the page's other times. */
function Changed({ at }: { at?: string }) {
  if (!at) return "—";
  if (!/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(at)) return at;
  return <Time iso={`${at.replace(" ", "T")}Z`} />;
}

export function ServiceNowCmdb() {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const q = useQuery(adm.serviceNowCmdb());
  const reconcile = useMutation({
    mutationFn: () => call(api.POST("/admin/integrations/servicenow/cmdb/reconcile")),
    onSuccess: (r) => {
      qc.setQueryData(["admin", "servicenow-cmdb"], r);
      toast.success(t("serviceNow.cmdb.reconcilingToast"), { description: t("serviceNow.cmdb.reconcilingHint") });
    },
    onError: (e) => failed(e, t("serviceNow.cmdb.notAsked")),
  });
  if (q.isPending) return <Skeleton className="h-48" />;
  if (q.isError) return <ErrorState error={q.error} onRetry={() => void q.refetch()} />;
  const c = q.data;
  return (
    <Panel
      title={t("serviceNow.cmdb.title")}
      description={t("serviceNow.cmdb.description")}
      actions={
        <>
          {!c.enabled && <Badge tone="neutral">{t("serviceNow.cmdb.off")}</Badge>}
          <Button variant="secondary" onClick={() => void q.refetch()} disabled={q.isFetching}>
            {t("serviceNow.cmdb.refresh")}
          </Button>
          <Button onClick={() => reconcile.mutate()} disabled={!c.enabled || reconcile.isPending}>
            {reconcile.isPending ? t("serviceNow.cmdb.asking") : t("serviceNow.cmdb.reconcile")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        {!c.enabled && <p className="text-secondary text-muted">{t("serviceNow.cmdb.tick")}</p>}
        <KeyValues
          items={[
            { label: t("serviceNow.cmdb.class"), value: <span className="font-mono text-secondary">{c.class}</span> },
            {
              label: t("serviceNow.cmdb.source"),
              value: <span className="font-mono text-secondary">{c.data_source}</span>,
            },
            {
              label: t("serviceNow.cmdb.projects"),
              value: c.projects.length ? c.projects.map((p) => p.name).join(", ") : t("serviceNow.cmdb.everyProject"),
            },
            { label: t("serviceNow.cmdb.kept"), value: <span className="tnum">{c.kept}</span> },
            { label: t("serviceNow.cmdb.retired"), value: <span className="tnum">{c.retired}</span> },
            {
              label: t("serviceNow.cmdb.waiting"),
              value: (
                <span className="tnum">
                  {t("serviceNow.cmdb.deployments", { count: c.queued })}
                  {c.failing > 0 && t("serviceNow.cmdb.refusedAgain", { count: c.failing })}
                </span>
              ),
            },
            {
              label: t("serviceNow.cmdb.reconciled"),
              value: c.reconciled_at ? (
                <>
                  <Time iso={c.reconciled_at} />
                  {c.next_reconcile_at && (
                    <>
                      {t("serviceNow.cmdb.next")}
                      <Time iso={c.next_reconcile_at} />
                    </>
                  )}
                  {c.servicenow !== undefined && t("serviceNow.cmdb.servicenowHas", { count: c.servicenow })}
                </>
              ) : (
                t("serviceNow.cmdb.notYet")
              ),
            },
          ]}
        />
        {c.reconcile_error && (
          <p role="alert" className="text-secondary text-bad">
            {t("serviceNow.cmdb.readFailed", { error: c.reconcile_error })}
          </p>
        )}
        {c.failures.length > 0 && (
          <Section title={t("serviceNow.cmdb.refused")} level={3}>
            <DataTable
              label={t("serviceNow.cmdb.refused")}
              columns={failureColumns(t)}
              rows={c.failures}
              rowKey={(f) => f.deployment_id}
              minWidth={640}
            />
          </Section>
        )}
        {c.reconciled_at && !c.reconcile_error && (
          <Section title={t("serviceNow.cmdb.orphans")} level={3}>
            <p className="text-secondary text-muted">
              {c.orphans_total === 0
                ? t("serviceNow.cmdb.orphansNone")
                : c.orphans_total > c.orphans.length
                  ? t("serviceNow.cmdb.orphansFirst", {
                      shown: formatNumber(c.orphans.length),
                      total: formatNumber(c.orphans_total),
                    })
                  : t("serviceNow.cmdb.orphansAll")}
            </p>
            {c.orphans.length > 0 && (
              <DataTable
                label={t("serviceNow.cmdb.orphans")}
                columns={orphanColumns(t)}
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
