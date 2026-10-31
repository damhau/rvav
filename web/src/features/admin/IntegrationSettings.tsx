// vCenter, AWX, EfficientIP, NSX and Git settings (S9, ADR-0020, ADR-0028, ADR-0029): saved from the UI,
// an environment variable winning and locking its field; the password or
// token is write-only; saved settings apply when rvav's processes restart.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Lock } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { ErrorState, Skeleton } from "@/components/common/feedback";
import { Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { adm } from "@/lib/query";
import { failed, fieldErrors } from "./governance/common";
import { VCenters } from "./VCenters";

type S = components["schemas"];
type Kind = S["IntegrationKind"];

interface Setting {
  name: string;
  label: string;
  env: string;
  type: "string" | "bool" | "int" | "list";
  hint?: string;
  // Several lines: a private key, known_hosts lines.
  multiline?: boolean;
}

// The settings of each integration, as the server's config.Fields; Slack
// has a second secret, its signing secret.
const fields: Record<Kind, { title: string; secret: Setting; signing?: Setting; settings: Setting[] }> = {
  vcenter: {
    title: "vCenter",
    secret: { name: "password", label: "Password", env: "RVAV_VCENTER_PASSWORD", type: "string" },
    settings: [
      {
        name: "url",
        label: "URL",
        env: "RVAV_VCENTER_URL",
        type: "string",
        hint: "For example https://vc01.corp.example/sdk",
      },
      { name: "username", label: "User", env: "RVAV_VCENTER_USERNAME", type: "string" },
      { name: "name", label: "Name shown", env: "RVAV_VCENTER_NAME", type: "string" },
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_VCENTER_INSECURE", type: "bool" },
    ],
  },
  awx: {
    title: "AWX",
    secret: { name: "token", label: "Token", env: "RVAV_AWX_TOKEN", type: "string" },
    settings: [
      { name: "url", label: "URL", env: "RVAV_AWX_URL", type: "string" },
      { name: "organization", label: "Organisation", env: "RVAV_AWX_ORGANIZATION", type: "string" },
      {
        name: "catalog_labels",
        label: "Catalog labels",
        env: "RVAV_AWX_CATALOG_LABELS",
        type: "list",
        hint: "Comma-separated",
      },
      {
        name: "action_labels",
        label: "Day-2 action labels",
        env: "RVAV_AWX_ACTION_LABELS",
        type: "list",
        hint: "Comma-separated",
      },
      { name: "inventory_source_id", label: "Inventory source ID", env: "RVAV_AWX_INVENTORY_SOURCE_ID", type: "int" },
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_AWX_INSECURE", type: "bool" },
    ],
  },
  efficientip: {
    title: "EfficientIP",
    secret: { name: "password", label: "Password", env: "RVAV_EFFICIENTIP_PASSWORD", type: "string" },
    settings: [
      { name: "url", label: "URL", env: "RVAV_EFFICIENTIP_URL", type: "string" },
      { name: "auth", label: "Sign-in", env: "RVAV_EFFICIENTIP_AUTH", type: "string", hint: "native or basic" },
      { name: "username", label: "User", env: "RVAV_EFFICIENTIP_USERNAME", type: "string" },
      { name: "space", label: "IP space", env: "RVAV_EFFICIENTIP_SPACE", type: "string" },
      { name: "dns_server", label: "DNS server", env: "RVAV_EFFICIENTIP_DNS_SERVER", type: "string" },
      { name: "dns_view", label: "DNS view", env: "RVAV_EFFICIENTIP_DNS_VIEW", type: "string" },
      {
        name: "instance_id",
        label: "rvav instance",
        env: "RVAV_INSTANCE_ID",
        type: "string",
        hint: "Tags what this rvav creates",
      },
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_EFFICIENTIP_INSECURE", type: "bool" },
    ],
  },
  // NSX networking (ADR-0028): a user and password, or a principal identity.
  nsx: {
    title: "NSX",
    secret: { name: "password", label: "Password", env: "RVAV_NSX_PASSWORD", type: "string" },
    settings: [
      {
        name: "url",
        label: "URL",
        env: "RVAV_NSX_URL",
        type: "string",
        hint: "The NSX Manager, for example https://nsx.corp.example",
      },
      { name: "username", label: "User", env: "RVAV_NSX_USERNAME", type: "string" },
      {
        name: "cert_file",
        label: "Certificate file",
        env: "RVAV_NSX_CERT_FILE",
        type: "string",
        hint: "A principal identity instead of a password: its PEM certificate on rvav's hosts",
      },
      { name: "key_file", label: "Key file", env: "RVAV_NSX_KEY_FILE", type: "string" },
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_NSX_INSECURE", type: "bool" },
    ],
  },
  // The catalog's repository (ADR-0029): a token over HTTPS, or a deploy
  // key over SSH with the server's host keys.
  git: {
    title: "Git",
    secret: {
      name: "credential",
      label: "Token or deploy key",
      env: "RVAV_GIT_CREDENTIAL",
      type: "string",
      multiline: true,
      hint: "HTTPS: a token. SSH: the deploy key's private key (PEM)",
    },
    settings: [
      {
        name: "url",
        label: "Repository",
        env: "RVAV_GIT_URL",
        type: "string",
        hint: "https://git.corp.example/infra/catalog.git or git@git.corp.example:infra/catalog.git",
      },
      { name: "branch", label: "Branch", env: "RVAV_GIT_BRANCH", type: "string" },
      { name: "dir", label: "Directory", env: "RVAV_GIT_DIR", type: "string", hint: "One file per item in it" },
      {
        name: "username",
        label: "User",
        env: "RVAV_GIT_USERNAME",
        type: "string",
        hint: "HTTPS: as the server wants it with a token",
      },
      {
        name: "committer",
        label: "Committer address",
        env: "RVAV_GIT_COMMITTER",
        type: "string",
        hint: "rvav's commits; the author is the administrator",
      },
      {
        name: "known_hosts",
        label: "SSH host keys",
        env: "RVAV_GIT_KNOWN_HOSTS",
        type: "string",
        multiline: true,
        hint: "known_hosts lines, from ssh-keyscan",
      },
      { name: "insecure", label: "Skip TLS or host key verification", env: "RVAV_GIT_INSECURE", type: "bool" },
    ],
  },
  // Notification channels (S11, ADR-0023).
  smtp: {
    title: "Email",
    secret: { name: "password", label: "Password", env: "RVAV_SMTP_PASSWORD", type: "string" },
    settings: [
      { name: "host", label: "SMTP server", env: "RVAV_SMTP_HOST", type: "string" },
      { name: "port", label: "Port", env: "RVAV_SMTP_PORT", type: "int" },
      { name: "tls", label: "TLS", env: "RVAV_SMTP_TLS", type: "string", hint: "starttls, tls or none" },
      { name: "from", label: "From", env: "RVAV_SMTP_FROM", type: "string", hint: "rvav <rvav@corp.example>" },
      { name: "username", label: "User", env: "RVAV_SMTP_USERNAME", type: "string" },
    ],
  },
  teams: {
    title: "Microsoft Teams",
    secret: { name: "app_password", label: "Client secret", env: "RVAV_TEAMS_APP_PASSWORD", type: "string" },
    settings: [
      { name: "app_id", label: "Bot app ID", env: "RVAV_TEAMS_APP_ID", type: "string" },
      { name: "tenant_id", label: "Tenant ID", env: "RVAV_TEAMS_TENANT_ID", type: "string" },
      {
        name: "service_url",
        label: "Bot Connector",
        env: "RVAV_TEAMS_SERVICE_URL",
        type: "string",
        hint: "Where conversations start, for example https://smba.trafficmanager.net/emea",
      },
    ],
  },
  slack: {
    title: "Slack",
    secret: { name: "bot_token", label: "Bot token", env: "RVAV_SLACK_BOT_TOKEN", type: "string" },
    signing: { name: "signing_secret", label: "Signing secret", env: "RVAV_SLACK_SIGNING_SECRET", type: "string" },
    settings: [],
  },
};

const text = (v: unknown, type: Setting["type"]) =>
  type === "list"
    ? Array.isArray(v)
      ? v.join(", ")
      : ""
    : typeof v === "string" || typeof v === "number"
      ? String(v)
      : "";

function Integration({ it }: { it: S["Integration"] }) {
  const qc = useQueryClient();
  const def = fields[it.kind];
  const editable = def.settings.filter((f) => !it.locked.includes(f.name));
  const [values, setValues] = useState<Record<string, string | boolean>>(() =>
    Object.fromEntries(
      def.settings.map((f) => [
        f.name,
        f.type === "bool" ? it.settings[f.name] === true : text(it.settings[f.name], f.type),
      ]),
    ),
  );
  const [secret, setSecret] = useState("");
  const [signing, setSigning] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const secretLocked = it.locked.includes(def.secret.name);
  const signingLocked = def.signing ? it.locked.includes(def.signing.name) : false;
  const body = (): S["IntegrationWrite"] => ({
    settings: Object.fromEntries(
      editable.map((f): [string, unknown] => {
        const v = values[f.name];
        if (f.type === "bool") return [f.name, v === true];
        const s = typeof v === "string" ? v.trim() : "";
        if (f.type === "int") return [f.name, s === "" ? 0 : Number(s)];
        if (f.type === "list")
          return [
            f.name,
            s
              .split(",")
              .map((x) => x.trim())
              .filter(Boolean),
          ];
        return [f.name, s];
      }),
    ),
    // A key or token pasted with a line break after it.
    ...(secret.trim() ? { secret: def.secret.multiline ? secret.trim() : secret } : {}),
    ...(signing ? { signing_secret: signing } : {}),
  });
  const save = useMutation({
    mutationFn: () =>
      call(api.PUT("/admin/integrations/{kind}", { params: { path: { kind: it.kind } }, body: body() })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["admin", "integrations"] });
      setSecret("");
      setSigning("");
      toast.success(`${def.title} settings saved`, { description: "They apply when rvav's processes restart." });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
    },
  });
  const test = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/integrations/{kind}/test", { params: { path: { kind: it.kind } }, body: body() })),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "The test didn't run");
    },
  });
  const lockedHint = (f: Setting) => (
    <span className="inline-flex items-center gap-1">
      <Lock className="size-3" aria-hidden /> Set by {f.env}
    </span>
  );
  return (
    <Panel
      title={`${def.title} connection`}
      actions={
        <div className="flex items-center gap-2">
          {!it.configured && <Badge tone="warn">Not configured</Badge>}
          {it.restart_needed && <Badge tone="warn">Restart needed</Badge>}
        </div>
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          save.mutate();
        }}
      >
        {it.restart_needed && (
          <p role="status" className="rounded-md border border-warn/40 bg-warn/10 px-3 py-2 text-xs">
            Saved settings apply when rvav's processes (API and worker) restart; until then they run with the earlier
            ones.
          </p>
        )}
        <div className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2">
          {def.settings.map((f) => {
            const locked = it.locked.includes(f.name);
            const id = `${it.kind}-${f.name}`;
            const error = errors[`/settings/${f.name}`];
            const hint = locked ? lockedHint(f) : f.hint;
            if (f.type === "bool")
              return (
                <div key={f.name} className="flex flex-col gap-1 self-end">
                  <label htmlFor={id} className="flex items-center gap-1.5 text-[13px]">
                    <Checkbox
                      id={id}
                      disabled={locked}
                      checked={values[f.name] === true}
                      onChange={(on) => setValues({ ...values, [f.name]: on })}
                    />
                    {f.label}
                  </label>
                  {locked && <span className="text-xs text-faint">{lockedHint(f)}</span>}
                </div>
              );
            if (f.multiline)
              return (
                <Field key={f.name} label={f.label} htmlFor={id} hint={hint} error={error}>
                  <Textarea
                    id={id}
                    rows={3}
                    spellCheck={false}
                    className="font-mono text-xs"
                    disabled={locked}
                    value={String(values[f.name] ?? "")}
                    onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
                  />
                </Field>
              );
            return (
              <Field key={f.name} label={f.label} htmlFor={id} hint={hint} error={error}>
                <Input
                  id={id}
                  className={f.type === "string" && f.name !== "username" ? "font-mono text-xs" : undefined}
                  inputMode={f.type === "int" ? "numeric" : undefined}
                  disabled={locked}
                  value={String(values[f.name] ?? "")}
                  onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
                />
              </Field>
            );
          })}
          <Field
            label={def.secret.label}
            htmlFor={`${it.kind}-secret`}
            error={errors["/secret"]}
            hint={
              secretLocked
                ? lockedHint(def.secret)
                : `${it.secret_set ? "Set; type a new one to replace it" : "Not set"}${def.secret.hint ? `. ${def.secret.hint}` : ""}`
            }
          >
            {def.secret.multiline ? (
              // A key keeps its lines; masked as it is typed, never shown
              // after.
              <Textarea
                id={`${it.kind}-secret`}
                rows={3}
                spellCheck={false}
                autoComplete="off"
                className="font-mono text-xs [-webkit-text-security:disc]"
                disabled={secretLocked}
                placeholder={it.secret_set ? "••••••••" : undefined}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
            ) : (
              <Input
                id={`${it.kind}-secret`}
                type="password"
                autoComplete="new-password"
                disabled={secretLocked}
                placeholder={it.secret_set ? "••••••••" : undefined}
                value={secret}
                onChange={(e) => setSecret(e.target.value)}
              />
            )}
          </Field>
          {def.signing && (
            <Field
              label={def.signing.label}
              htmlFor={`${it.kind}-signing`}
              error={errors["/signing_secret"]}
              hint={
                signingLocked
                  ? lockedHint(def.signing)
                  : it.signing_secret_set
                    ? "Set; type a new one to replace it"
                    : "Not set: Approve and Reject can't be checked"
              }
            >
              <Input
                id={`${it.kind}-signing`}
                type="password"
                autoComplete="new-password"
                disabled={signingLocked}
                placeholder={it.signing_secret_set ? "••••••••" : undefined}
                value={signing}
                onChange={(e) => setSigning(e.target.value)}
              />
            </Field>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" variant="primary" disabled={save.isPending}>
            Save settings
          </Button>
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : "Test connection"}
          </Button>
          {it.updated_at && (
            <span className="text-xs text-faint">
              Saved {new Date(it.updated_at).toLocaleString()}
              {it.updated_by ? ` by ${it.updated_by.name}` : ""}
            </span>
          )}
        </div>
        {test.data && (
          <section
            aria-label={`${def.title} test`}
            aria-live="polite"
            className="flex flex-col gap-1 rounded-md border border-line p-3 text-xs"
          >
            <p className="font-medium">{test.data.ok ? "Every check passed." : "A check failed."}</p>
            <ul className="flex flex-col gap-0.5">
              {test.data.checks.map((c) => (
                <li key={c.name} className="flex items-start gap-1.5">
                  {c.ok ? (
                    <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-label="Passed" />
                  ) : (
                    <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-label="Failed" />
                  )}
                  <span>
                    <span className="font-medium">{c.name}:</span> {c.message}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </form>
    </Panel>
  );
}

export function IntegrationSettings() {
  const list = useQuery(adm.integrations());
  if (list.isPending) return <Skeleton className="h-64" />;
  if (list.isError) return <ErrorState error={list.error} onRetry={() => void list.refetch()} />;
  const items = list.data.items;
  return (
    <Tabs defaultValue={items[0]?.kind}>
      <TabsList aria-label="Integrations">
        {items.map((it) => (
          <TabsTrigger key={it.kind} value={it.kind}>
            {fields[it.kind].title}
          </TabsTrigger>
        ))}
      </TabsList>
      {items.map((it) => (
        <TabsContent key={it.kind} value={it.kind} className="flex flex-col gap-4 pt-4">
          <Integration key={it.updated_at ?? "unsaved"} it={it} />
          {it.kind === "vcenter" && <VCenters />}
        </TabsContent>
      ))}
    </Tabs>
  );
}
