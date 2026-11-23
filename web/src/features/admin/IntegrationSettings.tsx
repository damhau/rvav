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
import { useId, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSaveFocus } from "@/components/ui/opener";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, Input, Textarea } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { formatList, fullDate, relative } from "@/lib/format";
import { i18n } from "@/lib/i18n";
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
  "servicenow",
  "smtp",
  "teams",
  "slack",
  "hosts",
  "trust",
] as const satisfies readonly S["IntegrationKind"][];

/** Read on next use, with no process to start again (ADR-0030): from when. */
const onNextUse: Partial<Record<Kind, "notification" | "console" | "vra" | "ad" | "servicenow" | "hosts">> = {
  smtp: "notification",
  teams: "notification",
  slack: "notification",
  console: "console",
  vra: "vra",
  ad: "ad",
  servicenow: "servicenow",
  hosts: "hosts",
};

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
  // The values the server takes, offered in a select.
  choices?: string[];
}

interface Definition {
  title: string;
  about: string;
  panel?: string;
  secret?: Setting;
  signing?: Setting;
  settings: Setting[];
  test?: string;
}

/** The settings of an integration, as the server's config.Fields, in the language shown. */
function definition(kind: Kind): Definition {
  const t = i18n.getFixedT(null, "admin");
  const about = t(`integrations.about.${kind}`);
  const interval = (env: string, hint: string): Setting => ({
    name: "sync_interval",
    label: t("integrations.setting.syncInterval"),
    env,
    type: "duration",
    hint,
  });
  // A certificate trusted for the connection besides those the CAs vouch for.
  const pin = (env: string): Setting => ({
    name: "cert_sha256",
    label: t("integrations.pin.label"),
    env,
    type: "string",
    hint: t("integrations.pin.hint"),
  });
  const insecure = (env: string): Setting => ({
    name: "insecure",
    label: t("integrations.setting.insecure"),
    env,
    type: "bool",
  });
  const password = (env: string): Setting => ({
    name: "password",
    label: t("integrations.setting.password"),
    env,
    type: "string",
  });
  const user = (env: string, hint?: string): Setting => ({
    name: "username",
    label: t("integrations.setting.user"),
    env,
    type: "string",
    hint,
  });
  const url = (env: string, hint?: string): Setting => ({
    name: "url",
    label: t("integrations.setting.url"),
    env,
    type: "string",
    hint,
  });
  const labels = (name: string, label: string, env: string): Setting => ({
    name,
    label,
    env,
    type: "list",
    hint: t("integrations.hint.commaSeparated"),
  });
  switch (kind) {
    case "vcenter":
      return {
        title: "vCenter",
        about,
        secret: password("RVAV_VCENTER_PASSWORD"),
        settings: [
          url("RVAV_VCENTER_URL", t("integrations.hint.vcenterUrl")),
          user("RVAV_VCENTER_USERNAME"),
          { name: "name", label: t("integrations.setting.nameShown"), env: "RVAV_VCENTER_NAME", type: "string" },
          interval("RVAV_SYNC_INTERVAL", t("integrations.hint.vcenterSync")),
          pin("RVAV_VCENTER_CERT_SHA256"),
          insecure("RVAV_VCENTER_INSECURE"),
        ],
      };
    // The console (ADR-0012): shown in vCenter's tab.
    case "console":
      return {
        title: t("integrations.kind.console"),
        about,
        panel: t("integrations.panel.console"),
        test: t("integrations.testConsole"),
        secret: {
          ...password("RVAV_CONSOLE_VCENTER_PASSWORD"),
          hint: t("integrations.hint.consolePassword"),
        },
        settings: [
          {
            name: "username",
            label: t("integrations.setting.consoleAccount"),
            env: "RVAV_CONSOLE_VCENTER_USERNAME",
            type: "string",
            hint: t("integrations.hint.consoleAccount"),
          },
          {
            name: "sdk_urls",
            label: t("integrations.setting.sdkUrls"),
            env: "RVAV_WMKS_SDK_URLS",
            type: "list",
            hint: t("integrations.hint.sdkUrls"),
          },
        ],
      };
    case "awx":
      return {
        title: "AWX",
        about,
        secret: { name: "token", label: t("integrations.setting.token"), env: "RVAV_AWX_TOKEN", type: "string" },
        settings: [
          url("RVAV_AWX_URL"),
          {
            name: "organization",
            label: t("integrations.setting.organization"),
            env: "RVAV_AWX_ORGANIZATION",
            type: "string",
          },
          labels("catalog_labels", t("integrations.setting.catalogLabels"), "RVAV_AWX_CATALOG_LABELS"),
          labels("action_labels", t("integrations.setting.actionLabels"), "RVAV_AWX_ACTION_LABELS"),
          {
            name: "inventory_source_id",
            label: t("integrations.setting.inventorySource"),
            env: "RVAV_AWX_INVENTORY_SOURCE_ID",
            type: "int",
          },
          interval("RVAV_AWX_SYNC_INTERVAL", t("integrations.hint.awxSync")),
          pin("RVAV_AWX_CERT_SHA256"),
          insecure("RVAV_AWX_INSECURE"),
        ],
      };
    case "efficientip":
      return {
        title: "EfficientIP",
        about,
        secret: password("RVAV_EFFICIENTIP_PASSWORD"),
        settings: [
          url("RVAV_EFFICIENTIP_URL"),
          {
            name: "auth",
            label: t("integrations.setting.signIn"),
            env: "RVAV_EFFICIENTIP_AUTH",
            type: "string",
            hint: t("integrations.hint.efficientipAuth"),
          },
          user("RVAV_EFFICIENTIP_USERNAME"),
          { name: "space", label: t("integrations.setting.space"), env: "RVAV_EFFICIENTIP_SPACE", type: "string" },
          {
            name: "dns_server",
            label: t("integrations.setting.dnsServer"),
            env: "RVAV_EFFICIENTIP_DNS_SERVER",
            type: "string",
          },
          {
            name: "dns_view",
            label: t("integrations.setting.dnsView"),
            env: "RVAV_EFFICIENTIP_DNS_VIEW",
            type: "string",
          },
          {
            name: "instance_id",
            label: t("integrations.setting.instance"),
            env: "RVAV_INSTANCE_ID",
            type: "string",
            hint: t("integrations.hint.instance"),
          },
          interval("RVAV_IPAM_SYNC_INTERVAL", t("integrations.hint.ipamSync")),
          pin("RVAV_EFFICIENTIP_CERT_SHA256"),
          insecure("RVAV_EFFICIENTIP_INSECURE"),
        ],
      };
    // NSX networking (ADR-0028): a user and password, or a principal identity.
    case "nsx":
      return {
        title: "NSX",
        about,
        secret: password("RVAV_NSX_PASSWORD"),
        settings: [
          url("RVAV_NSX_URL", t("integrations.hint.nsxUrl")),
          user("RVAV_NSX_USERNAME"),
          {
            name: "cert_file",
            label: t("integrations.setting.certFile"),
            env: "RVAV_NSX_CERT_FILE",
            type: "string",
            hint: t("integrations.hint.nsxCertFile"),
          },
          { name: "key_file", label: t("integrations.setting.keyFile"), env: "RVAV_NSX_KEY_FILE", type: "string" },
          pin("RVAV_NSX_CERT_SHA256"),
          insecure("RVAV_NSX_INSECURE"),
        ],
      };
    // The catalog's repository (ADR-0029): a token over HTTPS, or a deploy
    // key over SSH with the server's host keys.
    case "git":
      return {
        title: "Git",
        about,
        secret: {
          name: "credential",
          label: t("integrations.setting.gitCredential"),
          env: "RVAV_GIT_CREDENTIAL",
          type: "string",
          multiline: true,
          hint: t("integrations.hint.gitCredential"),
        },
        settings: [
          {
            name: "url",
            label: t("integrations.setting.repository"),
            env: "RVAV_GIT_URL",
            type: "string",
            hint: t("integrations.hint.gitUrl"),
          },
          { name: "branch", label: t("integrations.setting.branch"), env: "RVAV_GIT_BRANCH", type: "string" },
          {
            name: "dir",
            label: t("integrations.setting.dir"),
            env: "RVAV_GIT_DIR",
            type: "string",
            hint: t("integrations.hint.dir"),
          },
          user("RVAV_GIT_USERNAME", t("integrations.hint.gitUser")),
          {
            name: "committer",
            label: t("integrations.setting.committer"),
            env: "RVAV_GIT_COMMITTER",
            type: "string",
            hint: t("integrations.hint.committer"),
          },
          {
            name: "known_hosts",
            label: t("integrations.setting.knownHosts"),
            env: "RVAV_GIT_KNOWN_HOSTS",
            type: "string",
            multiline: true,
            hint: t("integrations.hint.knownHosts"),
          },
          interval("RVAV_GIT_SYNC_INTERVAL", t("integrations.hint.gitSync")),
          { ...insecure("RVAV_GIT_INSECURE"), label: t("integrations.setting.gitInsecure") },
        ],
      };
    // The vRA the importer reads (ADR-0027).
    case "vra":
      return {
        title: "vRA",
        about,
        secret: {
          name: "credential",
          label: t("integrations.setting.vraCredential"),
          env: formatList(["RVAV_VRA_TOKEN", "RVAV_VRA_PASSWORD"], "disjunction"),
          type: "string",
          hint: t("integrations.hint.vraCredential"),
        },
        settings: [
          url("RVAV_VRA_URL", t("integrations.hint.vraUrl")),
          user("RVAV_VRA_USERNAME", t("integrations.hint.vraUser")),
          {
            name: "domain",
            label: t("integrations.setting.domain"),
            env: "RVAV_VRA_DOMAIN",
            type: "string",
            hint: t("integrations.hint.domain"),
          },
          pin("RVAV_VRA_CERT_SHA256"),
          insecure("RVAV_VRA_INSECURE"),
        ],
      };
    // The Active Directory computer objects are made in (ADR-0064).
    case "ad":
      return {
        title: "Active Directory",
        about,
        secret: { ...password("RVAV_AD_PASSWORD"), label: t("integrations.setting.adPassword") },
        settings: [
          url("RVAV_AD_URL", t("integrations.hint.adUrl")),
          {
            ...user("RVAV_AD_USERNAME", t("integrations.hint.adUser")),
            label: t("integrations.setting.serviceAccount"),
          },
          {
            name: "base_dn",
            label: t("integrations.setting.baseDn"),
            env: "RVAV_AD_BASE_DN",
            type: "string",
            hint: t("integrations.hint.baseDn"),
          },
          pin("RVAV_AD_CERT_SHA256"),
          insecure("RVAV_AD_INSECURE"),
          {
            name: "plain_ldap",
            label: t("integrations.setting.plainLdap"),
            env: "RVAV_AD_PLAIN_LDAP",
            type: "bool",
            warn: t("integrations.hint.plainLdap"),
          },
        ],
      };
    // ADR-0065.
    case "servicenow":
      return {
        title: "ServiceNow",
        about,
        secret: {
          name: "secret",
          label: t("integrations.setting.snowSecret"),
          env: "RVAV_SERVICENOW_SECRET",
          type: "string",
          hint: t("integrations.hint.snowSecret"),
        },
        settings: [
          {
            ...url("RVAV_SERVICENOW_URL", t("integrations.hint.snowUrl")),
            label: t("integrations.setting.instanceUrl"),
          },
          {
            name: "auth",
            label: t("integrations.setting.signIn"),
            env: "RVAV_SERVICENOW_AUTH",
            type: "string",
            hint: t("integrations.hint.snowAuth"),
            choices: ["basic", "oauth"],
          },
          user("RVAV_SERVICENOW_USERNAME", t("integrations.hint.snowUser")),
          {
            name: "client_id",
            label: t("integrations.setting.clientId"),
            env: "RVAV_SERVICENOW_CLIENT_ID",
            type: "string",
          },
          pin("RVAV_SERVICENOW_CERT_SHA256"),
          insecure("RVAV_SERVICENOW_INSECURE"),
          {
            name: "cmdb",
            label: t("integrations.setting.cmdb"),
            env: "RVAV_SERVICENOW_CMDB",
            type: "bool",
            hint: t("integrations.hint.cmdb"),
          },
          {
            name: "cmdb_class",
            label: t("integrations.setting.cmdbClass"),
            env: "RVAV_SERVICENOW_CMDB_CLASS",
            type: "string",
            hint: t("integrations.hint.cmdbClass"),
          },
          {
            name: "data_source",
            label: t("integrations.setting.dataSource"),
            env: "RVAV_SERVICENOW_DATA_SOURCE",
            type: "string",
            hint: t("integrations.hint.dataSource"),
          },
          {
            name: "projects",
            label: t("integrations.setting.projects"),
            env: "RVAV_SERVICENOW_PROJECTS",
            type: "list",
            hint: t("integrations.hint.projects"),
          },
          interval("RVAV_SERVICENOW_SYNC_INTERVAL", t("integrations.hint.snowSync")),
          {
            name: "allowed_sources",
            label: t("integrations.setting.allowedSources"),
            env: "RVAV_SERVICENOW_ALLOWED_SOURCES",
            type: "list",
            hint: t("integrations.hint.allowedSources"),
          },
          {
            name: "requests_per_minute",
            label: t("integrations.setting.requestsPerMinute"),
            env: "RVAV_SERVICENOW_REQUESTS_PER_MINUTE",
            type: "int",
            hint: t("integrations.hint.requestsPerMinute"),
          },
        ],
      };
    // Notification channels (S11, ADR-0023).
    case "smtp":
      return {
        title: t("integrations.kind.smtp"),
        about,
        secret: password("RVAV_SMTP_PASSWORD"),
        settings: [
          { name: "host", label: t("integrations.setting.smtpHost"), env: "RVAV_SMTP_HOST", type: "string" },
          { name: "port", label: t("integrations.setting.port"), env: "RVAV_SMTP_PORT", type: "int" },
          { name: "tls", label: "TLS", env: "RVAV_SMTP_TLS", type: "string", hint: t("integrations.hint.smtpTls") },
          {
            name: "from",
            label: t("integrations.setting.from"),
            env: "RVAV_SMTP_FROM",
            type: "string",
            hint: "rvav <rvav@corp.example>",
          },
          user("RVAV_SMTP_USERNAME"),
        ],
      };
    case "teams":
      return {
        title: "Microsoft Teams",
        about,
        secret: {
          name: "app_password",
          label: t("integrations.setting.clientSecret"),
          env: "RVAV_TEAMS_APP_PASSWORD",
          type: "string",
        },
        settings: [
          { name: "app_id", label: t("integrations.setting.botAppId"), env: "RVAV_TEAMS_APP_ID", type: "string" },
          {
            name: "tenant_id",
            label: t("integrations.setting.tenantId"),
            env: "RVAV_TEAMS_TENANT_ID",
            type: "string",
          },
          {
            name: "service_url",
            label: t("integrations.setting.serviceUrl"),
            env: "RVAV_TEAMS_SERVICE_URL",
            type: "string",
            hint: t("integrations.hint.serviceUrl"),
          },
          // Microsoft's public cloud's by default; another cloud's here.
          {
            name: "login_url",
            label: t("integrations.setting.loginUrl"),
            env: "RVAV_TEAMS_LOGIN_URL",
            type: "string",
            hint: t("integrations.hint.loginUrl"),
          },
          { name: "graph_url", label: "Microsoft Graph", env: "RVAV_TEAMS_GRAPH_URL", type: "string" },
          {
            name: "openid_url",
            label: t("integrations.setting.openidUrl"),
            env: "RVAV_TEAMS_OPENID_URL",
            type: "string",
            hint: t("integrations.hint.openidUrl"),
          },
        ],
      };
    case "slack":
      return {
        title: "Slack",
        about,
        secret: {
          name: "bot_token",
          label: t("integrations.setting.botToken"),
          env: "RVAV_SLACK_BOT_TOKEN",
          type: "string",
        },
        signing: {
          name: "signing_secret",
          label: t("integrations.setting.signingSecret"),
          env: "RVAV_SLACK_SIGNING_SECRET",
          type: "string",
        },
        settings: [
          {
            name: "api_url",
            label: t("integrations.setting.apiUrl"),
            env: "RVAV_SLACK_API_URL",
            type: "string",
            hint: t("integrations.hint.apiUrl"),
          },
        ],
      };
    // The hosts rvav may call on others' behalf; no secret, no test.
    case "hosts":
      return {
        title: t("integrations.kind.hosts"),
        about,
        panel: t("integrations.panel.hosts"),
        test: t("integrations.check"),
        settings: [
          {
            name: "rest_source_hosts",
            label: t("integrations.setting.restHosts"),
            env: "RVAV_REST_SOURCE_HOSTS",
            type: "list",
            hint: t("integrations.hint.restHosts"),
          },
          {
            name: "webhook_hosts",
            label: t("integrations.setting.webhookHosts"),
            env: "RVAV_WEBHOOK_HOSTS",
            type: "list",
            hint: t("integrations.hint.webhookHosts"),
          },
        ],
      };
    // The CAs every connection trusts besides the system's (U1.4); no secret.
    case "trust":
      return {
        title: t("integrations.kind.trust"),
        about,
        panel: t("integrations.panel.trust"),
        test: t("integrations.check"),
        settings: [
          {
            name: "ca_bundle",
            label: t("integrations.setting.caBundle"),
            env: "RVAV_CA_BUNDLE",
            type: "string",
            multiline: true,
            rows: 10,
            wide: true,
            hint: t("integrations.hint.caBundle"),
          },
        ],
      };
  }
}

const text = (v: unknown, type: Setting["type"]) =>
  type === "list"
    ? Array.isArray(v)
      ? v.join(", ")
      : ""
    : typeof v === "string" || typeof v === "number"
      ? String(v)
      : "";

/** One test check: its name, and the server's message. */
export function CheckLine({ name, message }: { name: string; message: string }) {
  const { t } = useTranslation("admin");
  return (
    <Trans
      t={t}
      i18nKey="integrations.checkLine"
      values={{ name, message }}
      components={{ name: <span className="font-medium" /> }}
    />
  );
}

/** A connection's settings form, with its test, in a panel of its own. */
export function Integration({ it }: { it: S["Integration"] & { kind: Kind } }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const formId = useId();
  const def = definition(it.kind);
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
      const nextUse = onNextUse[it.kind];
      toast.success(t("integrations.savedToast", { title: def.title }), {
        description: nextUse
          ? t(`integrations.onNextUse.${nextUse}`)
          : saved.applying
            ? t("integrations.appliesWhenDone")
            : t("integrations.appliesAtStart"),
      });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.notSaved"));
    },
  });
  const test = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/integrations/{kind}/test", { params: { path: { kind: it.kind } }, body: body() })),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.testDidntRun"));
    },
  });
  const form = useRef<HTMLFormElement>(null);
  const submit = useRef<HTMLButtonElement>(null);
  const saveFocus = useSaveFocus(form, save.isPending, () => submit.current);
  // Trust this certificate: its SHA-256 in the form, with TLS checks on.
  const pinField = def.settings.find((f) => f.name === "cert_sha256");
  const trustIt = pinField
    ? (sha256: string) => {
        setValues((v) => ({
          ...v,
          cert_sha256: sha256,
          ...(it.locked.includes("insecure") ? {} : { insecure: false }),
        }));
        toast.info(t("integrations.pin.inForm"), { description: t("integrations.pin.saveToTrust") });
      }
    : undefined;
  const lockedHint = (f: Setting) => (
    <span className="inline-flex items-center gap-1">
      <Lock className="size-3" aria-hidden /> {t("integrations.setBy", { env: f.env })}
    </span>
  );
  const secretHint = () => {
    const state = it.secret_set ? t("integrations.secretSet") : t("integrations.secretNotSet");
    return def.secret?.hint ? t("integrations.secretHint", { state, hint: def.secret.hint }) : state;
  };
  return (
    <Panel
      title={def.panel ?? t("integrations.panel.connection", { title: def.title })}
      actions={
        <div className="flex items-center gap-2">
          {!it.configured && <Badge tone="warn">{t("integrations.state.notConfigured")}</Badge>}
          {it.applying && <Badge tone="warn">{t("integrations.state.applying")}</Badge>}
        </div>
      }
      footer={
        <>
          <SavedBy it={it} />
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("integrations.testing") : (def.test ?? t("integrations.testConnection"))}
          </Button>
          <Button ref={submit} type="submit" form={formId} variant="primary" disabled={save.isPending}>
            {save.isPending ? t("integrations.saving") : t("integrations.saveSettings")}
          </Button>
        </>
      }
    >
      <form
        ref={form}
        id={formId}
        className="flex flex-col gap-4"
        onKeyDownCapture={saveFocus.interacted}
        onPointerDownCapture={saveFocus.interacted}
        onSubmit={(e) => {
          e.preventDefault();
          if (save.isPending) return;
          setErrors({});
          saveFocus.submitted();
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
            if (f.choices)
              return (
                <Field key={f.name} label={f.label} htmlFor={id} hint={hint} error={error}>
                  <Select
                    id={id}
                    disabled={locked}
                    listLabel={f.label}
                    value={String(values[f.name] ?? "")}
                    onChange={(v) => setValues({ ...values, [f.name]: v })}
                    options={f.choices.map((c) => ({ value: c, label: c }))}
                  />
                </Field>
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
              hint={secretLocked ? lockedHint(def.secret) : secretHint()}
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
                    ? t("integrations.secretSet")
                    : t("integrations.signingNotSet")
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
            aria-label={t("integrations.testOf", { title: def.title })}
            aria-live="polite"
            className="flex flex-col gap-1 rounded-md border border-line p-3 text-secondary"
          >
            <p className="font-medium">{test.data.ok ? t("integrations.allPassed") : t("integrations.oneFailed")}</p>
            <ul className="flex flex-col gap-0.5">
              {test.data.checks.map((c) => (
                <li key={c.name} className="flex items-start gap-1.5">
                  {c.ok ? (
                    <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-label={t("integrations.passed")} />
                  ) : (
                    <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-label={t("integrations.failed")} />
                  )}
                  <span className="break-words">
                    <CheckLine name={c.name} message={c.message} />
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
  const { t } = useTranslation("admin");
  if (!it.updated_at) return <span className="mr-auto">{t("integrations.notSavedYet")}</span>;
  return (
    <span className="mr-auto">
      <Trans
        t={t}
        i18nKey={it.updated_by ? "integrations.savedAtBy" : "integrations.savedAt"}
        values={{ when: relative(it.updated_at), name: it.updated_by?.name }}
        components={{ time: <span title={fullDate(it.updated_at)} /> }}
      />
    </span>
  );
}

/** An integration's name, as its page and the list say it. */
export const integrationTitle = (kind: S["IntegrationKind"]) =>
  kind === "oidc" ? i18n.t("admin:integrations.kind.oidc") : definition(kind).title;

/** What an integration is for, in a sentence. */
export const integrationAbout = (kind: S["IntegrationKind"]) => i18n.t(`admin:integrations.about.${kind}`);

/** The page an integration's form is on: the console's is on vCenter's. */
export const integrationPage = (kind: S["IntegrationKind"]) => (kind === "console" ? "vcenter" : kind);
