// Signing in (ADR-0010, ADR-0030): with the identity provider (authorization
// code + PKCE, tokens kept in memory only), or as the local administrator,
// whose session is a cookie the API sets. The way chosen is remembered in
// the browser, so the provider's users go straight to it next time.
// The provider is the one the API says (set in Administration); the web
// container's RVAV_WEB_OIDC_* win over it.
// In mock mode there is no identity provider: a fixed development token is used.
// With VITE_AUTH_MODE=none no token is sent (local Go API in dev auth mode).
import type { UserManager } from "oidc-client-ts";
import type { components } from "@/lib/api/schema";
import { env, isMock } from "./env";

const MOCK_TOKEN = "mock-dev-token";
const CALLBACK_PATH = "/auth/callback";
/** Where the local administrator changes the password, when asked to or not. */
export const CHANGE_PASSWORD_PATH = "/change-password";
/** The sign-in page, as a path for mock mode and the browser tests. */
export const SIGN_IN_PATH = "/sign-in";
/** The window a provider's trial sign-in runs in (Administration). */
export const TRIAL_PATH = "/auth/trial";
/** Where the trial window sends its token, or why there is none. */
export const TRIAL_CHANNEL = "rvav.oidc-trial";
const METHOD_KEY = "rvav.signInMethod";
// The trial's settings, handed to its window (localStorage), then kept by
// that window across the provider's pages (sessionStorage).
const TRIAL_KEY = "rvav.oidcTrial";

export type SignInMethod = "provider" | "local";

/** How the browser signs in at the provider. */
export interface ProviderConfig {
  authority: string;
  clientId: string;
  scope: string;
}

/** What the trial window sends back. */
export interface TrialMessage {
  token?: string;
  error?: string;
}

const noSignIn = isMock || env.authMode === "none";

let manager: UserManager | null = null;
let renewing: Promise<void> | null = null;
let method: SignInMethod | null = null;
let provider: ProviderConfig | null = null;
let trialOutcome: TrialMessage = {};

/** The provider settings: the web container's win over the API's. */
export function providerConfig(api: components["schemas"]["AuthProvider"] | undefined): ProviderConfig | null {
  const authority = env.oidcAuthority || api?.authority || "";
  if (!authority) return null;
  return {
    authority,
    clientId: env.oidcClientId || api?.client_id || "rvav",
    scope: env.oidcScope || api?.scope || "openid profile email",
  };
}

/** The sign-in settings the web container sets, which lock their fields in Administration. */
export function webOverrides(): Partial<Record<"issuer" | "client_id" | "scopes", { env: string; value: string }>> {
  return {
    ...(env.oidcAuthority ? { issuer: { env: "RVAV_WEB_OIDC_AUTHORITY", value: env.oidcAuthority } } : {}),
    ...(env.oidcClientId ? { client_id: { env: "RVAV_WEB_OIDC_CLIENT_ID", value: env.oidcClientId } } : {}),
    ...(env.oidcScope ? { scopes: { env: "RVAV_WEB_OIDC_SCOPE", value: env.oidcScope } } : {}),
  };
}

/** Whether an identity provider is set (in mock mode, one is). */
export const hasProvider = () => isMock || provider !== null;

/** Reads the provider the API says; without an answer, the web container's only. */
async function loadOptions() {
  try {
    const res = await fetch("/api/v1/auth/options", { headers: { "X-Requested-With": "rvav" } });
    if (res.ok) {
      const opts = (await res.json()) as components["schemas"]["AuthOptions"];
      provider = providerConfig(opts.provider);
      return;
    }
  } catch {
    // The API is down: the app says so once it loads.
  }
  provider = providerConfig(undefined);
}

function remembered(): SignInMethod | null {
  try {
    const m = globalThis.localStorage?.getItem(METHOD_KEY);
    return m === "provider" || m === "local" ? m : null;
  } catch {
    return null;
  }
}

function remember(m: SignInMethod | null) {
  method = m;
  try {
    if (m) globalThis.localStorage?.setItem(METHOD_KEY, m);
    else globalThis.localStorage?.removeItem(METHOD_KEY);
  } catch {
    // Storage refused (private mode): the choice lasts this visit only.
  }
}

async function createManager(p: ProviderConfig, renew = true) {
  // Loaded on demand, so mock and test builds don't carry the OIDC client.
  const { InMemoryWebStorage, UserManager, WebStorageStateStore } = await import("oidc-client-ts");
  const origin = globalThis.location.origin;
  return new UserManager({
    authority: p.authority,
    client_id: p.clientId,
    redirect_uri: origin + CALLBACK_PATH,
    post_logout_redirect_uri: origin + "/",
    response_type: "code",
    scope: p.scope,
    automaticSilentRenew: renew,
    userStore: new WebStorageStateStore({ store: new InMemoryWebStorage() }),
  });
}

/** What the local session says about who is signed in, if anyone. */
async function localSession(): Promise<{ passwordChangeRequired: boolean } | null> {
  try {
    const res = await fetch("/api/v1/me", { credentials: "same-origin", headers: { "X-Requested-With": "rvav" } });
    if (!res.ok) return null;
    const me = (await res.json()) as { local?: boolean; password_change_required?: boolean };
    return me.local ? { passwordChangeRequired: me.password_change_required === true } : null;
  } catch {
    return null;
  }
}

const here = () => globalThis.location.pathname + globalThis.location.search;

/**
 * Call once before rendering. Resolves to `ready`; to `redirecting` when the
 * browser is on its way to the identity provider; to `sign-in` when nobody
 * is signed in; to `change-password` when the local administrator must
 * change the password (or asked to); or to `trial` in the window of a
 * provider's trial sign-in, once it is over.
 */
export async function initAuth(): Promise<"ready" | "redirecting" | "sign-in" | "change-password" | "trial"> {
  const path = globalThis.location.pathname;
  if (path === TRIAL_PATH) return startTrial();
  if (path === CALLBACK_PATH && trialSettings()) return finishTrial();
  if (isMock) {
    // The pages are reachable in mock mode, for the browser tests.
    if (path === SIGN_IN_PATH) return "sign-in";
    if (path === CHANGE_PASSWORD_PATH) return "change-password";
    return "ready";
  }
  if (noSignIn) return "ready";
  await loadOptions();

  if (path === CALLBACK_PATH && provider) {
    manager = await createManager(provider);
    const user = await manager.signinRedirectCallback();
    remember("provider");
    const target = typeof user.state === "string" && user.state.startsWith("/") ? user.state : "/";
    globalThis.history.replaceState({}, "", target);
    return "ready";
  }

  const local = await localSession();
  if (local) {
    method = "local";
    if (local.passwordChangeRequired || path === CHANGE_PASSWORD_PATH) return "change-password";
    if (path === SIGN_IN_PATH) globalThis.history.replaceState({}, "", "/");
    return "ready";
  }

  if (remembered() === "provider" && provider) {
    method = "provider";
    manager = await createManager(provider);
    const user = await manager.getUser();
    if (!user || user.expired) {
      await manager.signinRedirect({ state: path === SIGN_IN_PATH ? "/" : here() });
      return "redirecting";
    }
    return "ready";
  }
  return "sign-in";
}

/** Go to the identity provider, back to `target` afterwards. */
export async function signInWithProvider(target = "/") {
  if (isMock || !provider) {
    globalThis.location.assign(target);
    return;
  }
  remember("provider");
  manager ??= await createManager(provider);
  await manager.signinRedirect({ state: target });
}

/** The local administrator signed in: the cookie holds the session. */
export function signedInLocally() {
  remember("local");
}

export async function getAccessToken(): Promise<string | null> {
  if (isMock) return MOCK_TOKEN;
  if (noSignIn || method !== "provider") return null;
  const user = await manager?.getUser();
  return user?.access_token ?? null;
}

/**
 * On a 401: for the provider, try one silent renew, and if that fails, go
 * back to it; for a local session that ended, back to the sign-in page.
 */
export async function handleUnauthorized() {
  if (noSignIn) return;
  if (method === "local") {
    globalThis.location.reload();
    return;
  }
  if (!manager) return;
  const m = manager;
  renewing ??= (async () => {
    try {
      await m.signinSilent();
    } catch {
      await m.signinRedirect({ state: here() });
    } finally {
      renewing = null;
    }
  })();
  await renewing;
}

export async function signOut() {
  if (isMock) {
    globalThis.location.assign(SIGN_IN_PATH);
    return;
  }
  if (noSignIn) return;
  const was = method;
  remember(null);
  if (was === "local") {
    await fetch("/api/v1/auth/sign-out", {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-Requested-With": "rvav" },
    }).catch(() => undefined);
    globalThis.location.assign("/");
    return;
  }
  if (manager) await manager.signoutRedirect();
}

// ── The provider's trial sign-in (ADR-0030) ──
// The page may only reach the provider's origin (Content-Security-Policy),
// so the trial runs in a window of its own, loaded after the test let the
// page reach the provider tried. That window signs in there, and sends the
// access token back over a BroadcastChannel; the Administration page asks
// the API what it reads from it.

/**
 * Opens the window of a trial sign-in with these settings; null when the
 * browser blocked it.
 */
export function openTrial(p: ProviderConfig): Window | null {
  globalThis.localStorage.setItem(TRIAL_KEY, JSON.stringify(p));
  const w = globalThis.open(TRIAL_PATH, "rvav-oidc-trial", "popup,width=520,height=720");
  if (!w) globalThis.localStorage.removeItem(TRIAL_KEY);
  return w;
}

function trialSettings(): ProviderConfig | null {
  try {
    const raw = globalThis.sessionStorage.getItem(TRIAL_KEY);
    return raw ? (JSON.parse(raw) as ProviderConfig) : null;
  } catch {
    return null;
  }
}

function sendTrial(m: TrialMessage) {
  trialOutcome = m;
  if (typeof BroadcastChannel === "undefined") return;
  const ch = new BroadcastChannel(TRIAL_CHANNEL);
  ch.postMessage(m);
  ch.close();
}

async function startTrial(): Promise<"redirecting" | "trial"> {
  const raw = globalThis.localStorage.getItem(TRIAL_KEY);
  globalThis.localStorage.removeItem(TRIAL_KEY);
  if (!raw) {
    sendTrial({ error: "The trial's settings are missing: start it again from Administration." });
    return "trial";
  }
  if (isMock) {
    // No provider to go to: the mock's token.
    sendTrial({ token: "mock-trial-token" });
    return "trial";
  }
  globalThis.sessionStorage.setItem(TRIAL_KEY, raw);
  try {
    const m = await createManager(JSON.parse(raw) as ProviderConfig, false);
    await m.signinRedirect({ state: "trial" });
    return "redirecting";
  } catch (e) {
    globalThis.sessionStorage.removeItem(TRIAL_KEY);
    sendTrial({ error: e instanceof Error ? e.message : String(e) });
    return "trial";
  }
}

async function finishTrial(): Promise<"trial"> {
  const p = trialSettings()!;
  globalThis.sessionStorage.removeItem(TRIAL_KEY);
  try {
    const user = await (await createManager(p, false)).signinRedirectCallback();
    sendTrial({ token: user.access_token });
  } catch (e) {
    // The provider's refusal (error_description), or the token request's.
    sendTrial({ error: e instanceof Error ? e.message : String(e) });
  }
  globalThis.history.replaceState({}, "", TRIAL_PATH);
  return "trial";
}

/** How the trial went, for its window. */
export const trialResult = (): TrialMessage => trialOutcome;
