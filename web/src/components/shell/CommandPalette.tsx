import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Command } from "cmdk";
import { ArrowRight, LayoutGrid, List, Monitor, Moon, Plus, Server, Sun } from "lucide-react";
import { useDeferredValue, useState } from "react";
import { useCurrentProject } from "@/lib/project";
import { q } from "@/lib/query";
import { useTheme } from "@/lib/theme";

interface Entry {
  id: string;
  label: string;
  meta?: string;
  hint?: string;
  icon: React.ElementType;
  run: () => void;
}

const itemClass =
  "mx-1.5 flex cursor-default items-center gap-2.5 rounded-md px-2 py-1.5 text-[13px] outline-none data-[selected=true]:bg-accent-soft [&_svg]:size-3.5 [&_svg]:text-faint";

function Group({ heading, entries }: { heading: string; entries: Entry[] }) {
  if (entries.length === 0) return null;
  return (
    <Command.Group
      heading={heading}
      className="[&_[cmdk-group-heading]]:label py-1 [&_[cmdk-group-heading]]:px-3.5 [&_[cmdk-group-heading]]:pt-2 [&_[cmdk-group-heading]]:pb-1"
    >
      {entries.map((e) => (
        <Command.Item key={e.id} value={e.id} onSelect={e.run} className={itemClass}>
          <e.icon aria-hidden />
          <span className="flex min-w-0 flex-1 items-baseline gap-2 overflow-hidden">
            <span className="truncate">{e.label}</span>
            {e.meta && <span className="truncate font-mono text-[11px] text-faint">{e.meta}</span>}
          </span>
          {e.hint && <span className="font-mono text-[10px] text-faint">{e.hint}</span>}
        </Command.Item>
      ))}
    </Command.Group>
  );
}

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const navigate = useNavigate();
  const { projectId } = useCurrentProject();
  const { setTheme } = useTheme();
  const [search, setSearch] = useState("");
  const term = useDeferredValue(search.trim());
  const searching = term.length >= 2;

  const catalog = useQuery({ ...q.catalog({ projectId }), enabled: open });
  const deployments = useInfiniteQuery({
    ...q.deployments({ projectId, q: term, limit: 6 }),
    enabled: open && searching,
  });
  const requests = useInfiniteQuery({ ...q.requests({ projectId, q: term, limit: 5 }), enabled: open && searching });

  const close = () => {
    onOpenChange(false);
    setSearch("");
  };
  const go = (to: string, params?: Record<string, string>) => () => {
    close();
    void navigate({ to, params } as never);
  };
  const match = (...parts: (string | undefined)[]) =>
    !term || parts.some((p) => p?.toLowerCase().includes(term.toLowerCase()));

  const nav: Entry[] = [
    { id: "go-overview", label: "Overview", icon: ArrowRight, hint: "g o", run: go("/") },
    { id: "go-catalog", label: "Catalog", icon: LayoutGrid, hint: "g c", run: go("/catalog") },
    { id: "go-deployments", label: "Deployments", icon: Server, hint: "g d", run: go("/deployments") },
    { id: "go-requests", label: "Requests", icon: List, hint: "g r", run: go("/requests") },
  ].filter((e) => match(e.label));

  const items: Entry[] = (catalog.data?.items ?? [])
    .filter((i) => match(i.name, i.summary, i.source.awx_template_name))
    .slice(0, term ? 5 : 3)
    .map((i) => ({
      id: `item-${i.slug}`,
      label: `Request ${i.name}`,
      meta: i.source.awx_template_name ?? i.version.number,
      icon: Plus,
      run: go("/catalog/$itemId/request", { itemId: i.slug }),
    }));

  const deps: Entry[] = searching
    ? (deployments.data?.pages[0]?.items ?? []).map((d) => ({
        id: `dep-${d.id}`,
        label: d.name,
        meta: [d.primary_address?.ip, `${d.vm_count} VM${d.vm_count === 1 ? "" : "s"}`].filter(Boolean).join(" · "),
        hint: d.environment,
        icon: Server,
        run: go("/deployments/$deploymentId", { deploymentId: d.id }),
      }))
    : [];

  const reqs: Entry[] = searching
    ? (requests.data?.pages[0]?.items ?? []).map((r) => ({
        id: `req-${r.id}`,
        label: `${r.number} ${r.title}`,
        meta: r.status.replace("_", " "),
        icon: List,
        run: go("/requests/$requestId", { requestId: r.number }),
      }))
    : [];

  const actions: Entry[] = [
    { id: "theme-light", label: "Use light theme", icon: Sun, run: () => (setTheme("light"), close()) },
    { id: "theme-dark", label: "Use dark theme", icon: Moon, run: () => (setTheme("dark"), close()) },
    { id: "theme-system", label: "Follow system theme", icon: Monitor, run: () => (setTheme("system"), close()) },
  ].filter((e) => term.length >= 2 && match(e.label, "theme"));

  const loading = searching && (deployments.isFetching || requests.isFetching);

  return (
    <Command.Dialog
      open={open}
      onOpenChange={(o) => (o ? onOpenChange(true) : close())}
      label="Search or run a command"
      shouldFilter={false}
      overlayClassName="fixed inset-0 z-40 bg-black/40"
      contentClassName="fixed left-1/2 top-[12vh] z-50 w-[min(620px,calc(100vw-32px))] -translate-x-1/2 overflow-hidden rounded-lg border border-line bg-surface shadow-pop"
    >
      <Command.Input
        value={search}
        onValueChange={setSearch}
        placeholder="Search VMs, IPs, requests, or type a command"
        className="h-12 w-full border-b border-line bg-transparent px-4 text-sm outline-none placeholder:text-faint"
      />
      <Command.List className="max-h-[60vh] overflow-y-auto py-1.5">
        {loading && <Command.Loading className="px-4 py-2 text-xs text-faint">Searching…</Command.Loading>}
        <Command.Empty className="px-4 py-5 text-[13px]">
          <span className="block font-medium">No matches for “{search}”</span>
          <span className="text-xs text-muted">
            Try a VM name, an IP address such as 10.21.10.37, or a request number.
          </span>
        </Command.Empty>
        <Group heading="Deployments and VMs" entries={deps} />
        <Group heading="Requests" entries={reqs} />
        <Group heading="Request" entries={items} />
        <Group heading="Go to" entries={nav} />
        <Group heading="Actions" entries={actions} />
      </Command.List>
      <div className="flex flex-wrap gap-3.5 border-t border-line bg-bg px-4 py-2 text-[11px] text-faint">
        <span>
          <span className="font-mono">↑↓</span> move
        </span>
        <span>
          <span className="font-mono">↵</span> open
        </span>
        <span>
          <span className="font-mono">esc</span> close
        </span>
        <span className="ml-auto">Type 2+ characters to search deployments and requests</span>
      </div>
    </Command.Dialog>
  );
}
