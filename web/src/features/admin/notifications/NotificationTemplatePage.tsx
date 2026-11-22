// A notification kind's template for one channel and language (ADR-0059):
// edited as Go templates, previewed with the kind's sample data as it would
// be sent, and reset to the built-in one.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "@tanstack/react-router";
import { useState } from "react";
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
import { channelLabel, channels, localeLabel, locales } from "./templateLabels";

const textHint: Record<TemplateChannel, string> = {
  email: "The email's text part, for mail clients that don't show HTML.",
  teams: "The card's text, in Markdown: what variables hold is escaped, so it can't add links or emphasis.",
  slack: "The message's text, in Slack's mrkdwn: what variables hold is escaped.",
};

const partsOf = (t: TemplateParts, channel: TemplateChannel): TemplateParts => ({
  subject: t.subject,
  text: t.text,
  ...(channel === "email" ? { html: t.html ?? "" } : {}),
});

const same = (a: TemplateParts, b: TemplateParts) =>
  a.subject === b.subject && a.text === b.text && (a.html ?? "") === (b.html ?? "");

const variableColumns: Column<TemplateVariable>[] = [
  {
    id: "name",
    header: "Variable",
    cell: (v) => <code className="font-mono text-secondary break-all">{v.name}</code>,
  },
  { id: "type", header: "Type", cell: (v) => <span className="text-secondary text-muted">{v.type}</span> },
  { id: "description", header: "What it holds", cell: (v) => <span className="text-secondary">{v.description}</span> },
];

const functionColumns: Column<TemplateVariable>[] = [
  { ...variableColumns[0]!, header: "Function" },
  { ...variableColumns[2]!, header: "What it does" },
];

function Editor({
  kind,
  template,
  onChanged,
}: {
  kind: NotificationKind;
  template: NotificationTemplate;
  onChanged: (t: NotificationTemplate) => void;
}) {
  const { channel, locale } = template;
  const saved = partsOf(template, channel);
  const [draft, setDraft] = useState<TemplateParts>(saved);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [resetting, setResetting] = useState(false);
  const body = { ...draft, channel, locale };
  const save = useMutation({
    mutationFn: () => call(api.PUT("/admin/notification-templates/{kind}", { params: { path: { kind } }, body })),
    onSuccess: (t) => {
      setErrors({});
      onChanged(t);
      toast.success("Template saved", { description: "Notifications sent from now on use it." });
    },
    onError: (e) => {
      setErrors(fieldErrors(e));
      if (!isApiError(e) || e.fieldErrors.length === 0) failed(e, "The template wasn't saved");
    },
  });
  const reset = useMutation({
    mutationFn: () =>
      call(
        api.DELETE("/admin/notification-templates/{kind}", {
          params: { path: { kind }, query: { channel, locale } },
        }),
      ),
    onSuccess: (t) => {
      setResetting(false);
      onChanged(t);
      toast.success("Built-in template back");
    },
    onError: (e) => {
      setResetting(false);
      failed(e, "The template wasn't reset");
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
  const status = template.edited
    ? `Edited${template.updated_by ? ` by ${template.updated_by}` : ""}${template.updated_at ? `, ${relative(template.updated_at)}` : ""}.`
    : "The built-in template.";
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-2">
      <Panel
        title="Template"
        description={
          <>
            A Go template: <code className="font-mono">{"{{.Request.Number}}"}</code> writes a variable,{" "}
            <code className="font-mono">{"{{if .Approval.Name}}…{{end}}"}</code> writes what it holds when the variable
            has a value.
          </>
        }
        footer={
          <>
            <span className="mr-auto">{dirty ? "Not saved yet." : status}</span>
            {(template.edited || !isDefault) && (
              <Button
                type="button"
                variant="quiet"
                onClick={() => (template.edited ? setResetting(true) : setDraft(partsOf(template.default, channel)))}
              >
                Reset to built-in
              </Button>
            )}
            <Button type="submit" form="template-editor" variant="primary" disabled={save.isPending || !dirty}>
              {save.isPending ? "Saving…" : "Save template"}
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
            label={channel === "email" ? "Subject" : "Title"}
            htmlFor="template-subject"
            hint={channel === "email" ? "The email's subject." : "The bold first line."}
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
          <Field label="Text" htmlFor="template-text" hint={textHint[channel]} error={errors["/text"]}>
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
            <Field
              label="HTML"
              htmlFor="template-html"
              hint="What the email shows between the greeting and the facts. What variables hold is escaped as HTML."
              error={errors["/html"]}
            >
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
        title="Preview"
        description={`With sample data, as ${channel === "email" ? "an email" : `a ${channelLabel[channel]} message`} in ${localeLabel[locale]} would show it.`}
      >
        <TemplatePreview channel={channel} preview={preview} />
      </Panel>
      {resetting && (
        <ConfirmDialog
          open
          onOpenChange={(o) => !o && setResetting(false)}
          title="Go back to the built-in template?"
          description={`The edited ${channelLabel[channel]} template in ${localeLabel[locale]} is removed.`}
          consequences="Notifications sent from then on use rvav's built-in template."
          confirmLabel="Reset to built-in"
          pending={reset.isPending}
          onConfirm={() => reset.mutate()}
        />
      )}
    </div>
  );
}

function Variables({ kind, functions }: { kind: NotificationTemplateKind; functions: TemplateVariable[] }) {
  return (
    <div className="grid min-w-0 gap-4 xl:grid-cols-[3fr_2fr]">
      <Panel
        title="Variables"
        description={`What a template of ${kind.name.toLowerCase()} can write. A template that names another is refused.`}
        flush
      >
        <DataTable
          label="Variables"
          columns={variableColumns}
          rows={kind.variables}
          rowKey={(v) => v.name}
          minWidth={520}
        />
      </Panel>
      <Panel
        title="Functions"
        description={
          <>
            Besides Go&apos;s own, such as <code className="font-mono">eq</code>,{" "}
            <code className="font-mono">printf</code> and <code className="font-mono">range</code>.
          </>
        }
        flush
      >
        <DataTable label="Functions" columns={functionColumns} rows={functions} rowKey={(v) => v.name} minWidth={360} />
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
  const qc = useQueryClient();
  const navigate = useNavigate();
  const catalog = useQuery(adm.notificationTemplates());
  const template = useQuery(adm.notificationTemplate(kind, channel, locale));
  const info = catalog.data?.kinds.find((k) => k.kind === kind);
  const go = (search: { channel: TemplateChannel; locale: TemplateLocale }) =>
    void navigate({ to: "/admin/notification-templates/$kind", params: { kind }, search, replace: true });
  const changed = (t: NotificationTemplate) => {
    qc.setQueryData(adm.notificationTemplate(kind, channel, locale).queryKey, t);
    void qc.invalidateQueries({ queryKey: adm.notificationTemplates().queryKey });
  };
  const error = catalog.error ?? template.error;
  return (
    <Page>
      <PageHeader
        breadcrumbs={
          <Breadcrumbs>
            <Link to="/admin/notification-templates">Notification templates</Link>
          </Breadcrumbs>
        }
        title={info?.name ?? "Notification template"}
        description={info?.when}
      />
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        <Segmented
          label="Channel"
          value={channel}
          options={channels.map((c) => ({ value: c, label: channelLabel[c] }))}
          onChange={(c) => go({ channel: c, locale })}
        />
        <Segmented
          label="Language"
          value={locale}
          options={locales.map((l) => ({ value: l, label: localeLabel[l] }))}
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
        <p className="text-secondary text-muted">Loading the template…</p>
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
