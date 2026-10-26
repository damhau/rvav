// OIDC sign-in (authorization code + PKCE) with tokens kept in memory only.
// In mock mode there is no identity provider: a fixed development token is used.
// With VITE_AUTH_MODE=none no token is sent (local Go API in dev auth mode).
import type { UserManager } from "oidc-client-ts";
import { env, isMock } from "./env";

const MOCK_TOKEN = "mock-dev-token";
const CALLBACK_PATH = "/auth/callback";

const noSignIn = isMock || env.authMode === "none";

let manager: UserManager | null = null;
let renewing: Promise<void> | null = null;

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
    scope: "openid profile email offline_access",
    automaticSilentRenew: true,
    userStore: new WebStorageStateStore({ store: new InMemoryWebStorage() }),
  });
}

/**
 * Call once before rendering. Resolves to `ready`, or to `redirecting` when the
 * browser is on its way to the identity provider.
 */
export async function initAuth(): Promise<"ready" | "redirecting"> {
  if (noSignIn) return "ready";
  manager = await createManager();
  if (globalThis.location.pathname === CALLBACK_PATH) {
    const user = await manager.signinRedirectCallback();
    const target = typeof user.state === "string" && user.state.startsWith("/") ? user.state : "/";
    globalThis.history.replaceState({}, "", target);
    return "ready";
  }
  const user = await manager.getUser();
  if (!user || user.expired) {
    await manager.signinRedirect({ state: globalThis.location.pathname + globalThis.location.search });
    return "redirecting";
  }
  return "ready";
}

export async function getAccessToken(): Promise<string | null> {
  if (isMock) return MOCK_TOKEN;
  if (noSignIn) return null;
  const user = await manager?.getUser();
  return user?.access_token ?? null;
}

/** On a 401: try one silent renew, and if that fails, go back to the identity provider. */
export async function handleUnauthorized() {
  if (noSignIn || !manager) return;
  const m = manager;
  renewing ??= (async () => {
    try {
      await m.signinSilent();
    } catch {
      await m.signinRedirect({ state: globalThis.location.pathname + globalThis.location.search });
    } finally {
      renewing = null;
    }
  })();
  await renewing;
}

export async function signOut() {
  if (noSignIn || !manager) return;
  await manager.signoutRedirect();
}
