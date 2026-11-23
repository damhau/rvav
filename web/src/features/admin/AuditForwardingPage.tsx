// Audit forwarding (O1.4, ADR-0058): the syslog servers and webhooks that
// get a copy of each audit event, how their delivery goes, and a test.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Send } from "lucide-react";
import type { TFunction } from "i18next";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { MenuItem, MenuSeparator } from "@/components/ui/dropdown-menu";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, FieldNote, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Select } from "@/components/ui/select";
import { Sheet } from "@/components/ui/sheet";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { can } from "@/lib/can";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { formatNumber, fullDate, relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";
import { errorAt, failed, fieldErrors } from "./governance/common";

type S = components["schemas"];
type Destination = S["AuditDestination"];
type Category = S["AuditCategory"];
type Transport = S["SyslogTransport"];
type Facility = S["SyslogFacility"];

const allCategories: Category[] = [
  "sign_in",
  "token",
  "access",
  "catalog",
  "policy",
  "infrastructure",
  "extensibility",
  "settings",
  "request",
  "other",
];
const categoryLabel = (c: Category) => i18n.t(`admin:auditForwarding.category.${c}`);

const transports: Transport[] = ["tls", "tcp", "udp"];
const facilities: Facility[] = ["kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news", "uucp", "cron", "authpriv", "ftp", "ntp", "audit", "alert", "clock", "local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"]; // prettier-ignore

const hostExample = "siem.corp.example";
const urlExample = "https://siem.corp.example/rvav";
const headerExample = "Authorization";
const pemExample = "-----BEGIN CERTIFICATE-----";

/** No stored secret: Radix's Select can't take an empty value, and no secret's name has brackets. */
const none = "(none)";

const invalidate = (qc: ReturnType<typeof useQueryClient>) =>
  void qc.invalidateQueries({ queryKey: ["admin", "audit-destinations"] });

interface Draft {
  name: string;
  enabled: boolean;
  categories: Category[];
  kind: Destination["kind"];
  host: string;
  port: string;
  transport: Transport;
  facility: Facility;
  appName: string;
  ca: string;
  cert: string;
  keySecret: string;
  url: string;
  secret: string;
  authHeader: string;
  authValue: string;
}

const draftOf = (d?: Destination): Draft => ({
  name: d?.name ?? "",
  enabled: d?.enabled ?? true,
  categories: d?.categories ?? [],
  kind: d?.kind ?? "syslog",
  host: d?.syslog?.host ?? "",
  port: d?.syslog?.port ? String(d.syslog.port) : "",
  transport: d?.syslog?.transport ?? "tls",
  facility: d?.syslog?.facility ?? "local0",
  appName: d?.syslog?.app_name ?? "",
  ca: d?.syslog?.ca_certificate ?? "",
  cert: d?.syslog?.client_certificate ?? "",
  keySecret: d?.syslog?.client_key_secret ?? none,
  url: d?.webhook?.url ?? "",
  secret: d?.webhook?.secret ?? "",
  authHeader: d?.webhook?.auth_header ?? "",
  authValue: "",
});

function bodyOf(d: Draft): S["AuditDestinationWrite"] {
  const head = { name: d.name, enabled: d.enabled, categories: d.categories };
  if (d.kind === "webhook") {
    const header = d.authHeader.trim();
    return {
      ...head,
      webhook: {
        url: d.url,
        secret: d.secret,
        ...(header ? { auth_header: header } : {}),
        ...(d.authValue ? { auth_value: d.authValue } : {}),
      },
    };
  }
  const tls = d.transport === "tls";
  return {
    ...head,
    syslog: {
      host: d.host,
      ...(d.port.trim() ? { port: Number(d.port) } : {}),
      transport: d.transport,
      facility: d.facility,
      ...(d.appName.trim() ? { app_name: d.appName.trim() } : {}),
      ...(tls && d.ca.trim() ? { ca_certificate: d.ca } : {}),
      ...(tls && d.cert.trim() ? { client_certificate: d.cert } : {}),
      ...(tls && d.keySecret !== none ? { client_key_secret: d.keySecret } : {}),
    },
  };
}

function DestinationSheet({ dest, onClose }: { dest?: Destination; onClose: () => void }) {
  const { t } = useTranslation(["admin", "common"]);
  const qc = useQueryClient();
  const secrets = useQuery(adm.secrets());
  const [d, setD] = useState(() => draftOf(dest));
  const [errors, setErrors] = useState<Record<string, string>>({});
  const set = (patch: Partial<Draft>) => setD({ ...d, ...patch });
  const stored = (secrets.data?.items ?? [])
    .filter((s) => !s.project_id)
    .map((s) => ({ value: s.name, label: s.name }));
  const save = useMutation({
    mutationFn: () =>
      dest
        ? call(api.PUT("/admin/audit-destinations/{id}", { params: { path: { id: dest.id } }, body: bodyOf(d) }))
        : call(api.POST("/admin/audit-destinations", { body: bodyOf(d) })),
    onSuccess: (saved) => {
      invalidate(qc);
      toast.success(t(dest ? "auditForwarding.saved" : "auditForwarding.created", { name: saved.name }));
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.notSaved"));
    },
  });
  const tls = d.transport === "tls";
  const kept = Boolean(dest?.webhook?.auth_header) && d.authHeader.trim() !== "";
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={dest ? t("auditForwarding.change", { name: dest.name }) : t("auditForwarding.new")}
      description={t("auditForwarding.sheetDescription")}
      submitLabel={
        save.isPending ? t("integrations.saving") : t(dest ? "auditForwarding.save" : "auditForwarding.create")
      }
      onSubmit={() => {
        if (d.kind === "syslog" && d.port.trim() && !/^\d{1,5}$/.test(d.port.trim())) {
          setErrors({ "/syslog/port": t("auditForwarding.portRange") });
          return;
        }
        setErrors({});
        save.mutate();
      }}
    >
      <Field label={t("auditForwarding.name")} htmlFor="adt-name" error={errors["/name"]}>
        <Input id="adt-name" value={d.name} onChange={(e) => set({ name: e.target.value })} />
      </Field>
      <Field label={t("auditForwarding.kind")} htmlFor="adt-kind" error={errors["/syslog"]}>
        <Select
          id="adt-kind"
          value={d.kind}
          onChange={(v) => set({ kind: v as Draft["kind"] })}
          options={[
            { value: "syslog", label: t("auditForwarding.syslog") },
            { value: "webhook", label: t("auditForwarding.webhook") },
          ]}
        />
      </Field>
      {d.kind === "syslog" ? (
        <>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_120px]">
            <Field label={t("auditForwarding.host")} htmlFor="adt-host" error={errors["/syslog/host"]}>
              <Input
                id="adt-host"
                className="font-mono text-secondary"
                placeholder={hostExample}
                value={d.host}
                onChange={(e) => set({ host: e.target.value })}
              />
            </Field>
            <Field label={t("auditForwarding.port")} htmlFor="adt-port" error={errors["/syslog/port"]}>
              <Input
                id="adt-port"
                inputMode="numeric"
                className="font-mono text-secondary"
                placeholder={tls ? "6514" : "514"}
                value={d.port}
                onChange={(e) => set({ port: e.target.value })}
              />
            </Field>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field
              label={t("auditForwarding.transportLabel")}
              htmlFor="adt-transport"
              error={errors["/syslog/transport"]}
            >
              <Select
                id="adt-transport"
                value={d.transport}
                onChange={(v) => set({ transport: v as Transport })}
                options={transports.map((value) => ({ value, label: t(`auditForwarding.transport.${value}`) }))}
              />
            </Field>
            <Field label={t("auditForwarding.facility")} htmlFor="adt-facility" error={errors["/syslog/facility"]}>
              <Select
                id="adt-facility"
                value={d.facility}
                onChange={(v) => set({ facility: v as Facility })}
                options={facilities.map((f) => ({ value: f, label: f }))}
              />
            </Field>
          </div>
          <Field
            label={t("auditForwarding.appName")}
            htmlFor="adt-app"
            error={errors["/syslog/app_name"]}
            hint={t("auditForwarding.appNameHint")}
          >
            <Input
              id="adt-app"
              className="font-mono text-secondary"
              placeholder="rvav"
              value={d.appName}
              onChange={(e) => set({ appName: e.target.value })}
            />
          </Field>
          {tls && (
            <>
              <Field
                label={t("auditForwarding.ca")}
                htmlFor="adt-ca"
                error={errors["/syslog/ca_certificate"]}
                hint={t("auditForwarding.caHint")}
              >
                <Textarea
                  id="adt-ca"
                  rows={3}
                  className="font-mono text-secondary"
                  placeholder={pemExample}
                  value={d.ca}
                  onChange={(e) => set({ ca: e.target.value })}
                />
              </Field>
              <Field
                label={t("auditForwarding.cert")}
                htmlFor="adt-cert"
                error={errors["/syslog/client_certificate"]}
                hint={t("auditForwarding.certHint")}
              >
                <Textarea
                  id="adt-cert"
                  rows={3}
                  className="font-mono text-secondary"
                  placeholder={pemExample}
                  value={d.cert}
                  onChange={(e) => set({ cert: e.target.value })}
                />
              </Field>
              <Field
                label={t("auditForwarding.key")}
                htmlFor="adt-key"
                error={errors["/syslog/client_key_secret"]}
                hint={t("auditForwarding.keyHint")}
              >
                <Select
                  id="adt-key"
                  value={d.keySecret}
                  loading={secrets.isPending}
                  onChange={(keySecret) => set({ keySecret })}
                  options={[{ value: none, label: t("auditForwarding.none") }, ...stored]}
                />
              </Field>
            </>
          )}
        </>
      ) : (
        <>
          <Field
            label={t("auditForwarding.url")}
            htmlFor="adt-url"
            error={errors["/webhook/url"]}
            hint={t("auditForwarding.urlHint")}
          >
            <Input
              id="adt-url"
              className="font-mono text-secondary"
              placeholder={urlExample}
              value={d.url}
              onChange={(e) => set({ url: e.target.value })}
            />
          </Field>
          <Field
            label={t("auditForwarding.secret")}
            htmlFor="adt-secret"
            error={errors["/webhook/secret"]}
            hint={t("auditForwarding.secretHint")}
          >
            <Select
              id="adt-secret"
              value={d.secret}
              placeholder={t("auditForwarding.choose")}
              loading={secrets.isPending}
              onChange={(secret) => set({ secret })}
              options={stored}
            />
          </Field>
          <Field
            label={t("auditForwarding.authHeader")}
            htmlFor="adt-auth-header"
            error={errors["/webhook/auth_header"]}
            hint={t("auditForwarding.authHeaderHint")}
          >
            <Input
              id="adt-auth-header"
              className="font-mono text-secondary"
              placeholder={headerExample}
              value={d.authHeader}
              onChange={(e) => set({ authHeader: e.target.value })}
            />
          </Field>
          <Field
            label={t("auditForwarding.authValue")}
            htmlFor="adt-auth-value"
            error={errors["/webhook/auth_value"]}
            hint={t(kept ? "auditForwarding.authValueKept" : "auditForwarding.authValueHint")}
          >
            <Input
              id="adt-auth-value"
              type="password"
              autoComplete="new-password"
              placeholder={kept ? "••••••••" : undefined}
              value={d.authValue}
              onChange={(e) => set({ authValue: e.target.value })}
            />
          </Field>
        </>
      )}
      <fieldset className="flex flex-col gap-2" aria-describedby="adt-events-note">
        <legend className="mb-1 text-secondary font-medium">{t("auditForwarding.events")}</legend>
        <div className="grid grid-cols-1 gap-x-4 gap-y-1.5 sm:grid-cols-2">
          {allCategories.map((c) => (
            <label key={c} htmlFor={`adt-cat-${c}`} className="flex items-center gap-1.5 text-body">
              <Checkbox
                id={`adt-cat-${c}`}
                checked={d.categories.includes(c)}
                onChange={(on) => set({ categories: on ? [...d.categories, c] : d.categories.filter((x) => x !== c) })}
              />
              {categoryLabel(c)}
            </label>
          ))}
        </div>
        <FieldNote id="adt-events-note" error={errorAt(errors, "/categories")} hint={t("auditForwarding.eventsHint")} />
      </fieldset>
      <label htmlFor="adt-enabled" className="flex items-center gap-1.5 text-body">
        <Checkbox id="adt-enabled" checked={d.enabled} onChange={(enabled) => set({ enabled })} />
        {t("auditForwarding.send")}
      </label>
    </Sheet>
  );
}

function TestDialog({ name, test, onClose }: { name: string; test: S["AuditDestinationTest"]; onClose: () => void }) {
  const { t } = useTranslation("admin");
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={t("auditForwarding.testOf", { name })}>
        <div className="flex flex-col gap-3 p-4" aria-live="polite">
          <p className="flex items-start gap-1.5 text-body break-words">
            {test.ok ? (
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden />
            ) : (
              <CircleX className="mt-0.5 size-4 shrink-0 text-bad" aria-hidden />
            )}
            <span>
              <Trans
                t={t}
                i18nKey={test.ok ? "auditForwarding.deliveredIn" : "auditForwarding.notDeliveredIn"}
                values={{ ms: test.duration_ms }}
                components={{ status: <span className="font-medium" /> }}
              />{" "}
              {test.message}
            </span>
          </p>
          <p className="text-secondary text-muted">{t("auditForwarding.testNote")}</p>
        </div>
      </DialogContent>
    </Dialog>
  );
}

const where = (t: TFunction<"admin">, d: Destination) =>
  d.syslog ? (
    <>
      <div className="font-mono text-secondary break-all">
        {d.syslog.host}:{d.syslog.port}
      </div>
      <div className="text-secondary text-muted">
        {(d.syslog.transport ?? "tls").toUpperCase()} · {d.syslog.facility} · {d.syslog.app_name}
        {d.syslog.client_certificate ? t("auditForwarding.clientCert") : ""}
      </div>
    </>
  ) : (
    <>
      <div className="font-mono text-secondary break-all">{d.webhook?.url}</div>
      <div className="text-secondary text-muted">
        {t("auditForwarding.signedWith", { secret: d.webhook?.secret })}
        {d.webhook?.auth_header ? t("auditForwarding.sendsHeader", { header: d.webhook.auth_header }) : ""}
      </div>
    </>
  );

function Delivery({ d }: { d: Destination }) {
  const { t } = useTranslation("admin");
  const s = d.state;
  const when = (iso: string) => <span title={fullDate(iso)}>{relative(iso)}</span>;
  const at = (iso: string) => ({
    values: { when: relative(iso) },
    components: { when: <span title={fullDate(iso)} /> },
  });
  return (
    <div className="flex flex-col gap-0.5 text-secondary">
      <div className="flex flex-wrap items-center gap-1.5">
        {!d.enabled ? (
          <Badge tone="neutral">{t("auditForwarding.off")}</Badge>
        ) : s.failures > 0 ? (
          <Badge tone="bad">{t("auditForwarding.failing")}</Badge>
        ) : s.backlog > 0 ? (
          <Badge tone="run">{t("auditForwarding.sending")}</Badge>
        ) : (
          <Badge tone="ok">{t("auditForwarding.upToDate")}</Badge>
        )}
        <span>
          {s.backlog > 0 ? t("auditForwarding.waiting", { n: formatNumber(s.backlog) }) : ""}
          {t("auditForwarding.delivered", { n: formatNumber(s.delivered) })}
        </span>
      </div>
      {s.failures > 0 && s.last_error && (
        <div className="break-words text-bad">
          {s.last_error}
          {s.last_error_at && <> ({when(s.last_error_at)})</>}
        </div>
      )}
      <div className="text-muted">
        {s.next_attempt_at && d.enabled && <Trans t={t} i18nKey="auditForwarding.nextTry" {...at(s.next_attempt_at)} />}
        {s.last_success_at ? (
          <Trans t={t} i18nKey="auditForwarding.lastDelivered" {...at(s.last_success_at)} />
        ) : (
          t("auditForwarding.nothingDelivered")
        )}
      </div>
    </div>
  );
}

const columns = (t: TFunction<"admin">): Column<Destination>[] => [
  {
    id: "name",
    header: t("auditForwarding.destination"),
    sortValue: (d) => d.name,
    cell: (d) => (
      <>
        <div className="font-medium">{d.name}</div>
        <div className="text-secondary text-muted">
          {d.kind === "syslog" ? t("auditForwarding.syslog") : t("auditForwarding.webhook")}
        </div>
      </>
    ),
  },
  { id: "where", header: t("auditForwarding.where"), cell: (d) => where(t, d) },
  {
    id: "events",
    header: t("auditForwarding.events"),
    cell: (d) => (
      <span className="text-secondary">
        {d.categories.length ? d.categories.map(categoryLabel).join(", ") : t("auditForwarding.everyEvent")}
      </span>
    ),
  },
  { id: "delivery", header: t("auditForwarding.delivery"), cell: (d) => <Delivery d={d} /> },
];

export function AuditForwardingPage() {
  const { t } = useTranslation(["admin", "shell"]);
  const qc = useQueryClient();
  const { me } = useCurrentProject();
  const canEdit = can(me, "integrations.manage");
  const list = useQuery({ ...adm.auditDestinations(), refetchInterval: 15_000 });
  const [editing, setEditing] = useState<Destination | "new" | null>(null);
  const [removing, setRemoving] = useState<Destination | null>(null);
  const [tested, setTested] = useState<{ name: string; test: S["AuditDestinationTest"] } | null>(null);
  const test = useMutation({
    mutationFn: (d: Destination) =>
      call(api.POST("/admin/audit-destinations/{id}/test", { params: { path: { id: d.id } } })),
    onSuccess: (t, d) => setTested({ name: d.name, test: t }),
    onError: (e) => failed(e, t("integrations.testDidntRun")),
  });
  const toggle = useMutation({
    mutationFn: (d: Destination) =>
      call(
        api.PUT("/admin/audit-destinations/{id}", {
          params: { path: { id: d.id } },
          body: { ...bodyOf(draftOf(d)), enabled: !d.enabled },
        }),
      ),
    onSuccess: (d) => {
      invalidate(qc);
      toast.success(
        d.enabled ? t("auditForwarding.isOn", { name: d.name }) : t("auditForwarding.isOff", { name: d.name }),
      );
    },
    onError: (e) => failed(e, t("integrations.notSaved")),
  });
  const remove = useMutation({
    mutationFn: (d: Destination) =>
      call(api.DELETE("/admin/audit-destinations/{id}", { params: { path: { id: d.id } } })),
    onSuccess: (_, d) => {
      invalidate(qc);
      toast.success(t("auditForwarding.removed", { name: d.name }));
      setRemoving(null);
    },
    onError: (e) => failed(e, t("vcenters.notRemoved")),
  });
  const create = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      {t("auditForwarding.new")}
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title={t("shell:nav.admin.auditForwarding")}
        description={t("auditForwarding.description")}
        actions={list.data?.items.length ? create : undefined}
      />
      <Panel title={t("auditForwarding.destinations")} flush>
        <DataTable
          label={t("auditForwarding.destinations")}
          columns={columns(t)}
          rows={list.data?.items}
          rowKey={(d) => d.id}
          minWidth={860}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          empty={
            <EmptyState
              icon={Send}
              title={t("auditForwarding.emptyTitle")}
              body={t("auditForwarding.emptyBody")}
              action={create || undefined}
            />
          }
          rowMenuLabel={(d) => t("auditForwarding.actionsFor", { name: d.name })}
          rowMenu={
            canEdit
              ? (d) => (
                  <>
                    <MenuItem disabled={test.isPending} onSelect={() => test.mutate(d)}>
                      {t("auditForwarding.sendTest")}
                    </MenuItem>
                    <MenuItem onSelect={() => setEditing(d)}>{t("auditForwarding.changeItem")}</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(d)}>
                      {d.enabled ? t("auditForwarding.turnOff") : t("auditForwarding.turnOn")}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(d)}>
                      {t("auditForwarding.remove")}
                    </MenuItem>
                  </>
                )
              : undefined
          }
        />
      </Panel>
      {editing && <DestinationSheet dest={editing === "new" ? undefined : editing} onClose={() => setEditing(null)} />}
      {tested && <TestDialog name={tested.name} test={tested.test} onClose={() => setTested(null)} />}
      {removing && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setRemoving(null)}
          title={t("auditForwarding.stopTitle", { name: removing.name })}
          description={t("auditForwarding.stopDescription")}
          consequences={
            removing.state.backlog > 0
              ? t("auditForwarding.dropped", {
                  count: removing.state.backlog,
                  n: formatNumber(removing.state.backlog),
                })
              : t("auditForwarding.keepsAll")
          }
          confirmLabel={t("auditForwarding.removeDestination")}
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Page>
  );
}
