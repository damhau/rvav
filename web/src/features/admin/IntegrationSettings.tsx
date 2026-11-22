// Integrations (S9, ADR-0020, ADR-0028, ADR-0029, ADR-0030): saved from the
// UI, an environment variable winning and locking its field; the password or
// token is write-only. Saved, a connection applies as each process drains
// and starts again; the console, vRA, notification channels, allowed hosts
// and sync intervals on next use. The identity provider has a form of its
// own; the console's is on vCenter's page. A connection's test shows
// its server's certificate, pinned here or trusted through the CA bundle of
// Trusted certificates (U1.4).
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { CircleCheck, CircleX, Lock } from "lucide-react";
import { useId, useState } from "react";
import { toast } from "sonner";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { fullDate, relative } from "@/lib/format";
import { Applying } from "./Applying";
import { failed, fieldErrors } from "./governance/common";
import { ServerCertificate } from "./ServerCertificate";

type S = components["schemas"];
type Kind = Exclude<S["IntegrationKind"], "oidc">;

export const integrationKinds = [
  "oidc",
  "vcenter",
  "console",
  "awx",
  "efficientip",
  "nsx",
  "git",
  "vra",
  "ad",
  "smtp",
  "teams",
  "slack",
  "hosts",
  "trust",
] as const satisfies readonly S["IntegrationKind"][];

/** Read on next use, with no process to start again (ADR-0030): from when. */
const onNextUse: Partial<Record<Kind, string>> = {
  smtp: "They apply from the next notification.",
  teams: "They apply from the next notification.",
  slack: "They apply from the next notification.",
  console: "They apply from the next console opened.",
  vra: "They apply from the next read of vRA.",
  ad: "They apply from the next computer object made or deleted.",
  hosts: "They apply from the next call to a host.",
};

const interval = (what: string): Setting => ({
  name: "sync_interval",
  label: "Sync interval",
  env: "",
  type: "duration",
  hint: `How often ${what}, for example 5m or 1h30m (30s to 24h); read on next use`,
});

/** A certificate trusted for the connection besides those the CAs vouch for. */
const pin = (env: string): Setting => ({
  name: "cert_sha256",
  label: "Trusted certificate (SHA-256)",
  env,
  type: "string",
  hint: "A self-signed one, trusted besides the CAs: Trust this certificate, after a test, sets it",
});

interface Setting {
  name: string;
  label: string;
  env: string;
  type: "string" | "bool" | "int" | "list" | "duration";
  hint?: string;
  // Several lines: a private key, known_hosts lines.
  multiline?: boolean;
  rows?: number;
  // Across the form's two columns.
  wide?: boolean;
  // A checkbox's warning, shown while it is ticked.
  warn?: string;
}

// The settings of each integration, as the server's config.Fields; Slack
// has a second secret, its signing secret; the allowed hosts have none.
const fields: Record<
  Kind,
  {
    title: string;
    about: string;
    panel?: string;
    secret?: Setting;
    signing?: Setting;
    settings: Setting[];
    test?: string;
  }
> = {
  vcenter: {
    title: "vCenter",
    about: "Where VMs are built and read, with the console's account and the other vCenters rvav manages.",
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
      { ...interval("the inventory is read in full; changes come in between"), env: "RVAV_SYNC_INTERVAL" },
      pin("RVAV_VCENTER_CERT_SHA256"),
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_VCENTER_INSECURE", type: "bool" },
    ],
  },
  // The console (ADR-0012): shown in vCenter's tab.
  console: {
    title: "Console",
    about: "The account VM consoles open with, and the WebMKS scripts that show them in the browser.",
    panel: "Console",
    test: "Test console",
    secret: {
      name: "password",
      label: "Password",
      env: "RVAV_CONSOLE_VCENTER_PASSWORD",
      type: "string",
      hint: "Given again when vCenter's address changes: it only goes to the vCenter it was given for",
    },
    settings: [
      {
        name: "username",
        label: "Console account",
        env: "RVAV_CONSOLE_VCENTER_USERNAME",
        type: "string",
        hint: "A vCenter account that may only open consoles: VMRC links carry a ticket of it",
      },
      {
        name: "sdk_urls",
        label: "WebMKS SDK scripts",
        env: "RVAV_WMKS_SDK_URLS",
        type: "list",
        hint: "Comma-separated, in load order; none: VMRC only",
      },
    ],
  },
  awx: {
    title: "AWX",
    about: "The jobs that configure VMs once built and run day-2 actions, and the inventory token AWX reads rvav with.",
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
      { ...interval("templates are read"), env: "RVAV_AWX_SYNC_INTERVAL" },
      pin("RVAV_AWX_CERT_SHA256"),
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_AWX_INSECURE", type: "bool" },
    ],
  },
  efficientip: {
    title: "EfficientIP",
    about: "Each VM's IP address and DNS records, and the subnets networks get.",
    secret: { name: "password", label: "Password", env: "RVAV_EFFICIENTIP_PASSWORD", type: "string" },
    settings: [
      { name: "url", label: "URL", env: "RVAV_EFFICIENTIP_URL", type: "string" },
      {
        name: "auth",
        label: "Sign-in",
        env: "RVAV_EFFICIENTIP_AUTH",
        type: "string",
        hint: "native, basic or token (an API token: its key ID as the user, its secret as the password)",
      },
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
      { ...interval("subnets are read"), env: "RVAV_IPAM_SYNC_INTERVAL" },
      pin("RVAV_EFFICIENTIP_CERT_SHA256"),
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_EFFICIENTIP_INSECURE", type: "bool" },
    ],
  },
  // NSX networking (ADR-0028): a user and password, or a principal identity.
  nsx: {
    title: "NSX",
    about: "Segments and security groups made for deployments, and their firewall rules.",
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
      pin("RVAV_NSX_CERT_SHA256"),
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_NSX_INSECURE", type: "bool" },
    ],
  },
  // The catalog's repository (ADR-0029): a token over HTTPS, or a deploy
  // key over SSH with the server's host keys.
  git: {
    title: "Git",
    about: "The repository rvav keeps the catalog in, one file per item.",
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
      { ...interval("the repository is read"), env: "RVAV_GIT_SYNC_INTERVAL" },
      { name: "insecure", label: "Skip TLS or host key verification", env: "RVAV_GIT_INSECURE", type: "bool" },
    ],
  },
  // The vRA the importer reads (ADR-0027).
  vra: {
    title: "vRA",
    about: "The vRA that vRA import reads its catalog and deployments from.",
    secret: {
      name: "credential",
      label: "Password or refresh token",
      env: "RVAV_VRA_TOKEN or RVAV_VRA_PASSWORD",
      type: "string",
      hint: "With a user, its password; without, a refresh token (an API token)",
    },
    settings: [
      {
        name: "url",
        label: "URL",
        env: "RVAV_VRA_URL",
        type: "string",
        hint: "For example https://vra.corp.example",
      },
      { name: "username", label: "User", env: "RVAV_VRA_USERNAME", type: "string", hint: "Leave empty for a token" },
      {
        name: "domain",
        label: "Domain",
        env: "RVAV_VRA_DOMAIN",
        type: "string",
        hint: "The user's, for example corp.example",
      },
      pin("RVAV_VRA_CERT_SHA256"),
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_VRA_INSECURE", type: "bool" },
    ],
  },
  // The Active Directory computer objects are made in (ADR-0064).
  ad: {
    title: "Active Directory",
    about: "Where VMs' computer objects are made before they are built, and deleted with them.",
    secret: { name: "password", label: "Service account password", env: "RVAV_AD_PASSWORD", type: "string" },
    settings: [
      {
        name: "url",
        label: "URL",
        env: "RVAV_AD_URL",
        type: "string",
        hint: "ldaps://dc01.corp.example for LDAPS, or ldap://dc01.corp.example for StartTLS",
      },
      {
        name: "username",
        label: "Service account",
        env: "RVAV_AD_USERNAME",
        type: "string",
        hint: "Its DN or UPN, for example svc-rvav@corp.example",
      },
      {
        name: "base_dn",
        label: "Base DN",
        env: "RVAV_AD_BASE_DN",
        type: "string",
        hint: "The domain's, for example DC=corp,DC=example: OUs are under it",
      },
      pin("RVAV_AD_CERT_SHA256"),
      { name: "insecure", label: "Skip TLS verification", env: "RVAV_AD_INSECURE", type: "bool" },
      {
        name: "plain_ldap",
        label: "Allow plain LDAP, without StartTLS",
        env: "RVAV_AD_PLAIN_LDAP",
        type: "bool",
        warn: "With ldap://, the service account's password and every object go over the network in the clear. For a lab only.",
      },
    ],
  },
  // Notification channels (S11, ADR-0023).
  smtp: {
    title: "Email",
    about: "The SMTP server notifications are sent by email through.",
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
    about: "The bot that sends notifications in Teams, where approvers approve and reject.",
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
      // Microsoft's public cloud's by default; another cloud's here.
      {
        name: "login_url",
        label: "Sign-in endpoint",
        env: "RVAV_TEAMS_LOGIN_URL",
        type: "string",
        hint: "Where the app gets its tokens; another Microsoft cloud's, if not the public one",
      },
      { name: "graph_url", label: "Microsoft Graph", env: "RVAV_TEAMS_GRAPH_URL", type: "string" },
      {
        name: "openid_url",
        label: "Bot Framework's OpenID document",
        env: "RVAV_TEAMS_OPENID_URL",
        type: "string",
        hint: "Its keys check Teams' calls to rvav",
      },
    ],
  },
  slack: {
    title: "Slack",
    about: "The app that sends notifications in Slack, where approvers approve and reject.",
    secret: { name: "bot_token", label: "Bot token", env: "RVAV_SLACK_BOT_TOKEN", type: "string" },
    signing: { name: "signing_secret", label: "Signing secret", env: "RVAV_SLACK_SIGNING_SECRET", type: "string" },
    settings: [
      {
        name: "api_url",
        label: "Web API",
        env: "RVAV_SLACK_API_URL",
        type: "string",
        hint: "Slack's, https://slack.com/api, unless another is used",
      },
    ],
  },
  // The hosts rvav may call on others' behalf; no secret, no test.
  hosts: {
    title: "Allowed hosts",
    about: "The hosts REST value sources, a composite's REST calls and webhooks may reach.",
    panel: "Hosts rvav may call",
    test: "Check",
    settings: [
      {
        name: "rest_source_hosts",
        label: "REST value sources and calls",
        env: "RVAV_REST_SOURCE_HOSTS",
        type: "list",
        hint: "Comma-separated: cmdb.corp.example, *.corp.example, http://host:port for plain http",
      },
      {
        name: "webhook_hosts",
        label: "Webhooks",
        env: "RVAV_WEBHOOK_HOSTS",
        type: "list",
        hint: "Comma-separated, as above: the hosts subscriptions may post to",
      },
    ],
  },
  // The CAs every connection trusts besides the system's (U1.4); no secret.
  trust: {
    title: "Trusted certificates",
    about: "The CAs every connection trusts besides the system's.",
    panel: "CA bundle",
    test: "Check",
    settings: [
      {
        name: "ca_bundle",
        label: "CA certificates (PEM)",
        env: "RVAV_CA_BUNDLE",
        type: "string",
        multiline: true,
        rows: 10,
        wide: true,
        hint: "Trusted by every connection besides the system's CAs: a private CA's root or issuing CA. Certificates only, no key",
      },
    ],
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

/** A connection's settings form, with its test, in a panel of its own. */
export function Integration({ it }: { it: S["Integration"] & { kind: Kind } }) {
  const qc = useQueryClient();
  const formId = useId();
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
  // Shown from a save until every process applied it, and then said so.
  const [watching] = useState(it.applying);
  const secretLocked = def.secret ? it.locked.includes(def.secret.name) : false;
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
    ...(def.secret && secret.trim() ? { secret: def.secret.multiline ? secret.trim() : secret } : {}),
    ...(signing ? { signing_secret: signing } : {}),
  });
  const save = useMutation({
    mutationFn: () =>
      call(api.PUT("/admin/integrations/{kind}", { params: { path: { kind: it.kind } }, body: body() })),
    onSuccess: (saved) => {
      void qc.invalidateQueries({ queryKey: ["admin", "integrations"] });
      setSecret("");
      setSigning("");
      toast.success(`${def.title} settings saved`, {
        description: onNextUse[it.kind]
          ? onNextUse[it.kind]
          : saved.applying
            ? "Each process applies them once its running work is done."
            : "They apply when rvav's processes start.",
      });
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
  // Trust this certificate: its SHA-256 in the form, with TLS checks on.
  const pinField = def.settings.find((f) => f.name === "cert_sha256");
  const trustIt = pinField
    ? (sha256: string) => {
        setValues((v) => ({
          ...v,
          cert_sha256: sha256,
          ...(it.locked.includes("insecure") ? {} : { insecure: false }),
        }));
        toast.info("Its SHA-256 is in the form", { description: "Save the settings to trust it, then test again." });
      }
    : undefined;
  const lockedHint = (f: Setting) => (
    <span className="inline-flex items-center gap-1">
      <Lock className="size-3" aria-hidden /> Set by {f.env}
    </span>
  );
  return (
    <Panel
      title={def.panel ?? `${def.title} connection`}
      actions={
        <div className="flex items-center gap-2">
          {!it.configured && <Badge tone="warn">Not configured</Badge>}
          {it.applying && <Badge tone="warn">Applying</Badge>}
        </div>
      }
      footer={
        <>
          <SavedBy it={it} />
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : (def.test ?? "Test connection")}
          </Button>
          <Button type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save settings"}
          </Button>
        </>
      }
    >
      <form
        id={formId}
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          save.mutate();
        }}
      >
        {(watching || it.applying) && <Applying revision={it.revision} />}
        <div className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2">
          {def.settings.map((f) => {
            const locked = it.locked.includes(f.name);
            const id = `${it.kind}-${f.name}`;
            const error = errors[`/settings/${f.name}`];
            const hint = locked ? lockedHint(f) : f.hint;
            if (f.type === "bool")
              return (
                <div key={f.name} className="flex flex-col gap-1 self-end">
                  <label htmlFor={id} className="flex items-center gap-1.5 text-body">
                    <Checkbox
                      id={id}
                      disabled={locked}
                      checked={values[f.name] === true}
                      onChange={(on) => setValues({ ...values, [f.name]: on })}
                    />
                    {f.label}
                  </label>
                  {locked && <span className="text-secondary text-faint">{lockedHint(f)}</span>}
                  {f.warn && values[f.name] === true && (
                    <span role="status" className="text-secondary text-warn">
                      {f.warn}
                    </span>
                  )}
                </div>
              );
            if (f.multiline)
              return (
                <Field
                  key={f.name}
                  label={f.label}
                  htmlFor={id}
                  hint={hint}
                  error={error}
                  className={f.wide ? "md:col-span-2" : undefined}
                >
                  <Textarea
                    id={id}
                    rows={f.rows ?? 3}
                    spellCheck={false}
                    className="font-mono text-secondary"
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
                  className={f.type === "string" && f.name !== "username" ? "font-mono text-secondary" : undefined}
                  inputMode={f.type === "int" ? "numeric" : undefined}
                  disabled={locked}
                  value={String(values[f.name] ?? "")}
                  onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
                />
              </Field>
            );
          })}
          {def.secret && (
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
                  className="font-mono text-secondary [-webkit-text-security:disc]"
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
          )}
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
        {test.data && (
          <section
            aria-label={`${def.title} test`}
            aria-live="polite"
            className="flex flex-col gap-1 rounded-md border border-line p-3 text-secondary"
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
                  <span className="break-words">
                    <span className="font-medium">{c.name}:</span> {c.message}
                  </span>
                </li>
              ))}
            </ul>
            {test.data.certificate && (
              <ServerCertificate
                cert={test.data.certificate}
                onTrust={trustIt}
                pinInForm={values.cert_sha256 === test.data.certificate.sha256}
                pinLocked={pinField && it.locked.includes("cert_sha256") ? pinField.env : undefined}
              />
            )}
          </section>
        )}
      </form>
    </Panel>
  );
}

/** When a form's settings were saved, and by whom, on the left of its footer. */
export function SavedBy({ it }: { it: S["Integration"] }) {
  if (!it.updated_at) return <span className="mr-auto">Not saved yet</span>;
  return (
    <span className="mr-auto">
      Saved <span title={fullDate(it.updated_at)}>{relative(it.updated_at)}</span>
      {it.updated_by ? ` by ${it.updated_by.name}` : ""}
    </span>
  );
}

/** An integration's name, as its page and the list say it. */
export const integrationTitle = (kind: S["IntegrationKind"]) =>
  kind === "oidc" ? "Identity provider" : fields[kind].title;

/** What an integration is for, in a sentence. */
export const integrationAbout = (kind: S["IntegrationKind"]) =>
  kind === "oidc"
    ? "Where everyone but the local administrator signs in, and the groups their roles come from."
    : fields[kind].about;

/** The page an integration's form is on: the console's is on vCenter's. */
export const integrationPage = (kind: S["IntegrationKind"]) => (kind === "console" ? "vcenter" : kind);
