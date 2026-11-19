import { useBranding } from "@/lib/branding";
import { cn } from "@/lib/utils";

/** The portal's logo, the dark theme's where there is one, or rvav's mark; the name beside it says what it is. */
export function BrandMark({ className }: { className?: string }) {
  const { logo_light_url: light, logo_dark_url: dark } = useBranding();
  if (!light)
    return (
      <span
        aria-hidden
        className={cn(
          "flex size-[22px] shrink-0 items-center justify-center rounded-sm bg-fg font-mono text-caption font-medium text-bg",
          className,
        )}
      >
        rv
      </span>
    );
  return (
    <span aria-hidden className={cn("flex h-[22px] max-w-24 shrink-0 items-center", className)}>
      <img src={light} alt="" className={cn("h-full w-auto max-w-full object-contain", dark && "brand-logo-light")} />
      {dark && <img src={dark} alt="" className="brand-logo-dark h-full w-auto max-w-full object-contain" />}
    </span>
  );
}
