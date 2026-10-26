/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_MODE?: "mock" | "real";
  readonly VITE_MOCK_TRANSPORT?: "worker" | "inline";
  readonly VITE_ROUTER_HISTORY?: "browser" | "memory";
  readonly VITE_AUTH_MODE?: "oidc" | "none" | "dev";
  readonly VITE_OIDC_AUTHORITY?: string;
  readonly VITE_OIDC_CLIENT_ID?: string;
  readonly VITE_OIDC_SCOPE?: string;
  readonly VITE_MOCK_TICK_MS?: string;
  readonly VITE_MOCK_LATENCY_MS?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
