import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Outlet } from "@tanstack/react-router";
import { Toaster } from "sonner";
import { useTranslation } from "react-i18next";
import { PageNotFound } from "@/components/common/blocked";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { AppShell } from "@/components/shell/AppShell";
import { TooltipProvider } from "@/components/ui/tooltip";
import { useBranding } from "@/lib/branding";
import { LiveProvider } from "@/lib/live/LiveProvider";
import { ProjectProvider } from "@/lib/project";

export interface RouterContext {
  queryClient: QueryClient;
}

function Boot({ state }: { state: "loading" | Error }) {
  const { t } = useTranslation();
  const { name } = useBranding();
  return (
    <div className="flex h-full items-center justify-center p-6">
      {state === "loading" ? (
        <p className="text-muted" role="status">
          {t("loadingApp", { name })}
        </p>
      ) : (
        <ErrorState
          error={state}
          onRetry={() => globalThis.location.reload()}
          className="max-w-lg rounded-lg border border-line bg-surface"
        />
      )}
    </div>
  );
}

function Root() {
  return (
    <TooltipProvider delayDuration={300}>
      <ProjectProvider fallback={(s) => <Boot state={s} />}>
        <LiveProvider>
          <AppShell>
            <Outlet />
          </AppShell>
        </LiveProvider>
      </ProjectProvider>
      <Toaster
        position="bottom-right"
        toastOptions={{
          classNames: {
            toast: "!rounded-lg !border !border-line !bg-surface !text-fg !shadow-pop !font-sans !text-body",
            description: "!text-muted !text-secondary",
            actionButton: "!bg-accent !text-accent-fg",
          },
        }}
      />
    </TooltipProvider>
  );
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: Root,
  notFoundComponent: PageNotFound,
  errorComponent: ({ error, reset }) => (
    <Page>
      <ErrorState error={error} onRetry={reset} className="rounded-lg border border-line bg-surface" />
    </Page>
  ),
});
