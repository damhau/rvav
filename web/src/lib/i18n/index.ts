// The web app's languages (ADR-0061): i18next, one namespace per area of the
// app in src/locales/<locale>/<namespace>.json.
import i18n, { type BackendModule, type InitOptions } from "i18next";
import LanguageDetector from "i18next-browser-languagedetector";
import { initReactI18next } from "react-i18next";

export const locales = ["en", "fr"] as const;
export type Locale = (typeof locales)[number];

/** Each language in its own words, the same whatever the language shown. */
export const localeNames: Record<Locale, string> = { en: "English", fr: "Français" };

/** Each area's texts; a folder's texts are all in its own namespace (docs/frontend.md#translating-a-folder). */
export const namespaces = [
  "common",
  "shell",
  "account",
  "forms",
  "canvas",
  "catalog",
  "requests",
  "deployments",
  "approvals",
  "resources",
  "reports",
  "overview",
  "notifications",
  "admin",
  "admin-catalog",
  "admin-governance",
  "admin-access",
  "admin-notifications",
  "admin-extensibility",
  "admin-onboarding",
  "names",
] as const;
export type Namespace = (typeof namespaces)[number];

/** Where this browser keeps the language its signed-in person chose, for the pages shown before `/me` answers. */
export const LOCALE_KEY = "rvav.locale";

type Bundle = { default: Record<string, Record<string, unknown>> };

// One chunk per language, holding all its namespaces.
const bundles: Record<Locale, () => Promise<Bundle>> = {
  en: () => import("@/locales/en"),
  fr: () => import("@/locales/fr"),
};

const backend: BackendModule = {
  type: "backend",
  init: () => {},
  read(lng, ns, done) {
    const load = bundles[lng as Locale];
    if (!load) {
      done(null, {});
      return;
    }
    load().then(
      (b) => done(null, b.default[`./${ns}.json`] ?? {}),
      (e: unknown) => done(e instanceof Error ? e : new Error(String(e)), false),
    );
  },
};

export const detector = new LanguageDetector();

export function isLocale(v: unknown): v is Locale {
  return typeof v === "string" && (locales as readonly string[]).includes(v);
}

/** Loads every namespace of the detected language (or `options.lng`) and of English. */
export async function initI18n(options: InitOptions = {}) {
  if (i18n.isInitialized) {
    if (options.lng && i18n.language !== options.lng) await i18n.changeLanguage(options.lng);
    return i18n;
  }
  i18n.on("languageChanged", (lng) => {
    if (typeof document !== "undefined") document.documentElement.lang = lng;
  });
  await i18n
    .use(backend)
    .use(detector)
    .use(initReactI18next)
    .init({
      supportedLngs: locales,
      fallbackLng: "en",
      load: "languageOnly",
      ns: namespaces,
      defaultNS: "common",
      interpolation: { escapeValue: false },
      returnEmptyString: false,
      detection: {
        order: ["localStorage", "navigator"],
        lookupLocalStorage: LOCALE_KEY,
        caches: [],
        // "fr-CH" shows the French texts, and <html lang> says "fr" like the language chosen would.
        convertDetectedLanguage: (lng: string) => lng.split("-")[0] ?? lng,
      },
      // Every namespace is loaded before the app renders, and before a new language shows.
      react: { useSuspense: false },
      ...options,
    });
  return i18n;
}

/** The browser's language, if rvav has it, else English. */
export function browserLocale(): Locale {
  const found = detector.detect(["navigator"]);
  const codes = typeof found === "string" ? [found] : (found ?? []);
  return codes.map((c) => c.toLowerCase().split("-")[0]).find(isLocale) ?? "en";
}

/** The language shown: the person's choice, else their browser's, else English. */
export function localeFor(chosen: Locale | undefined): Locale {
  return chosen ?? browserLocale();
}

function remember(chosen: Locale | undefined) {
  try {
    if (chosen) globalThis.localStorage?.setItem(LOCALE_KEY, chosen);
    else globalThis.localStorage?.removeItem(LOCALE_KEY);
  } catch {
    // Storage blocked: the pages before /me answers follow the browser.
  }
}

/** Shows the language for a person's choice (or none), and remembers it in this browser. */
export async function applyLocale(chosen: Locale | undefined) {
  remember(chosen);
  const lng = localeFor(chosen);
  if (i18n.resolvedLanguage !== lng) await i18n.changeLanguage(lng);
}

/** Resolves once the language shown is loaded: for text made while the app starts, before React renders. */
export function i18nReady(): Promise<void> {
  if (i18n.isInitialized) return Promise.resolve();
  return new Promise((resolve) => i18n.on("initialized", () => resolve()));
}

/** The language shown now. */
export function currentLocale(): Locale {
  return isLocale(i18n.resolvedLanguage) ? i18n.resolvedLanguage : "en";
}

export { i18n };
