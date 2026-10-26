const e = import.meta.env;

export const env = {
  /** `mock` runs the MSW handlers instead of calling the Go API. */
  apiMode: e.VITE_API_MODE ?? (e.DEV ? "mock" : "real"),
  /** `worker` uses the MSW service worker; `inline` runs handlers in-process (single-file mockup build). */
  mockTransport: e.VITE_MOCK_TRANSPORT ?? "worker",
  history: e.VITE_ROUTER_HISTORY ?? "browser",
  oidcAuthority: e.VITE_OIDC_AUTHORITY ?? "",
  oidcClientId: e.VITE_OIDC_CLIENT_ID ?? "rvav-web",
  mockTickMs: Number(e.VITE_MOCK_TICK_MS ?? 900),
  mockLatencyMs: Number(e.VITE_MOCK_LATENCY_MS ?? 180),
} as const;

export const isMock = env.apiMode === "mock";
