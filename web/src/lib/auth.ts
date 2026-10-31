// Signing in (ADR-0010, ADR-0030): with the identity provider (authorization
// code + PKCE, tokens kept in memory only), or as the local administrator,
// whose session is a cookie the API sets. The way chosen is remembered in
// the browser, so the provider's users go straight to it next time.
// In mock mode there is no identity provider: a fixed development token is used.
// With VITE_AUTH_MODE=none no token is sent (local Go API in dev auth mode).
import type { UserManager } from "oidc-client-ts";
import { env, isMock } from "./env";

const MOCK_TOKEN = "mock-dev-token";
const CALLBACK_PATH = "/auth/callback";
/** Where the local administrator changes the password, when asked to or not. */
export const CHANGE_PASSWORD_PATH = "/change-password";
/** The sign-in page, as a path for mock mode and the browser tests. */
export const SIGN_IN_PATH = "/sign-in";
const METHOD_KEY = "rvav.signInMethod";

export type SignInMethod = "provider" | "local";

const noSignIn = isMock || env.authMode === "none";

let manager: UserManager | null = null;
let renewing: Promise<void> | null = null;
let method: SignInMethod | null = null;

/** Whether an identity provider is set for the web app (in mock mode, one is). */
export const hasProvider = () => isMock || env.oidcAuthority !== "";

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

async function createManager() {
  // Loaded on demand, so mock and test builds don't carry the OIDC client.
  const { InMemoryWebStorage, UserManager, WebStorageStateStore } = await import("oidc-client-ts");
  const origin = globalThis.location.origin;
  return new UserManager({
    authority: env.oidcAuthority,
    client_id: env.oidcClientId,
    redirect_uri: origin + CALLBACK_PATH,
    post_logout_redirect_uri: origin + "/",
    response_type: "code",
    scope: env.oidcScope,
    automaticSilentRenew: true,
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
 * is signed in; or to `change-password` when the local administrator must
 * change the password (or asked to).
 */
export async function initAuth(): Promise<"ready" | "redirecting" | "sign-in" | "change-password"> {
  const path = globalThis.location.pathname;
  if (isMock) {
    // The pages are reachable in mock mode, for the browser tests.
    if (path === SIGN_IN_PATH) return "sign-in";
    if (path === CHANGE_PASSWORD_PATH) return "change-password";
    return "ready";
  }
  if (noSignIn) return "ready";

  if (path === CALLBACK_PATH && hasProvider()) {
    manager = await createManager();
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

  if (remembered() === "provider" && hasProvider()) {
    method = "provider";
    manager = await createManager();
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
  if (isMock) {
    globalThis.location.assign(target);
    return;
  }
  remember("provider");
  manager ??= await createManager();
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
