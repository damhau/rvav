import type { QueryClient } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { env } from "@/lib/env";
import { routeTree } from "./routeTree.gen";

export function createAppRouter(queryClient: QueryClient, initialPath?: string) {
  return createRouter({
    routeTree,
    context: { queryClient },
    history:
      env.history === "memory" || initialPath
        ? createMemoryHistory({ initialEntries: [initialPath ?? "/"] })
        : undefined,
    defaultPreload: "intent",
    defaultPreloadStaleTime: 0,
    scrollRestoration: true,
  });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof createAppRouter>;
  }
}
