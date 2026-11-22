import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Star } from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { api, call, isApiError } from "@/lib/api/client";
import { i18n } from "@/lib/i18n";
import type { CatalogItem, CatalogItemPage } from "@/lib/api/types";
import { keys } from "@/lib/query";
import { cn } from "@/lib/utils";

const itemQueries = ["catalog", "item"] as const;

function useFavourite(item: CatalogItem) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (fav: boolean) =>
      call(
        fav
          ? api.PUT("/catalog/items/{itemId}/favourite", { params: { path: { itemId: item.id } } })
          : api.DELETE("/catalog/items/{itemId}/favourite", { params: { path: { itemId: item.id } } }),
      ),
    onMutate: async (fav) => {
      await qc.cancelQueries({ queryKey: keys.catalog.all });
      const lists = qc.getQueriesData<CatalogItemPage>({ queryKey: keys.catalog.lists() });
      const items = qc.getQueriesData<CatalogItem>({ queryKey: itemQueries });
      qc.setQueriesData<CatalogItemPage>({ queryKey: keys.catalog.lists() }, (old) =>
        old ? { ...old, items: old.items.map((i) => (i.id === item.id ? { ...i, favourite: fav } : i)) } : old,
      );
      qc.setQueriesData<CatalogItem>({ queryKey: itemQueries }, (old) =>
        old && old.id === item.id ? { ...old, favourite: fav } : old,
      );
      return { snapshot: [...lists, ...items] };
    },
    onError: (e, _fav, ctx) => {
      ctx?.snapshot.forEach(([key, data]) => qc.setQueryData(key, data));
      toast.error(i18n.t("catalog:favourite.notUpdated"), {
        description: isApiError(e) ? i18n.t("catalog:favourite.requestId", { id: e.requestId }) : undefined,
      });
    },
    onSettled: () => void qc.invalidateQueries({ queryKey: keys.catalog.all }),
  });
}

/** The star on a catalog card: a toggle named after the item. */
export function FavouriteStar({ item }: { item: CatalogItem }) {
  const { t } = useTranslation("catalog");
  const mutation = useFavourite(item);
  return (
    <button
      type="button"
      aria-pressed={item.favourite}
      aria-label={t(item.favourite ? "favourite.remove" : "favourite.add", { name: item.name })}
      onClick={() => mutation.mutate(!item.favourite)}
      className="relative z-10 -m-1 rounded-sm p-1 text-faint hover:text-warn"
    >
      <Star className={cn("size-3.5", item.favourite && "fill-warn text-warn")} aria-hidden />
    </button>
  );
}

/** The same toggle as a button with its text, for an item's page. */
export function FavouriteButton({ item }: { item: CatalogItem }) {
  const { t } = useTranslation("catalog");
  const mutation = useFavourite(item);
  return (
    <Button aria-pressed={item.favourite} onClick={() => mutation.mutate(!item.favourite)}>
      <Star className={cn(item.favourite && "fill-warn text-warn")} aria-hidden />
      {t("favourite.button")}
    </Button>
  );
}
