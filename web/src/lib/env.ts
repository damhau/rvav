const e = import.meta.env;

export const env = {
  /** `mock` runs the MSW handlers instead of calling the Go API. */
  apiMode: e.VITE_API_MODE ?? (e.DEV ? "mock" : "real"),
  /** `worker` uses the MSW service worker; `inline` runs handlers in-process (single-file mockup build). */
  mockTransport: e.VITE_MOCK_TRANSPORT ?? "worker",
  history: e.VITE_ROUTER_HISTORY ?? "browser",
  /**
   * `oidc` signs in with the identity provider. `none` sends no token, for a
   * local Go API started with RVAV_AUTH_MODE=dev. Mock mode never signs in.
   */
  authMode: e.VITE_AUTH_MODE ?? "oidc",
  oidcAuthority: e.VITE_OIDC_AUTHORITY ?? "",
  oidcClientId: e.VITE_OIDC_CLIENT_ID ?? "rvav-web",
  mockTickMs: Number(e.VITE_MOCK_TICK_MS ?? 900),
  mockLatencyMs: Number(e.VITE_MOCK_LATENCY_MS ?? 180),
} as const;

export const isMock = env.apiMode === "mock";
