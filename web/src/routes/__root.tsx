import type { QueryClient } from "@tanstack/react-query";
import { createRootRouteWithContext, Link, Outlet } from "@tanstack/react-router";
import { Toaster } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page, PageHeader, Panel } from "@/components/common/page";
import { AppShell } from "@/components/shell/AppShell";
import { TooltipProvider } from "@/components/ui/tooltip";
import { LiveProvider } from "@/lib/live/LiveProvider";
import { ProjectProvider } from "@/lib/project";

export interface RouterContext {
  queryClient: QueryClient;
}

function Boot({ state }: { state: "loading" | Error }) {
  return (
    <div className="flex h-full items-center justify-center p-6">
      {state === "loading" ? (
        <p className="text-muted" role="status">
          Loading rvav…
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
            toast: "!rounded-lg !border !border-line !bg-surface !text-fg !shadow-pop !font-sans !text-[13px]",
            description: "!text-muted !text-xs",
            actionButton: "!bg-accent !text-accent-fg",
          },
        }}
      />
    </TooltipProvider>
  );
}

export const Route = createRootRouteWithContext<RouterContext>()({
  component: Root,
  notFoundComponent: () => (
    <Page>
      <PageHeader title="Page not found" />
      <Panel>
        <p className="text-muted">
          This address doesn't match anything in rvav. Go back to the <Link to="/">overview</Link>.
        </p>
      </Panel>
    </Page>
  ),
  errorComponent: ({ error, reset }) => (
    <Page>
      <ErrorState error={error} onRetry={reset} className="rounded-lg border border-line bg-surface" />
    </Page>
  ),
});
