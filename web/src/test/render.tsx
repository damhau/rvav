import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { createAppRouter } from "@/router";

export function testQueryClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 }, mutations: { retry: false } } });
}

export function renderWithQuery(ui: ReactNode, client = testQueryClient()) {
  return { client, ...render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>) };
}

/**
 * Render the whole app (shell, providers, routes) at a path, against the mock API. `strict` renders it in
 * StrictMode, as main.tsx does, for effects that must run once.
 */
export function renderApp(path: string, client = testQueryClient(), { strict = false }: { strict?: boolean } = {}) {
  const router = createAppRouter(client, path);
  return {
    client,
    router,
    ...render(
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
      { reactStrictMode: strict },
    ),
  };
}
