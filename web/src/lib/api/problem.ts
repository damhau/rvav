import { i18n } from "@/lib/i18n";
import type { ApiError } from "./client";

/** A text to show, with its language when it is the server's English rather than the language shown. */
export interface Shown {
  text: string;
  lang?: "en";
}

// What the server says for each 403 and 404 it says nothing more about (internal/httpapi), and
// the mock backend's titles for them.
const generic = {
  403: { key: "forbidden", texts: ["Not allowed", "Forbidden", "You do not have permission to do this."] },
  404: { key: "notFound", texts: ["Not found", "It does not exist, or you cannot see it."] },
} as const;

const isGeneric = (status: number): status is keyof typeof generic => status in generic;

/**
 * A problem's title and detail to show. A 403's and a 404's are worded in the
 * language shown, keeping the server's title and detail where they say more;
 * any other problem is the server's, in English (ADR-0061).
 */
export function problemText(e: ApiError): { title: Shown; detail?: Shown } {
  const { title, detail } = e.problem;
  const server = (text: string | undefined): Shown | undefined => (text ? { text, lang: "en" } : undefined);
  if (!isGeneric(e.status)) return { title: server(title)!, detail: server(detail) };
  const { key, texts } = generic[e.status];
  const says = (text: string | undefined) => text !== undefined && (texts as readonly string[]).includes(text);
  const own = detail && detail !== title && !says(detail) ? detail : undefined;
  return {
    title: says(title) ? { text: i18n.t(`common:error.${key}.title`) } : server(title)!,
    detail: server(own) ?? { text: i18n.t(`common:error.${key}.detail`) },
  };
}
