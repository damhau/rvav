// The portal's branding (ADR-0059): its name, an accent colour that reads in
// both themes, and logos for each. The sign-in page, the menu, the browser's
// title and emails show them.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ImageOff, LoaderCircle, Upload } from "lucide-react";
import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Button } from "@/components/ui/button";
import { Field, FieldNote, Input } from "@/components/ui/input";
import { useSaveFocus } from "@/components/ui/opener";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type { AccentPalette, Branding, LogoVariant } from "@/lib/api/types";
import { applyBranding, rvavBranding } from "@/lib/branding";
import { formatNumber } from "@/lib/format";
import { useDebounced } from "@/lib/hooks";
import { adm } from "@/lib/query";

const themes = {
  light: { surface: "#ffffff", fg: "#14191c", line: "#d5dbdf" },
  dark: { surface: "#161b1e", fg: "#e4e9ec", line: "#273036" },
} as const;

const hexColour = /^#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})$/;

const twoDecimals = { minimumFractionDigits: 2, maximumFractionDigits: 2 };

/** The contrast text needs, as the server's brand.MinContrast. */
const minContrast = 4.5;

/** A colour as the server writes it: #rrggbb, in lower case. */
function normalHex(colour: string) {
  const hex = colour.trim().slice(1).toLowerCase();
  return `#${hex.length === 3 ? [...hex].map((c) => c + c).join("") : hex}`;
}

/** How the accent looks in one theme: a button, a link and the current page in the menu. */
function Swatch({ theme, palette }: { theme: keyof typeof themes; palette: AccentPalette }) {
  const { t } = useTranslation("admin");
  const colours = themes[theme];
  return (
    <figure className="m-0 flex min-w-0 flex-1 flex-col gap-1.5">
      <figcaption className="text-caption text-muted">
        {t(`branding.theme.${theme}`)} <span className="font-mono">{palette.accent}</span>
      </figcaption>
      <div
        className="flex flex-wrap items-center gap-3 rounded-md border p-3 text-secondary"
        style={{ background: colours.surface, color: colours.fg, borderColor: colours.line }}
      >
        <span
          className="rounded-md px-3 py-1.5 font-medium"
          style={{ background: palette.accent, color: palette.accent_fg }}
        >
          {t("branding.sample.submit")}
        </span>
        <span className="underline" style={{ color: palette.accent }}>
          {t("branding.sample.link")}
        </span>
        <span className="rounded-md px-2 py-1" style={{ background: palette.accent_soft, color: colours.fg }}>
          {t("branding.sample.current")}
        </span>
      </div>
    </figure>
  );
}

function AccentCheckNote({ accent, onUse }: { accent: string; onUse: (colour: string) => void }) {
  const { t } = useTranslation("admin");
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
        <p>{t("branding.own")}</p>
        <div className="flex flex-wrap gap-3">
          <Swatch theme="light" palette={rvavBranding.light} />
          <Swatch theme="dark" palette={rvavBranding.dark} />
        </div>
      </>
    );
  else if (!valid) note = <p>{t("branding.format")}</p>;
  else if (check.isPending) note = <p>{t("branding.checking")}</p>;
  else if (check.isError) note = <p className="text-bad">{t("branding.checkFailed")}</p>;
  else if (!check.data.ok)
    note = (
      <>
        {check.data.ratio > 0 ? (
          <p className="text-bad">
            {t("branding.contrast", {
              colour: normalHex(wanted),
              ratio: formatNumber(check.data.ratio, twoDecimals),
              needed: formatNumber(minContrast, { minimumFractionDigits: 1 }),
            })}
            {check.data.suggestion && <> {t("branding.closest", { colour: check.data.suggestion })}</>}
          </p>
        ) : (
          <p lang="en" className="text-bad">
            {check.data.message}
          </p>
        )}
        {check.data.suggestion && (
          <Button size="sm" type="button" className="self-start" onClick={() => onUse(check.data.suggestion!)}>
            <span
              aria-hidden
              className="size-3 rounded-sm border border-line"
              style={{ background: check.data.suggestion }}
            />
            {t("branding.use", { colour: check.data.suggestion })}
          </Button>
        )}
      </>
    );
  else
    note = (
      <>
        <p>
          {check.data.dark!.accent === check.data.light!.accent
            ? t("branding.readsSame", { ratio: formatNumber(check.data.ratio, twoDecimals) })
            : t("branding.readsLighter", {
                ratio: formatNumber(check.data.ratio, twoDecimals),
                dark: check.data.dark!.accent,
              })}
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
  const { t } = useTranslation("admin");
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
      toast.success(t("branding.saved"));
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("branding.notSaved"));
    },
  });
  const form = useRef<HTMLFormElement>(null);
  const submit = useRef<HTMLButtonElement>(null);
  const accentInput = useRef<HTMLInputElement>(null);
  const saveFocus = useSaveFocus(form, save.isPending, () => submit.current);
  const picked = hexColour.test(accent.trim()) ? accent.trim() : branding.light.accent;
  const full = (c: string) => (c.length === 4 ? "#" + [...c.slice(1)].map((x) => x + x).join("") : c.toLowerCase());
  return (
    <Panel
      title={t("branding.nameAccent")}
      description={t("branding.nameAccentDescription")}
      footer={
        <>
          <Button type="button" variant="quiet" disabled={!accent} onClick={() => setAccent("")}>
            {t("branding.useRvav")}
          </Button>
          <Button ref={submit} type="submit" form="branding-settings" variant="primary" disabled={save.isPending}>
            {save.isPending ? t("integrations.saving") : t("branding.save")}
          </Button>
        </>
      }
    >
      <form
        ref={form}
        id="branding-settings"
        className="flex max-w-[72ch] flex-col gap-4"
        onKeyDownCapture={saveFocus.interacted}
        onPointerDownCapture={saveFocus.interacted}
        onSubmit={(e) => {
          e.preventDefault();
          if (save.isPending) return;
          saveFocus.submitted();
          save.mutate();
        }}
      >
        <Field
          label={t("branding.portalName")}
          htmlFor="branding-name"
          hint={t("branding.portalNameHint")}
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
            {t("branding.accent")}
          </label>
          <div className="flex items-center gap-2">
            <Input
              ref={accentInput}
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
              aria-label={t("branding.pick")}
              className="h-8 w-12 shrink-0 cursor-pointer rounded-md border border-line-strong bg-surface p-0.5"
              value={full(picked)}
              onChange={(e) => setAccent(e.target.value)}
            />
          </div>
          <FieldNote id="branding-accent-note" error={errors["/accent"]} hint={t("branding.accentHint")} />
        </div>
        <AccentCheckNote
          accent={accent}
          onUse={(colour) => {
            setAccent(colour);
            // The button goes once the colour passes: the field it filled takes the focus.
            accentInput.current?.focus();
          }}
        />
      </form>
    </Panel>
  );
}

function LogoRow({ variant, branding }: { variant: LogoVariant; branding: Branding }) {
  const { t } = useTranslation("admin");
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const noteId = useId();
  const [error, setError] = useState<string>();
  const url = variant === "light" ? branding.logo_light_url : branding.logo_dark_url;
  const colours = themes[variant];
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
    onSuccess: (b) => done(b, t(`branding.logo.${variant}.saved`)),
    onError: (e, f) =>
      setError(
        isApiError(e)
          ? (e.fieldErrors[0]?.message ?? e.problem.detail ?? e.problem.title)
          : t("branding.notUploaded", { name: f.name }),
      ),
  });
  const remove = useMutation({
    mutationFn: () => call(api.DELETE("/admin/branding/logos/{variant}", { params: { path: { variant } } })),
    onSuccess: (b) => done(b, t(`branding.logo.${variant}.removed`)),
    onError: (e) => failed(e, t("branding.notRemoved")),
  });
  const fallback =
    variant === "dark" && branding.logo_light_url ? t("branding.fallbackLight") : t("branding.fallbackMark");
  return (
    <div className="flex flex-col gap-2 py-3 first:pt-0 last:pb-0 sm:flex-row sm:items-center sm:gap-4">
      <div
        className="flex h-16 w-full shrink-0 items-center justify-center rounded-md border px-3 sm:w-48"
        style={{ background: colours.surface, borderColor: colours.line, color: colours.fg }}
      >
        {url ? (
          <img src={url} alt={t(`branding.logo.${variant}.alt`)} className="max-h-10 max-w-full object-contain" />
        ) : (
          <ImageOff aria-hidden className="size-5 opacity-60" />
        )}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <h3 className="text-body font-medium">{t(`branding.theme.${variant}`)}</h3>
        <p id={noteId} className={error ? "text-secondary text-bad" : "text-secondary text-muted"}>
          {error ?? (url ? t("branding.shown") : t("branding.noLogo", { fallback }))}
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
            if (f.size > 256 << 10) setError(t("branding.tooLarge", { name: f.name }));
            else upload.mutate(f);
          }}
        />
        <Button
          size="sm"
          type="button"
          disabled={upload.isPending}
          aria-describedby={noteId}
          aria-label={url ? t(`branding.logo.${variant}.replace`) : t(`branding.logo.${variant}.upload`)}
          onClick={() => input.current?.click()}
        >
          {upload.isPending ? <LoaderCircle className="animate-spin" /> : <Upload />}
          {url ? t("branding.replace") : t("branding.upload")}
        </Button>
        {url && (
          <Button
            size="sm"
            type="button"
            variant="danger"
            disabled={remove.isPending}
            aria-label={t(`branding.logo.${variant}.remove`)}
            onClick={() => remove.mutate()}
          >
            {t("branding.remove")}
          </Button>
        )}
      </div>
    </div>
  );
}

export function BrandingPage() {
  const { t } = useTranslation(["admin", "shell"]);
  const branding = useQuery(adm.branding());
  return (
    <Page>
      <PageHeader title={t("shell:nav.admin.branding")} description={t("branding.description")} />
      {branding.isError ? (
        <ErrorState error={branding.error} onRetry={() => void branding.refetch()} />
      ) : !branding.data ? (
        <p className="text-secondary text-muted">{t("branding.loading")}</p>
      ) : (
        <>
          <NameAndAccent branding={branding.data} />
          <Panel title={t("branding.logos")} description={t("branding.logosDescription")}>
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
