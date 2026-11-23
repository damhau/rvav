// The sign-in page and the local administrator's password change (ADR-0030).
// Both are shown before the app, outside its router: nobody is signed in
// yet, or the session may do nothing but change the password.
import { KeyRound, LogIn, RotateCw } from "lucide-react";
import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { api } from "@/lib/api/client";
import type { Problem } from "@/lib/api/types";
import {
  CHANGE_PASSWORD_PATH,
  SIGN_IN_PATH,
  hasProvider,
  signInProblem,
  signInWithProvider,
  signOut,
  signedInLocally,
  trialResult,
} from "@/lib/auth";
import { useBranding } from "@/lib/branding";
import { BrandMark } from "@/components/shell/BrandMark";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

/** Where to go once signed in: the page asked for, or the overview. */
function target() {
  const { pathname, search } = globalThis.location;
  return pathname === SIGN_IN_PATH || pathname === CHANGE_PASSWORD_PATH ? "/" : pathname + search;
}

function Screen({ title, lead, children }: { title: string; lead: React.ReactNode; children: React.ReactNode }) {
  const { name } = useBranding();
  return (
    <main className="flex min-h-dvh items-start justify-center bg-bg px-4 pt-[12vh] pb-10 text-fg">
      <div className="flex w-full max-w-[360px] flex-col gap-6">
        <div className="flex items-center gap-2">
          <BrandMark />
          <span className="min-w-0 text-body leading-tight font-semibold tracking-[-0.01em] break-words">{name}</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <h1 className="text-title font-semibold">{title}</h1>
          <div className="text-body text-muted">{lead}</div>
        </div>
        {children}
      </div>
    </main>
  );
}

function Alert({ children }: { children: React.ReactNode }) {
  return (
    <p role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-body text-bad">
      {children}
    </p>
  );
}

/** The seconds left before another attempt, outside the alert: read when asked, not each second. */
function Left({ seconds }: { seconds: number }) {
  const { t } = useTranslation("account");
  if (seconds <= 0) return null;
  return (
    <p aria-live="off" className="-mt-1.5 text-secondary text-muted tabular-nums">
      {t("signIn.left", { count: seconds })}
    </p>
  );
}

/** Seconds left before another attempt, counting down from a 429's Retry-After. */
function useWait() {
  const [until, setUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (until <= now) return;
    const t = setTimeout(() => setNow(Date.now()), 250);
    return () => clearTimeout(t);
  }, [until, now]);
  const left = Math.max(0, Math.ceil((until - now) / 1000));
  return {
    left,
    start: (seconds: number) => {
      const n = Date.now();
      setNow(n);
      setUntil(n + seconds * 1000);
    },
  };
}

export function SignInPage({
  provider = hasProvider(),
  problem = signInProblem(),
}: {
  provider?: boolean;
  problem?: ReturnType<typeof signInProblem>;
}) {
  const { t } = useTranslation("account");
  const { name } = useBranding();
  const providerDown = t("signIn.providerDown");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [providerError, setProviderError] = useState<string | null>(() =>
    problem === "provider" ? providerDown : problem === "refused" ? t("signIn.refused") : null,
  );
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const wait = useWait();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    setProviderError(null);
    try {
      const { data, error: body, response } = await api.POST("/auth/sign-in", { body: { username, password } });
      if (data) {
        signedInLocally();
        globalThis.location.assign(data.password_change_required ? CHANGE_PASSWORD_PATH : target());
        return;
      }
      const problem: Problem | undefined = body;
      if (response.status === 429) {
        const seconds = Number(response.headers.get("Retry-After") ?? "1") || 1;
        wait.start(seconds);
        setError(t("signIn.tooMany"));
      } else if (response.status === 401) {
        setError(t("signIn.wrong"));
      } else if (response.status === 409 && problem?.detail) {
        // In FIPS 140-only mode, a password hash it refuses: what to do instead (ADR-0060).
        setError(problem.detail);
      } else {
        setError(t("signIn.failed"));
      }
      setPassword("");
    } catch {
      setError(t("signIn.unreachable"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Screen
      title={t("signIn.title", { name })}
      lead={t(problem === "api" ? "signIn.lead.api" : provider ? "signIn.lead.provider" : "signIn.lead.noProvider")}
    >
      {problem === "api" && (
        <Button variant="primary" className="w-full" onClick={() => globalThis.location.reload()}>
          <RotateCw aria-hidden /> {t("signIn.retry")}
        </Button>
      )}
      {providerError && <Alert>{providerError}</Alert>}
      {provider && (
        <>
          <Button
            variant={problem === "api" ? "secondary" : "primary"}
            className="w-full"
            onClick={() => void signInWithProvider(target()).catch(() => setProviderError(providerDown))}
          >
            <LogIn aria-hidden /> {t("signIn.sso")}
          </Button>
          <div className="flex items-center gap-3 text-secondary text-faint" aria-hidden>
            <span className="h-px flex-1 bg-line" />
            {t("signIn.or")}
            <span className="h-px flex-1 bg-line" />
          </div>
        </>
      )}
      <form
        aria-label={t("signIn.localForm")}
        className="flex flex-col gap-3.5"
        onSubmit={(e) => void submit(e)}
        noValidate
      >
        <Field label={t("signIn.username")} htmlFor="sign-in-username">
          <Input
            id="sign-in-username"
            name="username"
            autoComplete="username"
            autoCapitalize="none"
            spellCheck={false}
            required
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </Field>
        <Field label={t("signIn.password")} htmlFor="sign-in-password">
          <Input
            id="sign-in-password"
            name="password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
        {error && <Alert>{error}</Alert>}
        <Left seconds={wait.left} />
        <Button
          type="submit"
          variant={provider || problem === "api" ? "secondary" : "primary"}
          className="w-full"
          disabled={busy || wait.left > 0 || username.trim() === "" || password === ""}
        >
          <KeyRound aria-hidden /> {t(provider ? "signIn.asLocal" : "signIn.submit")}
        </Button>
      </form>
    </Screen>
  );
}

const MIN_LENGTH = 12;

export function ChangePasswordPage() {
  const { t } = useTranslation("account");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [repeat, setRepeat] = useState("");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const wait = useWait();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    const local: Record<string, string> = {};
    if (next.length < MIN_LENGTH) local.new = t("password.tooShort", { min: MIN_LENGTH });
    else if (repeat !== next) local.repeat = t("password.differ");
    setErrors(local);
    setError(null);
    if (Object.keys(local).length > 0) return;
    setBusy(true);
    try {
      const { response, error: body } = await api.POST("/auth/password", {
        body: { current_password: current, new_password: next },
      });
      if (response.ok) {
        globalThis.location.assign(target());
        return;
      }
      const problem: Problem | undefined = body;
      if (response.status === 422 && problem?.errors) {
        const byField: Record<string, string> = {};
        for (const fe of problem.errors) {
          byField[fe.field === "/current_password" ? "current" : "new"] = fe.message;
        }
        setErrors(byField);
      } else if (response.status === 429) {
        wait.start(Number(response.headers.get("Retry-After") ?? "1") || 1);
        setError(t("signIn.tooMany"));
      } else if (response.status === 401) {
        // The session ended: the sign-in page again.
        globalThis.location.assign("/");
      } else {
        setError(problem?.detail ?? t("password.failed"));
      }
    } catch {
      setError(t("signIn.unreachable"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Screen title={t("password.title")} lead={t("password.lead")}>
      <form
        aria-label={t("password.form")}
        className="flex flex-col gap-3.5"
        onSubmit={(e) => void submit(e)}
        noValidate
      >
        {/* For password managers: the account this password belongs to. */}
        <input type="text" name="username" autoComplete="username" value="admin" readOnly hidden />
        <Field label={t("password.current")} htmlFor="password-current" error={errors.current}>
          <Input
            id="password-current"
            type="password"
            autoComplete="current-password"
            required
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </Field>
        <Field
          label={t("password.new")}
          htmlFor="password-new"
          error={errors.new}
          hint={t("password.newHint", { min: MIN_LENGTH })}
        >
          <Input
            id="password-new"
            type="password"
            autoComplete="new-password"
            required
            minLength={MIN_LENGTH}
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        </Field>
        <Field label={t("password.repeat")} htmlFor="password-repeat" error={errors.repeat}>
          <Input
            id="password-repeat"
            type="password"
            autoComplete="new-password"
            required
            value={repeat}
            onChange={(e) => setRepeat(e.target.value)}
          />
        </Field>
        {error && <Alert>{error}</Alert>}
        <Left seconds={wait.left} />
        <div className="flex items-center gap-2">
          <Button type="submit" variant="primary" disabled={busy || wait.left > 0 || current === "" || next === ""}>
            {t("password.submit")}
          </Button>
          <Button type="button" variant="quiet" onClick={() => void signOut()}>
            {t("password.signOut")}
          </Button>
        </div>
      </form>
    </Screen>
  );
}

/**
 * The window of a provider's trial sign-in, once it is over (ADR-0030): the
 * token went back to the Administration page, which shows what rvav reads
 * from it; this window closes.
 */
export function TrialWindowPage({ result = trialResult() }: { result?: { token?: string; error?: string } }) {
  const { t } = useTranslation("account");
  useEffect(() => {
    if (!result.token) return;
    const t = setTimeout(() => globalThis.close(), 1200);
    return () => clearTimeout(t);
  }, [result.token]);
  return (
    <Screen title={t("trial.title")} lead={t("trial.lead")}>
      {result.token ? (
        <p role="status" className="text-body">
          {t("trial.signedIn")}
        </p>
      ) : (
        <Alert>{t("trial.failed", { reason: result.error ?? t("trial.noToken") })}</Alert>
      )}
    </Screen>
  );
}
