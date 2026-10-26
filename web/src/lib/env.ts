const e = import.meta.env;

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
  authMode: e.VITE_AUTH_MODE === "dev" ? "none" : (e.VITE_AUTH_MODE ?? "oidc"),
  oidcAuthority: e.VITE_OIDC_AUTHORITY ?? "",
  oidcClientId: e.VITE_OIDC_CLIENT_ID ?? "rvav",
  /**
   * No `offline_access` by default: tokens stay in memory and renew with the
   * normal refresh token. Add it for providers that only issue refresh tokens
   * with it (Entra ID).
   */
  oidcScope: e.VITE_OIDC_SCOPE ?? "openid profile email",
  mockTickMs: Number(e.VITE_MOCK_TICK_MS ?? 900),
  mockLatencyMs: Number(e.VITE_MOCK_LATENCY_MS ?? 180),
} as const;

export const isMock = env.apiMode === "mock";
