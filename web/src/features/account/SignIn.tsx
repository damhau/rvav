// The sign-in page and the local administrator's password change (ADR-0030).
// Both are shown before the app, outside its router: nobody is signed in
// yet, or the session may do nothing but change the password.
import { KeyRound, LogIn } from "lucide-react";
import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import type { Problem } from "@/lib/api/types";
import {
  CHANGE_PASSWORD_PATH,
  SIGN_IN_PATH,
  hasProvider,
  signInWithProvider,
  signOut,
  signedInLocally,
  trialResult,
} from "@/lib/auth";
import { Button } from "@/components/ui/button";
import { Field, Input } from "@/components/ui/input";

/** Where to go once signed in: the page asked for, or the overview. */
function target() {
  const { pathname, search } = globalThis.location;
  return pathname === SIGN_IN_PATH || pathname === CHANGE_PASSWORD_PATH ? "/" : pathname + search;
}

function Screen({ title, lead, children }: { title: string; lead: React.ReactNode; children: React.ReactNode }) {
  return (
    <main className="flex min-h-dvh items-start justify-center bg-bg px-4 pt-[12vh] pb-10 text-fg">
      <div className="flex w-full max-w-[360px] flex-col gap-6">
        <div className="flex items-center gap-2">
          <span
            aria-hidden
            className="flex size-[22px] items-center justify-center rounded-sm bg-fg font-mono text-[11px] font-medium text-bg"
          >
            rv
          </span>
          <span className="text-sm font-semibold tracking-[-0.01em]">rvav</span>
        </div>
        <div className="flex flex-col gap-1.5">
          <h1 className="text-lg font-semibold tracking-[-0.01em]">{title}</h1>
          <div className="text-[13px] text-muted">{lead}</div>
        </div>
        {children}
      </div>
    </main>
  );
}

function Alert({ children }: { children: React.ReactNode }) {
  return (
    <p role="alert" className="rounded-md border border-bad/40 bg-bad-soft px-3 py-2 text-[13px] text-bad">
      {children}
    </p>
  );
}

const unreachable = "Can't reach the rvav API. Check your connection and try again.";

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

export function SignInPage({ provider = hasProvider() }: { provider?: boolean }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const wait = useWait();

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { data, response } = await api.POST("/auth/sign-in", { body: { username, password } });
      if (data) {
        signedInLocally();
        globalThis.location.assign(data.password_change_required ? CHANGE_PASSWORD_PATH : target());
        return;
      }
      if (response.status === 429) {
        const seconds = Number(response.headers.get("Retry-After") ?? "1") || 1;
        wait.start(seconds);
        setError("Too many failed attempts in a row. Wait before trying again.");
      } else if (response.status === 401) {
        setError("The username or password is wrong.");
      } else {
        setError("Signing in didn't work. Try again in a moment.");
      }
      setPassword("");
    } catch {
      setError(unreachable);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Screen
      title="Sign in to rvav"
      lead={
        provider
          ? "Sign in with your organisation's account."
          : "No identity provider is set up yet. Sign in as the local administrator to set one up in Administration."
      }
    >
      {provider && (
        <>
          <Button variant="primary" className="w-full" onClick={() => void signInWithProvider(target())}>
            <LogIn aria-hidden /> Sign in with single sign-on
          </Button>
          <div className="flex items-center gap-3 text-xs text-faint" aria-hidden>
            <span className="h-px flex-1 bg-line" />
            or, when single sign-on is down
            <span className="h-px flex-1 bg-line" />
          </div>
        </>
      )}
      <form
        aria-label="Local administrator"
        className="flex flex-col gap-3.5"
        onSubmit={(e) => void submit(e)}
        noValidate
      >
        <Field label="Username" htmlFor="sign-in-username">
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
        <Field label="Password" htmlFor="sign-in-password">
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
        {error && (
          <Alert>
            {error}
            {wait.left > 0 && ` ${wait.left === 1 ? "1 second" : `${wait.left} seconds`} left.`}
          </Alert>
        )}
        <Button
          type="submit"
          variant={provider ? "secondary" : "primary"}
          className="w-full"
          disabled={busy || wait.left > 0 || username.trim() === "" || password === ""}
        >
          <KeyRound aria-hidden /> {provider ? "Sign in as the local administrator" : "Sign in"}
        </Button>
      </form>
    </Screen>
  );
}

const MIN_LENGTH = 12;

export function ChangePasswordPage() {
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
    if (next.length < MIN_LENGTH) local.new = `Use at least ${MIN_LENGTH} characters.`;
    else if (repeat !== next) local.repeat = "The two new passwords differ.";
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
        setError("Too many failed attempts in a row. Wait before trying again.");
      } else if (response.status === 401) {
        // The session ended: the sign-in page again.
        globalThis.location.assign("/");
      } else {
        setError(problem?.detail ?? "Changing the password didn't work. Try again in a moment.");
      }
    } catch {
      setError(unreachable);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Screen
      title="Change the administrator's password"
      lead="Choose your own password before going on: the one you signed in with was given at installation, or by a reset."
    >
      <form aria-label="Change password" className="flex flex-col gap-3.5" onSubmit={(e) => void submit(e)} noValidate>
        {/* For password managers: the account this password belongs to. */}
        <input type="text" name="username" autoComplete="username" value="admin" readOnly hidden />
        <Field label="Current password" htmlFor="password-current" error={errors.current}>
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
          label="New password"
          htmlFor="password-new"
          error={errors.new}
          hint={`At least ${MIN_LENGTH} characters, other than the current one.`}
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
        <Field label="New password again" htmlFor="password-repeat" error={errors.repeat}>
          <Input
            id="password-repeat"
            type="password"
            autoComplete="new-password"
            required
            value={repeat}
            onChange={(e) => setRepeat(e.target.value)}
          />
        </Field>
        {error && (
          <Alert>
            {error}
            {wait.left > 0 && ` ${wait.left === 1 ? "1 second" : `${wait.left} seconds`} left.`}
          </Alert>
        )}
        <div className="flex items-center gap-2">
          <Button type="submit" variant="primary" disabled={busy || wait.left > 0 || current === "" || next === ""}>
            Change password
          </Button>
          <Button type="button" variant="ghost" onClick={() => void signOut()}>
            Sign out
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
  useEffect(() => {
    if (!result.token) return;
    const t = setTimeout(() => globalThis.close(), 1200);
    return () => clearTimeout(t);
  }, [result.token]);
  return (
    <Screen
      title="Trial sign-in"
      lead="Signing in at the identity provider with the settings being tried, before they are saved."
    >
      {result.token ? (
        <p role="status" className="text-[13px]">
          Signed in at the provider. What rvav reads from the token is on the Administration page; this window closes.
        </p>
      ) : (
        <Alert>The trial sign-in failed: {result.error ?? "no token came back."}</Alert>
      )}
    </Screen>
  );
}
