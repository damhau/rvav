// Pictures image fields show (F1.2, ADR-0040): an address, or upload:<id>
// for one a catalog administrator uploaded, which needs the user's token
// and so is fetched, then shown from a blob: URL.
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, call } from "@/lib/api/client";
import type { FormImage } from "@/lib/api/types";
import { shownUrl } from "@/lib/utils";

const uploaded = "upload:";

/** The uploaded picture an image field's address names, if it names one. */
export function uploadedImage(url: string | undefined): string | undefined {
  const u = url?.trim();
  return u?.startsWith(uploaded) ? u.slice(uploaded.length) : undefined;
}

/** What an image field's `<img>` shows, and whether its picture failed to load. */
export function useShownImage(url: string | undefined): { src?: string; failed: boolean } {
  const id = uploadedImage(url);
  const picture = useQuery({
    queryKey: ["forms", "images", id],
    queryFn: ({ signal }) =>
      call(api.GET("/forms/images/{imageId}", { params: { path: { imageId: id ?? "" } }, parseAs: "blob", signal })),
    enabled: Boolean(id),
    staleTime: Infinity,
    retry: false,
  });
  const [blob, setBlob] = useState<{ of: Blob; url: string }>();
  useEffect(() => {
    if (!picture.data) return;
    const shown = { of: picture.data, url: URL.createObjectURL(picture.data) };
    setBlob(shown);
    return () => URL.revokeObjectURL(shown.url);
  }, [picture.data]);
  if (!id) return { src: shownUrl(url), failed: false };
  return { src: blob?.of === picture.data ? blob?.url : undefined, failed: picture.isError };
}

/** Uploads a picture for image fields: its `url` is what a field's address takes. */
export function uploadImage(file: File): Promise<FormImage> {
  return call(
    api.POST("/admin/catalog/images", {
      params: { query: { name: file.name } },
      body: file as unknown as string,
      bodySerializer: (b: unknown) => b as BodyInit,
      headers: { "Content-Type": "application/octet-stream" },
    }),
  );
}
