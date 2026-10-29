import { useNavigate } from "@tanstack/react-router";
import { Check, ChevronDown, KeyRound, LogOut, Menu as MenuIcon, Monitor, Moon, Search, Sun } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NotificationBell } from "@/features/notifications/NotificationBell";
import { Kbd } from "@/components/ui/kbd";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { signOut } from "@/lib/auth";
import { isMock } from "@/lib/env";
import { useCurrentProject } from "@/lib/project";
import { useTheme, type Theme } from "@/lib/theme";
import { initials } from "@/lib/utils";

const themes: { value: Theme; label: string; icon: React.ElementType }[] = [
  { value: "system", label: "System", icon: Monitor },
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
];

const roleLabel = { "project-admin": "Project admin", member: "Member", viewer: "Viewer" } as const;

export function TopBar({ onOpenPalette, onOpenNav }: { onOpenPalette: () => void; onOpenNav: () => void }) {
  const { me, project, role, setProjectId } = useCurrentProject();
  const { theme, setTheme } = useTheme();
  const navigate = useNavigate();
  const ThemeIcon = themes.find((t) => t.value === theme)?.icon ?? Monitor;

  return (
    <header className="flex flex-wrap items-center gap-2.5 border-b border-line bg-surface px-4 py-2.5 md:px-6">
      <Button variant="ghost" size="icon" className="md:hidden" aria-label="Open navigation" onClick={onOpenNav}>
        <MenuIcon />
      </Button>
      <button
        type="button"
        onClick={onOpenPalette}
        className="flex h-[30px] min-w-0 flex-[0_1_380px] items-center gap-2 rounded-md border border-line bg-bg pr-2 pl-2.5 text-left text-faint hover:border-line-strong"
      >
        <Search className="size-3.5 shrink-0" aria-hidden />
        <span className="truncate">Search VMs, IPs, requests…</span>
        <Kbd className="ml-auto">⌘K</Kbd>
      </button>

      <div className="ml-auto flex items-center gap-2">
        <NotificationBell />
        <Menu>
          <MenuTrigger asChild>
            <Button variant="secondary" className="border-line">
              <span className="text-[11px] font-normal text-faint">Project</span>
              <span className="max-w-40 truncate">{project.name}</span>
              <ChevronDown className="text-faint" />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuLabel>Your projects</MenuLabel>
            {me.projects.map((p) => (
              <MenuItem key={p.project.id} onSelect={() => setProjectId(p.project.id)}>
                <span className="flex flex-col">
                  <span>{p.project.name}</span>
                  <span className="text-[11px] text-faint">{roleLabel[p.role]}</span>
                </span>
                {p.project.id === project.id && <Check className="ml-auto text-accent!" />}
              </MenuItem>
            ))}
          </MenuContent>
        </Menu>

        <Menu>
          <MenuTrigger asChild>
            <Button variant="secondary" size="icon" className="size-[30px] border-line" aria-label={`Theme: ${theme}`}>
              <ThemeIcon />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuLabel>Theme</MenuLabel>
            {themes.map((t) => (
              <MenuItem key={t.value} onSelect={() => setTheme(t.value)}>
                <t.icon /> {t.label}
                {theme === t.value && <Check className="ml-auto text-accent!" />}
              </MenuItem>
            ))}
          </MenuContent>
        </Menu>

        <Menu>
          <MenuTrigger asChild>
            <button type="button" className="flex items-center gap-2 rounded-md py-0.5 pr-1 pl-1 hover:bg-sunken">
              {/* Named by what it shows (WCAG 2.5.3), "Account" first. */}
              <span className="sr-only">Account: </span>
              <span
                aria-hidden
                className="flex size-[26px] items-center justify-center rounded-full bg-accent-soft text-[10px] font-semibold text-accent"
              >
                {initials(me.user.name)}
              </span>
              <span className="hidden flex-col text-left leading-tight sm:flex">
                <span className="font-medium">{me.user.name}</span>
                <span className="text-[11px] text-faint">{roleLabel[role]}</span>
              </span>
            </button>
          </MenuTrigger>
          <MenuContent>
            <MenuLabel>{me.user.email}</MenuLabel>
            {me.title && <p className="px-2 pb-1 text-xs text-muted">{me.title}</p>}
            <MenuSeparator />
            <MenuItem onSelect={() => void navigate({ to: "/tokens" })}>
              <KeyRound /> API tokens
            </MenuItem>
            {isMock ? (
              <p className="max-w-56 px-2 py-1.5 text-xs text-faint">
                Mock mode: signed in with a development identity.
              </p>
            ) : (
              <MenuItem onSelect={() => void signOut()}>
                <LogOut /> Sign out
              </MenuItem>
            )}
          </MenuContent>
        </Menu>
      </div>
    </header>
  );
}
