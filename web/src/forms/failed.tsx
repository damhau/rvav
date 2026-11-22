import { LoaderCircle, TriangleAlert } from "lucide-react";
import { useLayoutEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";

/**
 * A value source that didn't answer, said under its field, with Try again.
 * `id` is for the control's aria-describedby. Once the source answers, the
 * note goes, and the focus it held moves to what `focusTo` finds: the
 * field's control.
 */
export function SourceFailed({
  id,
  message,
  onRetry,
  retrying,
  focusTo,
}: {
  id: string;
  message: string;
  onRetry: () => void;
  retrying: boolean;
  focusTo: () => HTMLElement | null | undefined;
}) {
  const { t } = useTranslation();
  const box = useRef<HTMLDivElement>(null);
  const target = useRef(focusTo);
  target.current = focusTo;
  useLayoutEffect(() => {
    const el = box.current;
    return () => {
      if (el?.contains(document.activeElement)) requestAnimationFrame(() => target.current()?.focus());
    };
  }, []);
  return (
    <div ref={box} className="flex flex-wrap items-center gap-x-2 gap-y-1">
      <span id={id} className="flex items-start gap-1.5 text-secondary text-bad">
        <TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
        {message}
      </span>
      <Button type="button" size="sm" aria-disabled={retrying || undefined} onClick={() => !retrying && onRetry()}>
        {retrying && <LoaderCircle className="animate-spin" aria-hidden />}
        {t("error.retry")}
      </Button>
    </div>
  );
}
