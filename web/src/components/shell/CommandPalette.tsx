import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Command } from "cmdk";
import { HardDrive, List, Monitor, Moon, Plus, Server, Sun } from "lucide-react";
import { Dialog as D } from "radix-ui";
import { useDeferredValue, useState } from "react";
import { useTranslation } from "react-i18next";
import { requestStatusLabel } from "@/components/common/status";
import { useOpener } from "@/components/ui/opener";
import { useCurrentProject } from "@/lib/project";
import { useEnvName } from "@/lib/environments";
import { q } from "@/lib/query";
import { useTheme } from "@/lib/theme";
import { adminKeywords, adminLabel, adminMenu, groupLabel, mainLabel, mainPages } from "./nav";

interface Entry {
  id: string;
  label: string;
  meta?: string;
  hint?: string;
  icon: React.ElementType;
  run: () => void;
}

const itemClass =
  "mx-1.5 flex cursor-default items-center gap-2.5 rounded-md px-2 py-1.5 text-body outline-none data-[selected=true]:bg-accent-soft [&_svg]:size-3.5 [&_svg]:text-faint";

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
            {e.meta && <span className="truncate font-mono text-caption text-faint">{e.meta}</span>}
          </span>
          {e.hint && <span className="text-badge whitespace-nowrap text-faint">{e.hint}</span>}
        </Command.Item>
      ))}
    </Command.Group>
  );
}

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const { t } = useTranslation("shell");
  const navigate = useNavigate();
  const { projectId, me } = useCurrentProject();
  const envName = useEnvName();
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

  const nav: Entry[] = mainPages
    .filter((p) => match(mainLabel(p.key)))
    .map((p) => ({ id: `go-${p.key}`, label: mainLabel(p.key), icon: p.icon, run: go(p.to) }));

  // The administration pages show once something is typed, in the sidebar's groups.
  const admin = term
    ? adminMenu(me).map((g) => ({
        heading: groupLabel(g),
        entries: g.pages
          .filter((p) => match(adminLabel(p), groupLabel(g), ...adminKeywords(p)))
          .map((p): Entry => ({ id: `go-${p.to}`, label: adminLabel(p), icon: p.icon, run: go(p.to) })),
      }))
    : [];

  const items: Entry[] = (catalog.data?.items ?? [])
    .filter((i) => match(i.name, i.summary, i.source.awx_template_name))
    .slice(0, term ? 5 : 3)
    .map((i) => ({
      id: `item-${i.slug}`,
      label: t("palette.requestItem", { name: i.name }),
      meta: i.source.awx_template_name ?? i.version.number,
      icon: Plus,
      run: go("/catalog/$itemId/request", { itemId: i.slug }),
    }));

  const deps: Entry[] = searching
    ? (deployments.data?.pages[0]?.items ?? []).map((d) => ({
        id: `dep-${d.id}`,
        label: d.name,
        meta: [d.primary_address?.ip, t("palette.vmCount", { count: d.vm_count })].filter(Boolean).join(" · "),
        hint: envName(d.environment),
        icon: Server,
        run: go("/deployments/$deploymentId", { deploymentId: d.id }),
      }))
    : [];

  // The palette shows a few deployments; Resources lists every VM the search finds.
  const vms: Entry[] = searching
    ? [
        {
          id: "find-vms",
          label: t("palette.findVms", { term }),
          meta: t("palette.findVmsMeta"),
          icon: HardDrive,
          run: () => {
            close();
            void navigate({ to: "/resources", search: { q: term } });
          },
        },
      ]
    : [];

  const reqs: Entry[] = searching
    ? (requests.data?.pages[0]?.items ?? []).map((r) => ({
        id: `req-${r.id}`,
        label: `${r.number} ${r.title}`,
        meta: requestStatusLabel(r.status),
        icon: List,
        run: go("/requests/$requestId", { requestId: r.number }),
      }))
    : [];

  const actions: Entry[] = [
    { id: "theme-light", label: t("palette.theme.light"), icon: Sun, run: () => (setTheme("light"), close()) },
    { id: "theme-dark", label: t("palette.theme.dark"), icon: Moon, run: () => (setTheme("dark"), close()) },
    {
      id: "theme-system",
      label: t("palette.theme.system"),
      icon: Monitor,
      run: () => (setTheme("system"), close()),
    },
  ].filter((e) => term.length >= 2 && match(e.label, "theme", t("palette.theme.keyword")));

  const loading = searching && (deployments.isFetching || requests.isFetching);

  return (
    <D.Root open={open} onOpenChange={(o) => (o ? onOpenChange(true) : close())}>
      <D.Portal>
        <D.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <PaletteContent>
          <Command label={t("palette.label")} shouldFilter={false}>
            <Command.Input
              value={search}
              onValueChange={setSearch}
              placeholder={t("palette.placeholder")}
              className="h-12 w-full border-b border-line bg-transparent px-4 text-body outline-none placeholder:text-faint"
            />
            <Command.List className="max-h-[60vh] overflow-y-auto py-1.5">
              {loading && (
                <Command.Loading className="px-4 py-2 text-secondary text-faint">
                  {t("palette.searching")}
                </Command.Loading>
              )}
              <Command.Empty className="px-4 py-5 text-body">
                <span className="block font-medium">{t("palette.noMatches", { search })}</span>
                <span className="text-secondary text-muted">{t("palette.tryHint")}</span>
              </Command.Empty>
              <Group heading={t("palette.groups.deploymentsAndVms")} entries={[...deps, ...vms]} />
              <Group heading={t("palette.groups.requests")} entries={reqs} />
              <Group heading={t("palette.groups.request")} entries={items} />
              <Group heading={t("palette.groups.goTo")} entries={nav} />
              {admin.map((g) => (
                <Group key={g.heading} heading={g.heading} entries={g.entries} />
              ))}
              <Group heading={t("palette.groups.actions")} entries={actions} />
            </Command.List>
            <div className="flex flex-wrap gap-3.5 border-t border-line bg-bg px-4 py-2 text-caption text-faint">
              <span>
                <span className="font-mono">↑↓</span> {t("palette.move")}
              </span>
              <span>
                <span className="font-mono">↵</span> {t("palette.open")}
              </span>
              <span>
                <span className="font-mono">esc</span> {t("palette.close")}
              </span>
              <span className="ml-auto">{t("palette.typeToSearch")}</span>
            </div>
          </Command>
        </PaletteContent>
      </D.Portal>
    </D.Root>
  );
}

// Mounted as the palette opens, so that it reads what had the focus then.
function PaletteContent({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation("shell");
  const giveFocusBack = useOpener();
  return (
    <D.Content
      aria-describedby={undefined}
      onCloseAutoFocus={giveFocusBack}
      className="fixed top-[12vh] left-1/2 z-50 w-[min(620px,calc(100vw-32px))] -translate-x-1/2 overflow-hidden rounded-lg border border-line bg-surface shadow-pop"
    >
      <D.Title className="sr-only">{t("palette.label")}</D.Title>
      {children}
    </D.Content>
  );
}
