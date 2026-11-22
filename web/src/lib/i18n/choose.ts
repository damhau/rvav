import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { api, call, isApiError } from "@/lib/api/client";
import type { Me } from "@/lib/api/types";
import { keys, q } from "@/lib/query";
import { applyLocale, i18n, type Locale } from ".";

/**
 * Shows the signed-in person's language once `/me` says it: their choice,
 * else their browser's. Ready once the first one shows, so that the app
 * doesn't flash in another language; later changes show in place.
 */
export function useAppliedLocale(me: Me | undefined): boolean {
  const [ready, setReady] = useState(false);
  const loaded = me !== undefined;
  const chosen = me?.locale;
  useEffect(() => {
    if (!loaded) return;
    void applyLocale(chosen).finally(() => setReady(true));
  }, [loaded, chosen]);
  return ready;
}

/** Chooses the signed-in person's language: it shows at once, and comes back if the server refuses it. */
export function useChooseLocale() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (locale: Locale) => call(api.PUT("/me/locale", { body: { locale } })),
    onMutate: async (locale) => {
      await qc.cancelQueries({ queryKey: keys.me });
      const before = qc.getQueryData<Me>(keys.me);
      qc.setQueryData<Me>(keys.me, (m) => m && { ...m, locale });
      await applyLocale(locale);
      return { before };
    },
    onError: async (e, _locale, ctx) => {
      qc.setQueryData(keys.me, ctx?.before);
      await applyLocale(ctx?.before?.locale);
      // In the language put back, not the one refused.
      toast.error(i18n.t("language.notSaved"), { description: isApiError(e) ? e.problem.title : undefined });
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: q.notificationPreferences().queryKey }),
  });
}
