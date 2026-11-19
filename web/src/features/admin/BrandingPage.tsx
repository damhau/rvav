// The portal's branding (ADR-0059): its name, an accent colour that reads in
// both themes, and logos for each. The sign-in page, the menu, the browser's
// title and emails show them.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ImageOff, LoaderCircle, Upload } from "lucide-react";
import { useId, useRef, useState } from "react";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { AccentPalette, Branding, LogoVariant } from "@/lib/api/types";
import { applyBranding, rvavBranding } from "@/lib/branding";
import { useDebounced } from "@/lib/hooks";
import { adm } from "@/lib/query";

const themes = {
  light: { label: "Light theme", surface: "#ffffff", fg: "#14191c", line: "#d5dbdf" },
  dark: { label: "Dark theme", surface: "#161b1e", fg: "#e4e9ec", line: "#273036" },
} as const;

const hexColour = /^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$/;

/** How the accent looks in one theme: a button, a link and the current page in the menu. */
function Swatch({ theme, palette }: { theme: keyof typeof themes; palette: AccentPalette }) {
  const t = themes[theme];
  return (
    <figure className="m-0 flex min-w-0 flex-1 flex-col gap-1.5">
      <figcaption className="text-caption text-muted">
        {t.label} <span className="font-mono">{palette.accent}</span>
      </figcaption>
      <div
        className="flex flex-wrap items-center gap-3 rounded-md border p-3 text-secondary"
        style={{ background: t.surface, color: t.fg, borderColor: t.line }}
      >
        <span
          className="rounded-md px-3 py-1.5 font-medium"
          style={{ background: palette.accent, color: palette.accent_fg }}
        >
          Submit request
        </span>
        <span className="underline" style={{ color: palette.accent }}>
          A link
        </span>
        <span className="rounded-md px-2 py-1" style={{ background: palette.accent_soft, color: t.fg }}>
          Current page
        </span>
      </div>
    </figure>
  );
}

function AccentCheckNote({ accent, onUse }: { accent: string; onUse: (colour: string) => void }) {
  const wanted = useDebounced(accent.trim(), 300);
  const valid = hexColour.test(wanted);
  const check = useQuery({
    queryKey: ["admin", "branding", "accent-check", wanted],
    queryFn: () => call(api.POST("/admin/branding/accent-check", { body: { accent: wanted } })),
    enabled: valid,
    staleTime: Infinity,
  });
  let note: React.ReactNode;
  if (!wanted)
    note = (
      <>
        <p>rvav&apos;s own accent, in both themes.</p>
        <div className="flex flex-wrap gap-3">
          <Swatch theme="light" palette={rvavBranding.light} />
          <Swatch theme="dark" palette={rvavBranding.dark} />
        </div>
      </>
    );
  else if (!valid) note = <p>A colour written #rrggbb, such as #5b2a86.</p>;
  else if (check.isPending) note = <p>Checking the contrast…</p>;
  else if (check.isError) note = <p className="text-bad">The contrast couldn&apos;t be checked.</p>;
  else if (!check.data.ok)
    note = (
      <>
        <p className="text-bad">{check.data.message}</p>
        {check.data.suggestion && (
          <Button size="sm" type="button" className="self-start" onClick={() => onUse(check.data.suggestion!)}>
            <span
              aria-hidden
              className="size-3 rounded-sm border border-line"
              style={{ background: check.data.suggestion }}
            />
            Use {check.data.suggestion}
          </Button>
        )}
      </>
    );
  else
    note = (
      <>
        <p>
          Reads at {check.data.ratio.toFixed(2)}:1 in the light theme. The dark theme takes{" "}
          {check.data.dark!.accent === check.data.light!.accent ? "it as it is" : `${check.data.dark!.accent}, lighter`}{" "}
          so that its text reads too.
        </p>
        <div className="flex flex-wrap gap-3">
          <Swatch theme="light" palette={check.data.light!} />
          <Swatch theme="dark" palette={check.data.dark!} />
        </div>
      </>
    );
  return (
    <div className="flex flex-col gap-2 text-secondary text-muted" aria-live="polite">
      {note}
    </div>
  );
}

function NameAndAccent({ branding }: { branding: Branding }) {
  const qc = useQueryClient();
  const [name, setName] = useState(branding.name === rvavBranding.name ? "" : branding.name);
  const [accent, setAccent] = useState(branding.accent ?? "");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => call(api.PUT("/admin/branding", { body: { name, accent } })),
    onSuccess: (b) => {
      setErrors({});
      applyBranding(b);
      qc.setQueryData(adm.branding().queryKey, b);
      setAccent(b.accent ?? "");
      toast.success("Branding saved");
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The branding wasn't saved");
    },
  });
  const picked = hexColour.test(accent.trim()) ? accent.trim() : branding.light.accent;
  const full = (c: string) => (c.length === 4 ? "#" + [...c.slice(1)].map((x) => x + x).join("") : c.toLowerCase());
  return (
    <Panel
      title="Name and accent colour"
      description="The menu, the sign-in page, the browser's title and emails show the name. Buttons, links and focus rings take the accent."
      footer={
        <>
          <Button type="button" variant="quiet" disabled={!accent} onClick={() => setAccent("")}>
            Use rvav&apos;s colours
          </Button>
          <Button type="submit" form="branding-settings" variant="primary" disabled={save.isPending}>
            {save.isPending ? "Saving…" : "Save branding"}
          </Button>
        </>
      }
    >
      <form
        id="branding-settings"
        className="flex max-w-[72ch] flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <Field
          label="Portal name"
          htmlFor="branding-name"
          hint="Up to 40 characters. Left empty, the portal is called rvav."
          error={errors["/name"]}
        >
          <Input
            id="branding-name"
            value={name}
            placeholder="rvav"
            maxLength={60}
            autoComplete="off"
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <div className="flex min-w-0 flex-col gap-1">
          <label htmlFor="branding-accent" className="text-secondary font-medium text-fg">
            Accent colour
          </label>
          <div className="flex items-center gap-2">
            <Input
              id="branding-accent"
              className="max-w-40 font-mono"
              value={accent}
              placeholder={rvavBranding.light.accent}
              autoComplete="off"
              spellCheck={false}
              aria-describedby="branding-accent-note"
              aria-invalid={errors["/accent"] ? true : undefined}
              onChange={(e) => setAccent(e.target.value)}
            />
            <input
              type="color"
              aria-label="Pick the accent colour"
              className="h-8 w-12 shrink-0 cursor-pointer rounded-md border border-line-strong bg-surface p-0.5"
              value={full(picked)}
              onChange={(e) => setAccent(e.target.value)}
            />
          </div>
          <FieldNote
            id="branding-accent-note"
            error={errors["/accent"]}
            hint="The light theme's, as #rrggbb: text in it needs a contrast of 4.5:1. Left empty, rvav's own."
          />
        </div>
        <AccentCheckNote accent={accent} onUse={setAccent} />
      </form>
    </Panel>
  );
}

function LogoRow({ variant, branding }: { variant: LogoVariant; branding: Branding }) {
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const noteId = useId();
  const [error, setError] = useState<string>();
  const url = variant === "light" ? branding.logo_light_url : branding.logo_dark_url;
  const t = themes[variant];
  const done = (b: Branding, message: string) => {
    setError(undefined);
    applyBranding(b);
    qc.setQueryData(adm.branding().queryKey, b);
    toast.success(message);
  };
  const upload = useMutation({
    mutationFn: (file: File) =>
      call(
        api.PUT("/admin/branding/logos/{variant}", {
          params: { path: { variant } },
          body: file as unknown as string,
          bodySerializer: (b: unknown) => b as BodyInit,
          headers: { "Content-Type": "application/octet-stream" },
        }),
      ),
    onSuccess: (b) => done(b, `${t.label}'s logo saved`),
    onError: (e, f) =>
      setError(
        isApiError(e)
          ? (e.fieldErrors[0]?.message ?? e.problem.detail ?? e.problem.title)
          : `${f.name} couldn't be uploaded.`,
      ),
  });
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/branding/logos/{variant}", { params: { path: { variant } } })),
    onSuccess: (b) => done(b, `${t.label}'s logo removed`),
    onError: (e) => failed(e, "The logo wasn't removed"),
  });
  const fallback =
    variant === "dark" && branding.logo_light_url ? "The light theme's logo is shown." : "rvav's mark is shown.";
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:gap-4">
      <div
        className="flex h-16 w-full shrink-0 items-center justify-center rounded-md border px-3 sm:w-48"
        style={{ background: t.surface, borderColor: t.line, color: t.fg }}
      >
        {url ? (
          <img src={url} alt={`${t.label}'s logo`} className="max-h-10 max-w-full object-contain" />
        ) : (
          <ImageOff aria-hidden className="size-5 opacity-60" />
        )}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <h3 className="text-body font-medium">{t.label}</h3>
        <p id={noteId} className={error ? "text-secondary text-bad" : "text-secondary text-muted"}>
          {error ?? (url ? "Shown in the menu and on the sign-in page." : `No logo: ${fallback}`)}
        </p>
      </div>
      <div className="flex shrink-0 flex-wrap gap-2">
        <input
          ref={input}
          type="file"
          hidden
          tabIndex={-1}
          accept="image/png,image/jpeg,image/svg+xml"
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (!f) return;
            if (f.size > 256 << 10) setError(`${f.name} is larger than 256 KB, the most a logo is.`);
            else upload.mutate(f);
          }}
        />
        <Button
          size="sm"
          type="button"
          disabled={upload.isPending}
          aria-describedby={noteId}
          aria-label={`${url ? "Replace" : "Upload"} the ${t.label.toLowerCase()}'s logo`}
          onClick={() => input.current?.click()}
        >
          {upload.isPending ? <LoaderCircle className="animate-spin" /> : <Upload />}
          {url ? "Replace" : "Upload"}
        </Button>
        {url && (
          <Button
            size="sm"
            type="button"
            variant="danger"
            disabled={remove.isPending}
            aria-label={`Remove the ${t.label.toLowerCase()}'s logo`}
            onClick={() => remove.mutate()}
          >
            Remove
          </Button>
        )}
      </div>
    </div>
  );
}

export function BrandingPage() {
  const branding = useQuery(adm.branding());
  return (
    <Page>
      <PageHeader
        title="Branding"
        description="The portal's name, accent colour and logos. Everyone sees them, on the sign-in page too, and emails carry them."
      />
      {branding.isError ? (
        <ErrorState error={branding.error} onRetry={() => void branding.refetch()} />
      ) : !branding.data ? (
        <p className="text-secondary text-muted">Loading the branding…</p>
      ) : (
        <>
          <NameAndAccent branding={branding.data} />
          <Panel
            title="Logos"
            description="PNG, JPEG or SVG, 256 KB and 2048 pixels a side at most. An SVG with scripts, links or external pictures is refused. Emails show the light theme's logo when it is a PNG or a JPEG."
          >
            <div className="flex flex-col divide-y divide-line">
              <LogoRow variant="light" branding={branding.data} />
              <LogoRow variant="dark" branding={branding.data} />
            </div>
          </Panel>
        </>
      )}
    </Page>
  );
}
