import { useQuery } from "@tanstack/react-query";
import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { Dialog } from "radix-ui";
import { lazy, Suspense, useEffect, useState, type ReactNode } from "react";
import { EmptyState, Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { useCurrentProject } from "@/lib/project";
import { adm } from "@/lib/query";
import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";

// The palette (and cmdk) loads the first time someone opens it.
const CommandPalette = lazy(() => import("./CommandPalette").then((m) => ({ default: m.CommandPalette })));

/** Pages that work without a project: approving, the requests one decides
 * on, API tokens and administration (an administrator setting up a new
 * install is in none yet; the server decides who may see each). */
const withoutProject = (path: string) =>
  path.startsWith("/approvals") || /^\/requests\/[^/]+$/.test(path) || path.startsWith("/admin/") || path === "/tokens";

function NoProject() {
  const { me } = useCurrentProject();
  if (me.global_roles.includes("admin")) {
    return (
      <Page>
        <EmptyState
          title="You are not in a project yet"
          body="Catalog, deployments and requests belong to projects. As an administrator, set rvav up: its connections, environments and projects, and a first catalog item."
          action={
            <Button variant="primary" size="sm" asChild>
              <Link to="/admin/setup">Set up rvav</Link>
            </Button>
          }
        />
      </Page>
    );
  }
  return (
    <Page>
      <EmptyState
        title="You are not in a project yet"
        body="Catalog, deployments and requests belong to projects. Ask a project admin to add you. You can still decide the approvals that wait for you."
        action={
          <Button variant="primary" size="sm" asChild>
            <Link to="/approvals">Open approvals</Link>
          </Button>
        }
      />
    </Page>
  );
}

// Administrators land on the setup checklist while it is open, once per page
// load (U1.5, ADR-0030): a new install's first sign-in.
let landedOnSetup = false;

function SetupLanding() {
  const { me } = useCurrentProject();
  const setup = useQuery({ ...adm.setup(), enabled: me.global_roles.includes("admin") && !landedOnSetup });
  const navigate = useNavigate();
  const open = setup.data?.open ?? false;
  useEffect(() => {
    if (open && !landedOnSetup) {
      landedOnSetup = true;
      void navigate({ to: "/admin/setup", replace: true });
    }
  }, [open, navigate]);
  return null;
}

export function AppShell({ children }: { children: ReactNode }) {
  const { projectId } = useCurrentProject();
  // The location whose pages are on screen: while the next page's code loads,
  // location is already the next one but the outlet still shows the last.
  const path = useRouterState({ select: (s) => (s.resolvedLocation ?? s.location).pathname });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [navOpen, setNavOpen] = useState(false);
  const [paletteLoaded, setPaletteLoaded] = useState(false);
  if (paletteOpen && !paletteLoaded) setPaletteLoaded(true);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  return (
    <div className="flex h-full min-h-0">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-surface px-3 py-2 focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        Skip to content
      </a>
      <aside className="hidden w-[216px] shrink-0 border-r border-line bg-rail md:block">
        <Sidebar />
      </aside>

      <Dialog.Root open={navOpen} onOpenChange={setNavOpen}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-40 bg-black/30 md:hidden" />
          <Dialog.Content
            className="fixed inset-y-0 left-0 z-50 w-[min(260px,85vw)] border-r border-line bg-rail md:hidden"
            style={{ paddingTop: "env(safe-area-inset-top, 0px)" }}
          >
            <Dialog.Title className="sr-only">Navigation</Dialog.Title>
            <Dialog.Description className="sr-only">Main sections of rvav</Dialog.Description>
            <Sidebar onNavigate={() => setNavOpen(false)} />
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>

      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar onOpenPalette={() => setPaletteOpen(true)} onOpenNav={() => setNavOpen(true)} />
        <main id="main" className="min-h-0 flex-1 overflow-y-auto">
          {path === "/" && <SetupLanding />}
          {projectId || withoutProject(path) ? children : <NoProject />}
        </main>
      </div>
      {paletteLoaded && (
        <Suspense fallback={null}>
          <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
        </Suspense>
      )}
    </div>
  );
}
