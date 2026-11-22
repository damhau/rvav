import { useNavigate } from "@tanstack/react-router";
import {
  Check,
  ChevronDown,
  KeyRound,
  Languages,
  LockKeyhole,
  LogOut,
  Menu as MenuIcon,
  Monitor,
  Moon,
  Search,
  ShieldCheck,
  Sun,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button, IconButton } from "@/components/ui/button";
import { NotificationBell } from "@/features/notifications/NotificationBell";
import { Kbd } from "@/components/ui/kbd";
import { Menu, MenuContent, MenuItem, MenuLabel, MenuSeparator, MenuTrigger } from "@/components/ui/dropdown-menu";
import { CHANGE_PASSWORD_PATH, signOut } from "@/lib/auth";
import { isMock } from "@/lib/env";
import { currentLocale, localeNames, locales } from "@/lib/i18n";
import { useChooseLocale } from "@/lib/i18n/choose";
import { useCurrentProject } from "@/lib/project";
import { rolesLabel } from "./roles";
import { useTheme, type Theme } from "@/lib/theme";
import { initials } from "@/lib/utils";

const themes: { value: Theme; icon: React.ElementType }[] = [
  { value: "system", icon: Monitor },
  { value: "light", icon: Sun },
  { value: "dark", icon: Moon },
];

export function TopBar({ onOpenPalette, onOpenNav }: { onOpenPalette: () => void; onOpenNav: () => void }) {
  const { t } = useTranslation("shell");
  const { me, projectId, project, setProjectId } = useCurrentProject();
  const ownRole = rolesLabel(me, projectId);
  const { theme, setTheme } = useTheme();
  const navigate = useNavigate();
  const chooseLocale = useChooseLocale();
  const shown = currentLocale();
  const ThemeIcon = themes.find((th) => th.value === theme)?.icon ?? Monitor;

  return (
    <header className="flex flex-wrap items-center gap-2.5 border-b border-line bg-surface px-4 py-2.5 md:px-6">
      <IconButton label={t("topBar.openNavigation")} className="lg:hidden" onClick={onOpenNav}>
        <MenuIcon />
      </IconButton>
      <button
        type="button"
        onClick={onOpenPalette}
        className="flex h-[30px] min-w-0 flex-[0_1_380px] items-center gap-2 rounded-md border border-line bg-bg pr-2 pl-2.5 text-left text-faint hover:border-line-strong"
      >
        <Search className="size-3.5 shrink-0" aria-hidden />
        <span className="truncate">{t("topBar.search")}</span>
        <Kbd className="ml-auto">⌘K</Kbd>
      </button>

      <div className="ml-auto flex items-center gap-2">
        <NotificationBell />
        <Menu>
          <MenuTrigger asChild>
            <Button variant="secondary" className="border-line">
              <span className="text-caption font-normal text-faint">{t("topBar.project")}</span>
              <span className="max-w-40 truncate">{project.name}</span>
              <ChevronDown className="text-faint" />
            </Button>
          </MenuTrigger>
          <MenuContent>
            <MenuLabel>{t("topBar.yourProjects")}</MenuLabel>
            {me.projects.map((p) => (
              <MenuItem key={p.project.id} onSelect={() => setProjectId(p.project.id)}>
                <span className="flex flex-col">
                  <span>{p.project.name}</span>
                  <span className="text-caption text-faint">{rolesLabel(me, p.project.id)}</span>
                </span>
                {p.project.id === project.id && <Check className="ml-auto text-accent!" />}
              </MenuItem>
            ))}
          </MenuContent>
        </Menu>

        <Menu>
          <MenuTrigger asChild>
            <IconButton variant="secondary" className="size-[30px] border-line" label={t(`topBar.themeIs.${theme}`)}>
              <ThemeIcon />
            </IconButton>
          </MenuTrigger>
          <MenuContent>
            <MenuLabel>{t("topBar.theme")}</MenuLabel>
            {themes.map((th) => (
              <MenuItem key={th.value} onSelect={() => setTheme(th.value)}>
                <th.icon /> {t(`topBar.themes.${th.value}`)}
                {theme === th.value && <Check className="ml-auto text-accent!" />}
              </MenuItem>
            ))}
          </MenuContent>
        </Menu>

        <Menu>
          <MenuTrigger asChild>
            <button type="button" className="flex items-center gap-2 rounded-md py-0.5 pr-1 pl-1 hover:bg-sunken">
              {/* Named by what it shows (WCAG 2.5.3), "Account" first. */}
              <span className="sr-only">{t("topBar.account")}</span>
              <span
                aria-hidden
                className="flex size-[26px] items-center justify-center rounded-full bg-accent-soft text-badge font-semibold text-accent"
              >
                {initials(me.user.name)}
              </span>
              <span className="hidden flex-col text-left leading-tight sm:flex">
                <span className="font-medium">{me.user.name}</span>
                {ownRole && <span className="max-w-48 truncate text-caption text-faint">{ownRole}</span>}
              </span>
            </button>
          </MenuTrigger>
          <MenuContent>
            <MenuLabel>{me.local ? t("topBar.localAccount") : me.user.email}</MenuLabel>
            {me.title && <p className="px-2 pb-1 text-secondary text-muted">{me.title}</p>}
            <MenuSeparator />
            <MenuItem onSelect={() => void navigate({ to: "/access", search: {} })}>
              <ShieldCheck /> {t("topBar.whatIMayDo")}
            </MenuItem>
            <MenuItem onSelect={() => void navigate({ to: "/tokens" })}>
              <KeyRound /> {t("topBar.apiTokens")}
            </MenuItem>
            {me.local && (
              <MenuItem onSelect={() => globalThis.location.assign(CHANGE_PASSWORD_PATH)}>
                <LockKeyhole /> {t("topBar.changePassword")}
              </MenuItem>
            )}
            <MenuSeparator />
            <MenuLabel className="flex items-center gap-1.5">
              <Languages className="size-3.5" aria-hidden /> {t("topBar.language")}
            </MenuLabel>
            {/* Each language in its own words, marked as such for screen readers (WCAG 3.1.2). */}
            {locales.map((l) => (
              <MenuItem key={l} lang={l} onSelect={() => chooseLocale.mutate(l)}>
                {localeNames[l]}
                {shown === l && <Check className="ml-auto text-accent!" />}
              </MenuItem>
            ))}
            <MenuSeparator />
            {isMock ? (
              <p className="max-w-56 px-2 py-1.5 text-secondary text-faint">{t("topBar.mockMode")}</p>
            ) : (
              <MenuItem onSelect={() => void signOut()}>
                <LogOut /> {t("topBar.signOut")}
              </MenuItem>
            )}
          </MenuContent>
        </Menu>
      </div>
    </header>
  );
}
