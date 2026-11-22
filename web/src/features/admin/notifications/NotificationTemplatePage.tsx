// A notification kind's template for one channel and language (ADR-0059):
// edited as Go templates, previewed with the kind's sample data as it would
// be sent, and reset to the built-in one.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import type { TFunction } from "i18next";
import { useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { toast } from "sonner";
import { ErrorState } from "@/components/common/feedback";
import { Page } from "@/components/common/page";
import { Breadcrumbs } from "@/components/ui/breadcrumbs";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type Column } from "@/components/ui/data-table";
import { Field, Input, Textarea } from "@/components/ui/input";
import { PageHeader } from "@/components/ui/page-header";
import { Panel } from "@/components/ui/panel";
import { Segmented } from "@/components/ui/segmented";
import { failed, fieldErrors } from "@/features/admin/governance/common";
import { api, call, isApiError } from "@/lib/api/client";
import type {
  NotificationKind,
  NotificationTemplate,
  NotificationTemplateKind,
  TemplateChannel,
  TemplateLocale,
  TemplateParts,
  TemplateVariable,
} from "@/lib/api/types";
import { useDebounced } from "@/lib/hooks";
import { adm } from "@/lib/query";
import { relative } from "@/lib/format";
import { TemplatePreview } from "./TemplatePreview";
import { channelLabel, channels, localeInText, localeLabel, locales } from "./templateLabels";
import { kindName, kindWhen } from "@/lib/i18n/names";

const partsOf = (t: TemplateParts, channel: TemplateChannel): TemplateParts => ({
  subject: t.subject,
  text: t.text,
  ...(channel === "email" ? { html: t.html ?? "" } : {}),
});

const same = (a: TemplateParts, b: TemplateParts) =>
  a.subject === b.subject && a.text === b.text && (a.html ?? "") === (b.html ?? "");

const nameColumn = (header: string): Column<TemplateVariable> => ({
  id: "name",
  header,
  cell: (v) => <code className="font-mono text-secondary break-all">{v.name}</code>,
});

const descriptionColumn = (header: string): Column<TemplateVariable> => ({
  id: "description",
  header,
  cell: (v) => (
    <span lang="en" className="text-secondary">
      {v.description}
    </span>
  ),
});

const variableColumns = (t: TFunction<"admin-notifications">): Column<TemplateVariable>[] => [
  nameColumn(t("editor.variable")),
  { id: "type", header: t("editor.type"), cell: (v) => <span className="text-secondary text-muted">{v.type}</span> },
  descriptionColumn(t("editor.holds")),
];

const functionColumns = (t: TFunction<"admin-notifications">): Column<TemplateVariable>[] => [
  nameColumn(t("editor.function")),
  descriptionColumn(t("editor.does")),
];

/** Whether and when the template was edited, and by whom. */
function statusText(t: TFunction<"admin-notifications">, template: NotificationTemplate) {
  if (!template.edited) return t("editor.builtIn");
  const who = template.updated_by;
  const when = template.updated_at ? relative(template.updated_at) : undefined;
  if (who && when) return t("editor.editedByWhen", { who, when });
  if (who) return t("editor.editedBy", { who });
  if (when) return t("editor.editedWhen", { when });
  return t("editor.edited");
}

function Editor({
  kind,
  template,
  onChanged,
}: {
  kind: NotificationKind;
  template: NotificationTemplate;
  onChanged: (t: NotificationTemplate) => void;
}) {
  const { t } = useTranslation("admin-notifications");
  const { channel, locale } = template;
  const saved = partsOf(template, channel);
  const [draft, setDraft] = useState<TemplateParts>(saved);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [resetting, setResetting] = useState(false);
  const body = { ...draft, channel, locale };
  const save = useMutation({
    mutationFn: () => call(api.PUT("/admin/notification-templates/{kind}", { params: { path: { kind } }, body })),
    onSuccess: (saved) => {
      setErrors({});
      onChanged(saved);
      toast.success(t("editor.saved"), { description: t("editor.savedBody") });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, t("editor.notSaved"));
    },
  });
  const reset = useMutation({
    mutationFn: () =>
      call(
        api.DELETE("/admin/notification-templates/{kind}", {
          params: { path: { kind }, query: { channel, locale } },
        }),
      ),
    onSuccess: (back) => {
      setResetting(false);
      onChanged(back);
      toast.success(t("editor.reset"));
    },
    onError: (e) => {
      setResetting(false);
      failed(e, t("editor.notReset"));
    },
  });
  const shown = useDebounced(body, 400);
  const preview = useQuery({
    queryKey: ["admin", "notification-templates", kind, "preview", shown],
    queryFn: ({ signal }) =>
      call(
        api.POST("/admin/notification-templates/{kind}/preview", { params: { path: { kind } }, body: shown, signal }),
      ),
    placeholderData: keepPreviousData,
    retry: false,
  });
  const set = (part: keyof TemplateParts) => (e: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>) =>
    setDraft((d) => ({ ...d, [part]: e.target.value }));
  const dirty = !same(draft, saved);
  const isDefault = same(draft, partsOf(template.default, channel));
  const status = statusText(t, template);
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-2">
      <Panel
        title={t("editor.template")}
        description={
          <Trans
            t={t}
            i18nKey="editor.templateLead"
            values={{ write: "{{.Request.Number}}", when: "{{if .Approval.Name}}…{{end}}" }}
            components={{ code: <code className="font-mono" /> }}
          />
        }
        footer={
          <>
            <span className="mr-auto">{dirty ? t("editor.notSavedYet") : status}</span>
            {(template.edited || !isDefault) && (
              <Button
                type="button"
                variant="quiet"
                onClick={() => (template.edited ? setResetting(true) : setDraft(partsOf(template.default, channel)))}
              >
                {t("editor.resetToBuiltIn")}
              </Button>
            )}
            <Button type="submit" form="template-editor" variant="primary" disabled={save.isPending || !dirty}>
              {save.isPending ? t("editor.saving") : t("editor.save")}
            </Button>
          </>
        }
      >
        <form
          id="template-editor"
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field
            label={channel === "email" ? t("editor.subject") : t("editor.title")}
            htmlFor="template-subject"
            hint={channel === "email" ? t("editor.subjectHint") : t("editor.titleHint")}
            error={errors["/subject"]}
          >
            <Input
              id="template-subject"
              className="font-mono"
              spellCheck={false}
              value={draft.subject}
              onChange={set("subject")}
            />
          </Field>
          <Field
            label={t("editor.text")}
            htmlFor="template-text"
            hint={t(`editor.textHint.${channel}`)}
            error={errors["/text"]}
          >
            <Textarea
              id="template-text"
              className="font-mono"
              rows={8}
              spellCheck={false}
              value={draft.text}
              onChange={set("text")}
            />
          </Field>
          {channel === "email" && (
            <Field label={t("editor.html")} htmlFor="template-html" hint={t("editor.htmlHint")} error={errors["/html"]}>
              <Textarea
                id="template-html"
                className="font-mono"
                rows={10}
                spellCheck={false}
                value={draft.html ?? ""}
                onChange={set("html")}
              />
            </Field>
          )}
        </form>
      </Panel>
      <Panel
        title={t("editor.preview")}
        description={
          channel === "email"
            ? t("editor.previewEmail", { locale: localeInText(locale) })
            : t("editor.previewMessage", { channel: channelLabel(channel), locale: localeInText(locale) })
        }
      >
        <TemplatePreview channel={channel} preview={preview} />
      </Panel>
      {resetting && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setResetting(false)}
          title={t("editor.resetTitle")}
          description={t("editor.resetBody", { channel: channelLabel(channel), locale: localeInText(locale) })}
          consequences={t("editor.resetConsequences")}
          confirmLabel={t("editor.resetToBuiltIn")}
          pending={reset.isPending}
          onConfirm={() => reset.mutate()}
        />
      )}
    </div>
  );
}

function Variables({ kind, functions }: { kind: NotificationTemplateKind; functions: TemplateVariable[] }) {
  const { t } = useTranslation("admin-notifications");
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-[3fr_2fr]">
      <Panel title={t("editor.variables")} description={t("editor.variablesLead", { kind: kindName(kind) })} flush>
        <DataTable
          label={t("editor.variables")}
          columns={variableColumns(t)}
          rows={kind.variables}
          rowKey={(v) => v.name}
          minWidth={520}
        />
      </Panel>
      <Panel
        title={t("editor.functions")}
        description={
          <Trans t={t} i18nKey="editor.functionsLead" components={{ code: <code className="font-mono" /> }} />
        }
        flush
      >
        <DataTable
          label={t("editor.functions")}
          columns={functionColumns(t)}
          rows={functions}
          rowKey={(v) => v.name}
          minWidth={360}
        />
      </Panel>
    </div>
  );
}

export function NotificationTemplatePage({
  kind,
  channel = "email",
  locale = "en",
}: {
  kind: NotificationKind;
  channel?: TemplateChannel;
  locale?: TemplateLocale;
}) {
  const { t } = useTranslation(["admin-notifications", "shell"]);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const catalog = useQuery(adm.notificationTemplates());
  const template = useQuery(adm.notificationTemplate(kind, channel, locale));
  const info = catalog.data?.kinds.find((k) => k.kind === kind);
  const go = (search: { channel: TemplateChannel; locale: TemplateLocale }) =>
    void navigate({ to: "/admin/notification-templates/$kind", params: { kind }, search, replace: true });
  const changed = (next: NotificationTemplate) => {
    qc.setQueryData(adm.notificationTemplate(kind, channel, locale).queryKey, next);
    void qc.invalidateQueries({ queryKey: adm.notificationTemplates().queryKey });
  };
  const error = catalog.error ?? template.error;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/notification-templates">{t("shell:nav.admin.notificationTemplates")}</Link>
          </Breadcrumbs>
        }
        title={info ? kindName(info) : t("editor.fallback")}
        description={info && kindWhen(info)}
      />
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        <Segmented
          label={t("editor.channelLabel")}
          value={channel}
          options={channels.map((c) => ({ value: c, label: channelLabel(c) }))}
          onChange={(c) => go({ channel: c, locale })}
        />
        <Segmented
          label={t("editor.languageLabel")}
          value={locale}
          options={locales.map((l) => ({ value: l, label: localeLabel(l) }))}
          onChange={(l) => go({ channel, locale: l })}
        />
      </div>
      {error ? (
        <ErrorState
          error={error}
          onRetry={() => {
            void catalog.refetch();
            void template.refetch();
          }}
        />
      ) : !template.data || !info ? (
        <p className="text-secondary text-muted">{t("editor.loading")}</p>
      ) : (
        <>
          <Editor
            key={`${channel}/${locale}/${template.data.edited}/${template.data.updated_at ?? ""}`}
            kind={kind}
            template={template.data}
            onChanged={changed}
          />
          <Variables kind={info} functions={catalog.data!.functions} />
        </>
      )}
    </Page>
  );
}
