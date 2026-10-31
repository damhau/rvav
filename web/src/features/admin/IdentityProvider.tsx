// The identity provider (ADR-0030): set in Administration, tested (discovery
// and keys), tried with a sign-in in a window of its own before it is
// saved, and applied without a restart. RVAV_OIDC_* lock their fields, as
// for any integration; RVAV_WEB_OIDC_*, on the web container, win in the
// browser, so they lock theirs too and are what is saved.
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { CircleCheck, CircleX, ExternalLink, Lock } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { CopyText } from "@/components/common/misc";
import { Panel } from "@/components/common/page";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";
import { api, call } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import { TRIAL_CHANNEL, type TrialMessage, openTrial, webOverrides } from "@/lib/auth";
import { failed, fieldErrors } from "./governance/common";

type S = components["schemas"];
type Name = "issuer" | "discovery_url" | "client_id" | "scopes" | "groups_claim";

const settings: { name: Name; label: string; env: string; hint: string; placeholder?: string }[] = [
  {
    name: "issuer",
    label: "Issuer",
    env: "RVAV_OIDC_ISSUER",
    hint: "As in its tokens, and as the browser reaches it. Entra ID: https://login.microsoftonline.com/<tenant>/v2.0",
  },
  {
    name: "discovery_url",
    label: "Discovery URL",
    env: "RVAV_OIDC_DISCOVERY_URL",
    hint: "Only when rvav reaches the provider at another address than the browser (inside a cluster)",
  },
  {
    name: "client_id",
    label: "Client ID",
    env: "RVAV_OIDC_CLIENT_ID",
    hint: "The web app's client at the provider; tokens must have it as their audience",
  },
  {
    name: "scopes",
    label: "Scopes",
    env: "RVAV_OIDC_SCOPES",
    hint: "Space-separated, with openid. Entra ID: also offline_access and a scope of rvav's API, api://<client>/access",
    placeholder: "openid profile email",
  },
  {
    name: "groups_claim",
    label: "Groups claim",
    env: "RVAV_OIDC_GROUPS_CLAIM",
    hint: "The claim group mappings read: groups, or roles for Entra ID's app roles",
    placeholder: "groups",
  },
];

const str = (v: unknown) => (typeof v === "string" ? v : "");

export const redirectUri = () => `${globalThis.location.origin}/auth/callback`;

export function IdentityProvider({ it }: { it: S["Integration"] }) {
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
      toast.success(saved.configured ? "Identity provider saved" : "Identity provider cleared", {
        description: saved.configured
          ? "It applies now: sign-in uses it within a few seconds, without a restart."
          : "Only the local administrator and API tokens sign in now.",
      });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      failed(e, "Not saved");
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
      failed(e, "The test didn't run");
    },
  });
  const trial = useMutation({
    mutationFn: (token: string) =>
      call(
        api.POST("/admin/integrations/oidc/trial", {
          body: { settings: tried.current, access_token: token },
        }),
      ),
    onError: (e) => failed(e, "The trial couldn't be checked"),
  });

  // The trial window's token, or why there is none.
  useEffect(() => {
    if (!waiting || typeof BroadcastChannel === "undefined") return;
    const ch = new BroadcastChannel(TRIAL_CHANNEL);
    ch.onmessage = (e: MessageEvent<TrialMessage>) => {
      setWaiting(false);
      if (e.data.token) trial.mutate(e.data.token);
      else setTrialError(e.data.error ?? "No token came back.");
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
      setTrialError("The browser blocked the window: allow pop-ups for this site, then try again.");
      return;
    }
    setWaiting(true);
  };

  const canTry = tested === key && values.issuer.trim() !== "" && values.client_id.trim() !== "";
  return (
    <Panel title="Identity provider" actions={!it.configured && <Badge tone="warn">Not set</Badge>}>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          setErrors({});
          save.mutate();
        }}
      >
        <p className="max-w-3xl text-xs text-muted">
          Everyone but the local administrator signs in here, and gets roles from the group mappings in{" "}
          <Link to="/admin/projects" className="text-accent underline underline-offset-2">
            Projects
          </Link>
          . A saved provider applies without a restart. The local administrator still signs in when it is down.
        </p>
        <div className="flex max-w-3xl flex-col gap-1 rounded-md border border-line px-3 py-2 text-xs">
          <span className="font-medium">Redirect URI, to register at the provider</span>
          <CopyText value={redirectUri()} />
          <span className="text-faint">
            As a single-page application's (Entra ID: the Single-page application platform; Keycloak: a public client
            with PKCE). Sign-out comes back to {globalThis.location.origin}/.
          </span>
        </div>
        <div className="grid max-w-3xl grid-cols-1 gap-3 md:grid-cols-2">
          {settings.map((s) => {
            const lock = lockedBy(s.name);
            const id = `oidc-${s.name}`;
            return (
              <Field
                key={s.name}
                label={s.label}
                htmlFor={id}
                error={errors[`/settings/${s.name}`]}
                hint={
                  lock ? (
                    <span className="inline-flex items-center gap-1">
                      <Lock className="size-3" aria-hidden /> Set by {lock}
                    </span>
                  ) : (
                    s.hint
                  )
                }
              >
                <Input
                  id={id}
                  className="font-mono text-xs"
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
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" variant="primary" disabled={save.isPending}>
            Save settings
          </Button>
          <Button type="button" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? "Testing…" : "Test provider"}
          </Button>
          <Button type="button" disabled={!canTry || waiting || trial.isPending} onClick={startTrial}>
            <ExternalLink aria-hidden /> {waiting ? "Signing in…" : "Try signing in"}
          </Button>
          {it.updated_at && (
            <span className="text-xs text-faint">
              Saved {new Date(it.updated_at).toLocaleString()}
              {it.updated_by ? ` by ${it.updated_by.name}` : ""}
            </span>
          )}
        </div>
        {!canTry && (
          <p className="text-xs text-faint">
            Test the provider first: the trial sign-in uses the settings above, before they are saved, in a window of
            its own.
          </p>
        )}
        {test.data && (
          <section
            aria-label="Identity provider test"
            aria-live="polite"
            className="flex flex-col gap-1 rounded-md border border-line p-3 text-xs"
          >
            <p className="font-medium">{test.data.ok ? "Every check passed: try signing in." : "A check failed."}</p>
            <ul className="flex flex-col gap-0.5">
              {test.data.checks.map((c) => (
                <li key={c.name} className="flex items-start gap-1.5">
                  {c.ok ? (
                    <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-label="Passed" />
                  ) : (
                    <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-label="Failed" />
                  )}
                  <span className="break-all">
                    <span className="font-medium">{c.name}:</span> {c.message}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )}
        {waiting && (
          <p role="status" className="text-xs text-muted">
            Sign in at the provider in the new window.{" "}
            <button
              type="button"
              className="text-accent underline underline-offset-2"
              onClick={() => setWaiting(false)}
            >
              Stop waiting
            </button>
          </p>
        )}
        {trialError && (
          <p role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-xs text-bad">
            {trialError}
          </p>
        )}
        {trial.data && <TrialResult r={trial.data} />}
      </form>
    </Panel>
  );
}

function TrialResult({ r }: { r: S["OidcTrial"] }) {
  const none = r.global_roles.length === 0 && r.project_roles.length === 0;
  return (
    <section
      aria-label="Trial sign-in"
      aria-live="polite"
      className="flex flex-col gap-3 rounded-md border border-line p-3 text-xs"
    >
      <p className="flex items-start gap-1.5 font-medium">
        {r.accepted ? (
          <CircleCheck className="mt-px size-3.5 shrink-0 text-ok" aria-hidden />
        ) : (
          <CircleX className="mt-px size-3.5 shrink-0 text-bad" aria-hidden />
        )}
        {r.accepted ? "rvav accepts this token." : "rvav refuses this token."}
      </p>
      {r.message && <p className={r.accepted ? "text-warn" : "text-bad"}>{r.message}</p>}
      <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1">
        <dt className="text-muted">Subject</dt>
        <dd className="font-mono break-all">{r.subject ?? "—"}</dd>
        <dt className="text-muted">Name</dt>
        <dd>{r.name ?? "—"}</dd>
        <dt className="text-muted">Email</dt>
        <dd>{r.email ?? "—"}</dd>
        <dt className="text-muted">
          Groups (<span className="font-mono">{r.groups_claim ?? "groups"}</span>)
        </dt>
        <dd className="flex flex-wrap gap-1">
          {r.groups.length === 0 ? "None" : r.groups.map((g) => <Badge key={g}>{g}</Badge>)}
        </dd>
        {r.accepted && (
          <>
            <dt className="text-muted">Roles given</dt>
            <dd className="flex flex-wrap gap-1">
              {none
                ? "None: no group mapping matches, so this user could sign in but do nothing."
                : [
                    ...r.global_roles.map((role) => (
                      <Badge key={role} tone="accent">
                        {role}
                      </Badge>
                    )),
                    ...r.project_roles.map((p) => (
                      <Badge key={p.project.id} tone="outline">
                        {p.role} in {p.project.name}
                      </Badge>
                    )),
                  ]}
            </dd>
          </>
        )}
      </dl>
      <details>
        <summary className="cursor-pointer text-muted">The token's claims</summary>
        <pre className="mt-1 max-h-72 overflow-auto rounded-sm bg-sunken p-2 font-mono text-[11px]">
          {JSON.stringify(r.claims, null, 2)}
        </pre>
      </details>
    </section>
  );
}
