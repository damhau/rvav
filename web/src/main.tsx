import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { isApiError } from "@/lib/api/client";
import { initAuth } from "@/lib/auth";
import { isMock } from "@/lib/env";
import { applyTheme, getStoredTheme } from "@/lib/theme";
import { createAppRouter } from "./router";
// Fonts served with the app, not from a third party (S14.6).
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "./styles.css";

export function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 30_000,
        // Client errors won't fix themselves; retry only network and server errors.
        retry: (count, error) => !(isApiError(error) && error.status >= 400 && error.status < 500) && count < 2,
      },
    },
  });
}

async function boot() {
  applyTheme(getStoredTheme());
  if (isMock) {
    const { startMocks } = await import("./mocks/browser");
    await startMocks();
  }
  const state = await initAuth();
  if (state === "redirecting") return;
  if (state === "sign-in" || state === "change-password") {
    // Before the app: nobody is signed in, or the local administrator must
    // change the password first (ADR-0030).
    const { ChangePasswordPage, SignInPage } = await import("@/features/account/SignIn");
    createRoot(document.getElementById("root")!).render(
      <StrictMode>{state === "sign-in" ? <SignInPage /> : <ChangePasswordPage />}</StrictMode>,
    );
    return;
  }

  const queryClient = createQueryClient();
  const router = createAppRouter(queryClient);
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </StrictMode>,
  );
}

void boot();
