// The identity provider (ADR-0030): set in Administration, tested (discovery
// and keys), tried with a sign-in in a window of its own before it is
// saved, and applied without a restart. RVAV_OIDC_* lock their fields, as
// for any integration; RVAV_WEB_OIDC_*, on the web container, win in the
// browser, so they lock theirs too and are what is saved.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, CircleX, ExternalLink, Lock } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { CopyText } from "@/components/common/misc";
import { Panel } from "@/components/ui/panel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { TRIAL_CHANNEL, type TrialMessage, openTrial, webOverrides } from "@/lib/auth";
import { failed, fieldErrors } from "./governance/common";
import { CheckLine, SavedBy } from "./IntegrationSettings";
import { ServerCertificate } from "./ServerCertificate";

type S = components["schemas"];
type Name = "issuer" | "discovery_url" | "client_id" | "scopes" | "groups_claim" | "manager_claim";

type Label = "issuer" | "discovery" | "clientId" | "scopes" | "groupsClaim" | "managerClaim";

const settings: { name: Name; label: Label; env: string; placeholder?: string }[] = [
  { name: "issuer", label: "issuer", env: "RVAV_OIDC_ISSUER" },
  { name: "discovery_url", label: "discovery", env: "RVAV_OIDC_DISCOVERY_URL" },
  { name: "client_id", label: "clientId", env: "RVAV_OIDC_CLIENT_ID" },
  { name: "scopes", label: "scopes", env: "RVAV_OIDC_SCOPES", placeholder: "openid profile email" },
  { name: "groups_claim", label: "groupsClaim", env: "RVAV_OIDC_GROUPS_CLAIM", placeholder: "groups" },
  { name: "manager_claim", label: "managerClaim", env: "RVAV_OIDC_MANAGER_CLAIM", placeholder: "manager" },
];

const str = (v: unknown) => (typeof v === "string" ? v : "");

export const redirectUri = () => `${globalThis.location.origin}/auth/callback`;

export function IdentityProvider({ it }: { it: S["Integration"] }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const web = webOverrides();
  const lockedBy = (n: Name) =>
    it.locked.includes(n) ? settings.find((s) => s.name === n)!.env : (web[n as keyof typeof web]?.env ?? null);
  const [values, setValues] = useState<Record<Name, string>>(
    () =>
      Object.fromEntries(
        settings.map((s) => [
          s.name,
          !it.locked.includes(s.name) && web[s.name as keyof typeof web]
            ? web[s.name as keyof typeof web]!.value
            : str(it.settings[s.name]),
        ]),
      ) as Record<Name, string>,
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  // The values the last passing test was run with: the trial needs one, for
  // its window to reach the provider (Content-Security-Policy).
  const [tested, setTested] = useState<string | null>(null);
  const [waiting, setWaiting] = useState(false);
  const [trialError, setTrialError] = useState<string | null>(null);
  const tried = useRef<Record<string, string>>({});

  const body = (): S["IntegrationWrite"] => ({
    settings: Object.fromEntries(
      settings.filter((s) => !it.locked.includes(s.name)).map((s) => [s.name, values[s.name].trim()]),
    ),
  });
  const key = JSON.stringify(values);

  const save = useMutation({
    mutationFn: () => call(api.PUT("/admin/integrations/{kind}", { params: { path: { kind: "oidc" } }, body: body() })),
    onSuccess: (saved) => {
      void qc.invalidateQueries({ queryKey: ["admin", "integrations"] });
      toast.success(saved.configured ? t("identityProvider.saved") : t("identityProvider.cleared"), {
        description: saved.configured ? t("identityProvider.appliesNow") : t("identityProvider.localOnly"),
      });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.notSaved"));
    },
  });
  const test = useMutation({
    mutationFn: () =>
      call(api.POST("/admin/integrations/{kind}/test", { params: { path: { kind: "oidc" } }, body: body() })),
    onMutate: () => {
      setErrors({});
      setTested(null);
    },
    onSuccess: (r) => setTested(r.ok ? key : null),
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, t("integrations.testDidntRun"));
    },
  });
  const trial = useMutation({
    mutationFn: (token: string) =>
      call(
        api.POST("/admin/integrations/oidc/trial", {
          body: { settings: tried.current, access_token: token },
        }),
      ),
    onError: (e) => failed(e, t("identityProvider.trialFailed")),
  });

  // The trial window's token, or why there is none.
  useEffect(() => {
    if (!waiting || typeof BroadcastChannel === "undefined") return;
    const ch = new BroadcastChannel(TRIAL_CHANNEL);
    ch.onmessage = (e: MessageEvent<TrialMessage>) => {
      setWaiting(false);
      if (e.data.token) trial.mutate(e.data.token);
      else setTrialError(e.data.error ?? t("identityProvider.noToken"));
    };
    return () => ch.close();
    // eslint-disable-next-line react-hooks/exhaustive-deps -- trial.mutate is stable
  }, [waiting]);

  const startTrial = () => {
    setTrialError(null);
    trial.reset();
    tried.current = body().settings as Record<string, string>;
    const w = openTrial({
      authority: values.issuer.trim(),
      clientId: values.client_id.trim(),
      scope: values.scopes.trim() || "openid profile email",
    });
    if (!w) {
      setTrialError(t("identityProvider.popupBlocked"));
      return;
    }
    setWaiting(true);
  };

  const canTry = tested === key && values.issuer.trim() !== "" && values.client_id.trim() !== "";
  return (
    <Panel
      title={t("identityProvider.title")}
      actions={!it.configured && <Badge tone="warn">{t("identityProvider.notSet")}</Badge>}
      footer={
        <>
          <SavedBy it={it} />
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("integrations.testing") : t("identityProvider.test")}
          </Button>
          <Button type="button" disabled={!canTry || waiting || trial.isPending} onClick={startTrial}>
            <ExternalLink aria-hidden /> {waiting ? t("identityProvider.signingIn") : t("identityProvider.try")}
          </Button>
          <Button type="submit" form="oidc-settings" variant="primary" disabled={save.isPending}>
            {save.isPending ? t("integrations.saving") : t("integrations.saveSettings")}
          </Button>
        </>
      }
    >
      <form
        id="oidc-settings"
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          save.mutate();
        }}
      >
        <p className="max-w-3xl text-secondary text-muted">
          <Trans
            t={t}
            i18nKey="identityProvider.about"
            components={{
              groups: (
                <Link
                  to="/admin/projects"
                  search={{ tab: "groups" }}
                  className="text-accent underline underline-offset-2"
                />
              ),
            }}
          />
        </p>
        <div className="flex max-w-3xl flex-col gap-1 rounded-md border border-line px-3 py-2 text-secondary">
          <span className="font-medium">{t("identityProvider.redirect")}</span>
          <CopyText value={redirectUri()} />
          <span className="text-faint">
            {t("identityProvider.redirectHint", { origin: globalThis.location.origin })}
          </span>
        </div>
        <div className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2">
          {settings.map((s) => {
            const lock = lockedBy(s.name);
            const id = `oidc-${s.name}`;
            return (
              <Field
                key={s.name}
                label={t(`identityProvider.${s.label}`)}
                htmlFor={id}
                error={errors[`/settings/${s.name}`]}
                hint={
                  lock ? (
                    <span className="inline-flex items-center gap-1">
                      <Lock className="size-3" aria-hidden /> {t("integrations.setBy", { env: lock })}
                    </span>
                  ) : (
                    t(`identityProvider.${s.label}Hint`)
                  )
                }
              >
                <Input
                  id={id}
                  className="font-mono text-secondary"
                  spellCheck={false}
                  disabled={lock !== null}
                  placeholder={s.placeholder}
                  value={values[s.name]}
                  onChange={(e) => setValues({ ...values, [s.name]: e.target.value })}
                />
              </Field>
            );
          })}
        </div>
        {!canTry && <p className="text-secondary text-faint">{t("identityProvider.testFirst")}</p>}
        {test.data && (
          <section
            aria-label={t("identityProvider.testLabel")}
            aria-live="polite"
            className="flex flex-col gap-1 rounded-md border border-line p-3 text-secondary"
          >
            <p className="font-medium">
              {test.data.ok ? t("identityProvider.allPassed") : t("integrations.oneFailed")}
            </p>
            <ul className="flex flex-col gap-0.5">
              {test.data.checks.map((c) => (
                <li key={c.name} className="flex items-start gap-1.5">
                  {c.ok ? (
                    <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-label={t("integrations.passed")} />
                  ) : (
                    <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-label={t("integrations.failed")} />
                  )}
                  <span className="break-all">
                    <CheckLine name={c.name} message={c.message} />
                  </span>
                </li>
              ))}
            </ul>
            {test.data.certificate && <ServerCertificate cert={test.data.certificate} />}
          </section>
        )}
        {waiting && (
          <p role="status" className="text-secondary text-muted">
            {t("identityProvider.waiting")}{" "}
            <button
              type="button"
              className="text-accent underline underline-offset-2"
              onClick={() => setWaiting(false)}
            >
              {t("identityProvider.stopWaiting")}
            </button>
          </p>
        )}
        {trialError && (
          <p role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-secondary text-bad">
            {trialError}
          </p>
        )}
        {trial.data && <TrialResult r={trial.data} />}
      </form>
    </Panel>
  );
}

function TrialResult({ r }: { r: S["OidcTrial"] }) {
  const { t } = useTranslation("admin");
  const none = r.global_roles.length === 0 && r.project_roles.length === 0;
  return (
    <section
      aria-label={t("identityProvider.trial")}
      aria-live="polite"
      className="flex flex-col gap-3 rounded-md border border-line p-3 text-secondary"
    >
      <p className="flex items-start gap-1.5 font-medium">
        {r.accepted ? (
          <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-hidden />
        ) : (
          <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-hidden />
        )}
        {r.accepted ? t("identityProvider.accepted") : t("identityProvider.refused")}
      </p>
      {r.message && <p className={r.accepted ? "text-warn" : "text-bad"}>{r.message}</p>}
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
        <dt className="text-muted">{t("identityProvider.subject")}</dt>
        <dd className="font-mono break-all">{r.subject ?? "—"}</dd>
        <dt className="text-muted">{t("identityProvider.name")}</dt>
        <dd>{r.name ?? "—"}</dd>
        <dt className="text-muted">{t("identityProvider.email")}</dt>
        <dd>{r.email ?? "—"}</dd>
        <dt className="text-muted">
          <Trans
            t={t}
            i18nKey="identityProvider.groups"
            values={{ claim: r.groups_claim ?? "groups" }}
            components={{ claim: <span className="font-mono" /> }}
          />
        </dt>
        <dd className="flex flex-wrap gap-1">
          {r.groups.length === 0 ? t("identityProvider.none") : r.groups.map((g) => <Badge key={g}>{g}</Badge>)}
        </dd>
        <dt className="text-muted">
          <Trans
            t={t}
            i18nKey="identityProvider.manager"
            values={{ claim: r.manager_claim ?? "manager" }}
            components={{ claim: <span className="font-mono" /> }}
          />
        </dt>
        <dd className="break-all">{r.manager ?? t("identityProvider.none")}</dd>
        {r.accepted && (
          <>
            <dt className="text-muted">{t("identityProvider.rolesGiven")}</dt>
            <dd className="flex flex-wrap gap-1">
              {none
                ? t("identityProvider.noRoles")
                : [
                    ...r.global_roles.map((role) => (
                      <Badge key={role} tone="accent">
                        {role}
                      </Badge>
                    )),
                    ...r.project_roles.map((p) => (
                      <Badge key={p.project.id} tone="outline">
                        {t("identityProvider.roleIn", { role: p.role, project: p.project.name })}
                      </Badge>
                    )),
                  ]}
            </dd>
          </>
        )}
      </dl>
      <details>
        <summary className="cursor-pointer text-muted">{t("identityProvider.claims")}</summary>
        <pre className="mt-1 max-h-72 overflow-auto rounded-sm bg-sunken p-2 font-mono text-caption">
          {JSON.stringify(r.claims, null, 2)}
        </pre>
      </details>
    </section>
  );
}
