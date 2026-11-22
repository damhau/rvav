// Audit forwarding (O1.4, ADR-0058): the syslog servers and webhooks that
// get a copy of each audit event, how their delivery goes, and a test.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Send } from "lucide-react";
import { useId, useState } from "react";
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
import { errorAt, failed, fieldErrors } from "./governance/common";

type S = components["schemas"];
type Destination = S["AuditDestination"];
type Category = S["AuditCategory"];
type Transport = S["SyslogTransport"];
type Facility = S["SyslogFacility"];

const categoryLabels: Record<Category, string> = {
  sign_in: "Sign-ins and sign-outs",
  token: "API tokens",
  access: "Access: projects, members, roles",
  catalog: "Catalog",
  policy: "Policies and naming",
  infrastructure: "Infrastructure",
  extensibility: "Extensibility",
  settings: "Settings, secrets, integrations",
  request: "Requests, deployments, approvals",
  other: "Anything else",
};
const allCategories = Object.keys(categoryLabels) as Category[];

const transports: { value: Transport; label: string }[] = [
  { value: "tls", label: "TLS (RFC 5425)" },
  { value: "tcp", label: "TCP in clear, for labs" },
  { value: "udp", label: "UDP, for labs" },
];
const facilities: Facility[] = ["kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news", "uucp", "cron", "authpriv", "ftp", "ntp", "audit", "alert", "clock", "local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7"]; // prettier-ignore

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
});

function bodyOf(d: Draft): S["AuditDestinationWrite"] {
  const head = { name: d.name, enabled: d.enabled, categories: d.categories };
  if (d.kind === "webhook") return { ...head, webhook: { url: d.url, secret: d.secret } };
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
  const qc = useQueryClient();
  const formId = useId();
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
      toast.success(`${saved.name} saved`);
      onClose();
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const tls = d.transport === "tls";
  return (
    <Sheet
      open
      onOpenChange={(o) => !o && onClose()}
      pending={save.isPending}
      title={dest ? `Change ${dest.name}` : "New destination"}
      description="rvav sends it each audit event written from now on, in order, and keeps those it couldn't send until it can."
      footer={
        <>
          <Button type="button" variant="secondary" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save destination"}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (d.kind === "syslog" && d.port.trim() && !/^\d{1,5}$/.test(d.port.trim())) {
            setErrors({ "/syslog/port": "A port from 1 to 65535." });
            return;
          }
          setErrors({});
          save.mutate();
        }}
      >
        <Field label="Name" htmlFor="adt-name" error={errors["/name"]}>
          <Input id="adt-name" value={d.name} onChange={(e) => set({ name: e.target.value })} />
        </Field>
        <Field label="Kind" htmlFor="adt-kind" error={errors["/syslog"]}>
          <Select
            id="adt-kind"
            value={d.kind}
            onChange={(v) => set({ kind: v as Draft["kind"] })}
            options={[
              { value: "syslog", label: "Syslog server" },
              { value: "webhook", label: "Webhook" },
            ]}
          />
        </Field>
        {d.kind === "syslog" ? (
          <>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_120px]">
              <Field label="Host" htmlFor="adt-host" error={errors["/syslog/host"]}>
                <Input
                  id="adt-host"
                  className="font-mono text-secondary"
                  placeholder="siem.corp.example"
                  value={d.host}
                  onChange={(e) => set({ host: e.target.value })}
                />
              </Field>
              <Field label="Port" htmlFor="adt-port" error={errors["/syslog/port"]}>
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
              <Field label="Transport" htmlFor="adt-transport" error={errors["/syslog/transport"]}>
                <Select
                  id="adt-transport"
                  value={d.transport}
                  onChange={(v) => set({ transport: v as Transport })}
                  options={transports}
                />
              </Field>
              <Field label="Facility" htmlFor="adt-facility" error={errors["/syslog/facility"]}>
                <Select
                  id="adt-facility"
                  value={d.facility}
                  onChange={(v) => set({ facility: v as Facility })}
                  options={facilities.map((f) => ({ value: f, label: f }))}
                />
              </Field>
            </div>
            <Field
              label="App name"
              htmlFor="adt-app"
              error={errors["/syslog/app_name"]}
              hint="The APP-NAME of each message, rvav when empty"
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
                  label="CA certificates"
                  htmlFor="adt-ca"
                  error={errors["/syslog/ca_certificate"]}
                  hint="PEM. The CAs that sign the server's certificate, besides the system's and rvav's CA bundle"
                >
                  <Textarea
                    id="adt-ca"
                    rows={3}
                    className="font-mono text-secondary"
                    placeholder="-----BEGIN CERTIFICATE-----"
                    value={d.ca}
                    onChange={(e) => set({ ca: e.target.value })}
                  />
                </Field>
                <Field
                  label="Client certificate"
                  htmlFor="adt-cert"
                  error={errors["/syslog/client_certificate"]}
                  hint="PEM. What rvav presents when the server asks for a certificate, with its key below"
                >
                  <Textarea
                    id="adt-cert"
                    rows={3}
                    className="font-mono text-secondary"
                    placeholder="-----BEGIN CERTIFICATE-----"
                    value={d.cert}
                    onChange={(e) => set({ cert: e.target.value })}
                  />
                </Field>
                <Field
                  label="Client key"
                  htmlFor="adt-key"
                  error={errors["/syslog/client_key_secret"]}
                  hint="The stored secret that holds the key in PEM (Catalog items › Secrets)"
                >
                  <Select
                    id="adt-key"
                    value={d.keySecret}
                    loading={secrets.isPending}
                    onChange={(keySecret) => set({ keySecret })}
                    options={[{ value: none, label: "None" }, ...stored]}
                  />
                </Field>
              </>
            )}
          </>
        ) : (
          <>
            <Field
              label="URL"
              htmlFor="adt-url"
              error={errors["/webhook/url"]}
              hint="On a host webhooks may call (Integrations › Allowed hosts)"
            >
              <Input
                id="adt-url"
                className="font-mono text-secondary"
                placeholder="https://siem.corp.example/rvav"
                value={d.url}
                onChange={(e) => set({ url: e.target.value })}
              />
            </Field>
            <Field
              label="Signing secret"
              htmlFor="adt-secret"
              error={errors["/webhook/secret"]}
              hint="Each call is signed with it in X-Rvav-Signature, as subscriptions' webhooks are"
            >
              <Select
                id="adt-secret"
                value={d.secret}
                placeholder="Choose a stored secret"
                loading={secrets.isPending}
                onChange={(secret) => set({ secret })}
                options={stored}
              />
            </Field>
          </>
        )}
        <fieldset className="flex flex-col gap-2" aria-describedby="adt-events-note">
          <legend className="mb-1 text-secondary font-medium">Events</legend>
          <div className="grid grid-cols-1 gap-x-4 gap-y-1.5 sm:grid-cols-2">
            {allCategories.map((c) => (
              <label key={c} htmlFor={`adt-cat-${c}`} className="flex items-center gap-1.5 text-body">
                <Checkbox
                  id={`adt-cat-${c}`}
                  checked={d.categories.includes(c)}
                  onChange={(on) =>
                    set({ categories: on ? [...d.categories, c] : d.categories.filter((x) => x !== c) })
                  }
                />
                {categoryLabels[c]}
              </label>
            ))}
          </div>
          <FieldNote id="adt-events-note" error={errorAt(errors, "/categories")} hint="None ticked: every event." />
        </fieldset>
        <label htmlFor="adt-enabled" className="flex items-center gap-1.5 text-body">
          <Checkbox id="adt-enabled" checked={d.enabled} onChange={(enabled) => set({ enabled })} />
          Send events
        </label>
      </form>
    </Sheet>
  );
}

function TestDialog({ name, test, onClose }: { name: string; test: S["AuditDestinationTest"]; onClose: () => void }) {
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent title={`Test of ${name}`}>
        <div className="flex flex-col gap-3 p-4" aria-live="polite">
          <p className="flex items-start gap-1.5 text-body break-words">
            {test.ok ? (
              <CircleCheck className="mt-0.5 size-4 shrink-0 text-ok" aria-hidden />
            ) : (
              <CircleX className="mt-0.5 size-4 shrink-0 text-bad" aria-hidden />
            )}
            <span>
              <span className="font-medium">{test.ok ? "Delivered" : "Not delivered"}</span> in {test.duration_ms} ms.{" "}
              {test.message}
            </span>
          </p>
          <p className="text-secondary text-muted">
            The test event, settings.audit_destination.test with the ID test, went once: the events waiting aren't
            affected. The audit log records the test.
          </p>
        </div>
      </DialogContent>
    </Dialog>
  );
}

const where = (d: Destination) =>
  d.syslog ? (
    <>
      <div className="font-mono text-secondary break-all">
        {d.syslog.host}:{d.syslog.port}
      </div>
      <div className="text-secondary text-muted">
        {(d.syslog.transport ?? "tls").toUpperCase()} · {d.syslog.facility} · {d.syslog.app_name}
        {d.syslog.client_certificate ? " · client certificate" : ""}
      </div>
    </>
  ) : (
    <>
      <div className="font-mono text-secondary break-all">{d.webhook?.url}</div>
      <div className="text-secondary text-muted">Signed with {d.webhook?.secret}</div>
    </>
  );

function Delivery({ d }: { d: Destination }) {
  const s = d.state;
  const when = (iso: string) => <span title={fullDate(iso)}>{relative(iso)}</span>;
  return (
    <div className="flex flex-col gap-0.5 text-secondary">
      <div className="flex flex-wrap items-center gap-1.5">
        {!d.enabled ? (
          <Badge tone="neutral">Off</Badge>
        ) : s.failures > 0 ? (
          <Badge tone="bad">Failing</Badge>
        ) : s.backlog > 0 ? (
          <Badge tone="run">Sending</Badge>
        ) : (
          <Badge tone="ok">Up to date</Badge>
        )}
        <span>
          {s.backlog > 0 ? `${formatNumber(s.backlog)} waiting · ` : ""}
          {formatNumber(s.delivered)} delivered
        </span>
      </div>
      {s.failures > 0 && s.last_error && (
        <div className="break-words text-bad">
          {s.last_error}
          {s.last_error_at && <> ({when(s.last_error_at)})</>}
        </div>
      )}
      <div className="text-muted">
        {s.next_attempt_at && d.enabled && <>Next try {when(s.next_attempt_at)} · </>}
        {s.last_success_at ? <>Last delivered {when(s.last_success_at)}</> : "Nothing delivered yet"}
      </div>
    </div>
  );
}

const columns: Column<Destination>[] = [
  {
    id: "name",
    header: "Destination",
    sortValue: (d) => d.name,
    cell: (d) => (
      <>
        <div className="font-medium">{d.name}</div>
        <div className="text-secondary text-muted">{d.kind === "syslog" ? "Syslog server" : "Webhook"}</div>
      </>
    ),
  },
  { id: "where", header: "Where", cell: where },
  {
    id: "events",
    header: "Events",
    cell: (d) => (
      <span className="text-secondary">
        {d.categories.length ? d.categories.map((c) => categoryLabels[c]).join(", ") : "Every event"}
      </span>
    ),
  },
  { id: "delivery", header: "Delivery", cell: (d) => <Delivery d={d} /> },
];

export function AuditForwardingPage() {
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
    onError: (e) => failed(e, "The test didn't run"),
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
      toast.success(d.enabled ? `${d.name} is on` : `${d.name} is off`);
    },
    onError: (e) => failed(e, "Not saved"),
  });
  const remove = useMutation({
    mutationFn: (d: Destination) =>
      call(api.DELETE("/admin/audit-destinations/{id}", { params: { path: { id: d.id } } })),
    onSuccess: (_, d) => {
      invalidate(qc);
      toast.success(`${d.name} removed`);
      setRemoving(null);
    },
    onError: (e) => failed(e, "Not removed"),
  });
  const create = canEdit && (
    <Button variant="primary" onClick={() => setEditing("new")}>
      New destination
    </Button>
  );
  return (
    <Page>
      <PageHeader
        title="Audit forwarding"
        description="Syslog servers and webhooks, such as your SIEM's, that get a copy of each audit event. Events wait in rvav until they are delivered; the audit log keeps them all the same."
        actions={list.data?.items.length ? create : undefined}
      />
      <Panel title="Destinations" flush>
        <DataTable
          label="Destinations"
          columns={columns}
          rows={list.data?.items}
          rowKey={(d) => d.id}
          minWidth={860}
          loading={list.isPending}
          error={list.isError && <ErrorState error={list.error} onRetry={() => void list.refetch()} />}
          empty={
            <EmptyState
              icon={Send}
              title="No destination yet"
              body="Send audit events to a syslog server over TLS, or to a webhook, to keep them in your SIEM."
              action={create || undefined}
            />
          }
          rowMenuLabel={(d) => `Actions for ${d.name}`}
          rowMenu={
            canEdit
              ? (d) => (
                  <>
                    <MenuItem disabled={test.isPending} onSelect={() => test.mutate(d)}>
                      Send a test event
                    </MenuItem>
                    <MenuItem onSelect={() => setEditing(d)}>Change</MenuItem>
                    <MenuItem disabled={toggle.isPending} onSelect={() => toggle.mutate(d)}>
                      {d.enabled ? "Turn off" : "Turn on"}
                    </MenuItem>
                    <MenuSeparator />
                    <MenuItem danger onSelect={() => setRemoving(d)}>
                      Remove
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
          title={`Stop sending audit events to ${removing.name}?`}
          description="rvav sends it nothing more."
          consequences={
            removing.state.backlog > 0
              ? `The ${formatNumber(removing.state.backlog)} events waiting for it are dropped. The audit log keeps them.`
              : "The audit log keeps every event."
          }
          confirmLabel="Remove destination"
          pending={remove.isPending}
          onConfirm={() => remove.mutate(removing)}
        />
      )}
    </Page>
  );
}
