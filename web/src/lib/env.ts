const e = import.meta.env;

/** Settings given at run time by /config.js (the web image's environment,
 * S14.2); the build's VITE_* values otherwise. */
const runtime: Partial<Record<"authMode" | "oidcAuthority" | "oidcClientId" | "oidcScope", string>> =
  (typeof window !== "undefined" && (window as { __RVAV_CONFIG__?: Record<string, string> }).__RVAV_CONFIG__) || {};
const pick = (v: string | undefined, fallback: string | undefined) => (v ? v : fallback);

export const env = {
  /** `mock` runs the MSW handlers instead of calling the Go API. */
  apiMode: e.VITE_API_MODE ?? (e.DEV ? "mock" : "real"),
  /** `worker` uses the MSW service worker; `inline` runs handlers in-process (single-file mockup build). */
  mockTransport: e.VITE_MOCK_TRANSPORT ?? "worker",
  history: e.VITE_ROUTER_HISTORY ?? "browser",
  /**
   * `oidc` signs in with the identity provider. `none` (or `dev`, so it can
   * follow RVAV_AUTH_MODE) sends no token, for a Go API in dev auth mode.
   * Mock mode never signs in.
   */
  authMode: (() => {
    const m = pick(runtime.authMode, e.VITE_AUTH_MODE);
    return m === "dev" ? "none" : (m ?? "oidc");
  })(),
  oidcAuthority: pick(runtime.oidcAuthority, e.VITE_OIDC_AUTHORITY) ?? "",
  oidcClientId: pick(runtime.oidcClientId, e.VITE_OIDC_CLIENT_ID) ?? "rvav",
  /**
   * No `offline_access` by default: tokens stay in memory and renew with the
   * normal refresh token. Add it for providers that only issue refresh tokens
   * with it (Entra ID).
   */
  oidcScope: pick(runtime.oidcScope, e.VITE_OIDC_SCOPE) ?? "openid profile email",
  mockTickMs: Number(e.VITE_MOCK_TICK_MS ?? 900),
  mockLatencyMs: Number(e.VITE_MOCK_LATENCY_MS ?? 180),
} as const;

export const isMock = env.apiMode === "mock";
