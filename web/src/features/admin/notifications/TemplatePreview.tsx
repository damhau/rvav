// A notification as it would be sent: an email's HTML in a sandboxed frame
// (no scripts, no same origin) and its text part, a Teams card or a Slack
// message drawn simply, and their JSON.
import type { UseQueryResult } from "@tanstack/react-query";
import { Fragment, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { isApiError } from "@/lib/api/client";
import type { NotificationTemplatePreview, TemplateChannel } from "@/lib/api/types";

type Rule = [RegExp, (m: RegExpExecArray, key: number) => ReactNode];

/** Inline markup as a small set of rules, each the first match wins; the rest is text. */
function inline(text: string, rules: Rule[]): ReactNode[] {
  const out: ReactNode[] = [];
  let rest = text;
  let key = 0;
  while (rest) {
    let best: { at: number; m: RegExpExecArray; render: Rule[1] } | undefined;
    for (const [re, render] of rules) {
      const m = new RegExp(re.source, re.flags.replace("g", "")).exec(rest);
      if (m && (!best || m.index < best.at)) best = { at: m.index, m, render };
    }
    if (!best) {
      out.push(rest);
      break;
    }
    if (best.at > 0) out.push(rest.slice(0, best.at));
    out.push(best.render(best.m, key++));
    rest = rest.slice(best.at + best.m[0].length);
  }
  return out;
}

const unescapeMarkdown = (s: string) => s.replace(/\\([\\[\]()*_`#>~])/g, "$1");

// Teams' Markdown: **bold**, _italic_, [text](link) and backslash escapes.
const teamsRules: Rule[] = [
  [/\\([\\[\]()*_`#>~])/, (m) => m[1]],
  [/\*\*(.+?)\*\*/, (m, k) => <strong key={k}>{unescapeMarkdown(m[1]!)}</strong>],
  [/(?<![\w\\])_(.+?)_(?!\w)/, (m, k) => <em key={k}>{unescapeMarkdown(m[1]!)}</em>],
  [
    /\[([^\]]+)\]\(([^)\s]+)\)/,
    (m, k) => (
      <span key={k} className="text-accent underline">
        {m[1]}
      </span>
    ),
  ],
];

const unescapeSlack = (s: string) => s.replaceAll("&lt;", "<").replaceAll("&gt;", ">").replaceAll("&amp;", "&");

// Slack's mrkdwn: *bold*, _italic_, <link|text> and its three escapes.
const slackRules: Rule[] = [
  [/(?<!\w)\*([^*\n]+)\*(?!\w)/, (m, k) => <strong key={k}>{unescapeSlack(m[1]!)}</strong>],
  [/(?<!\w)_([^_\n]+)_(?!\w)/, (m, k) => <em key={k}>{unescapeSlack(m[1]!)}</em>],
  [
    /<([^|>]+)(?:\|([^>]+))?>/,
    (m, k) => (
      <span key={k} className="text-accent underline">
        {unescapeSlack(m[2] ?? m[1]!)}
      </span>
    ),
  ],
  [/&(lt|gt|amp);/, (m) => ({ lt: "<", gt: ">", amp: "&" })[m[1] as "lt" | "gt" | "amp"]],
];

function Lines({ text, rules }: { text: string; rules: Rule[] }) {
  return (
    <>
      {text.split("\n").map((line, i) => (
        <Fragment key={i}>
          {i > 0 && <br />}
          {inline(line, rules)}
        </Fragment>
      ))}
    </>
  );
}

const button = "inline-flex h-7 items-center rounded-md border border-line-strong px-2.5 text-secondary font-medium";

type Item = Record<string, unknown>;
const str = (v: unknown) => (typeof v === "string" ? v : "");
const items = (v: unknown) => (Array.isArray(v) ? (v as Item[]) : []);

function TeamsCard({ card }: { card: Item }) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-line bg-surface p-4 shadow-pop">
      {items(card.body).map((b, i) => {
        if (b.type === "TextBlock")
          return (
            <p key={i} className={b.weight === "Bolder" ? "text-section font-semibold" : "text-body"}>
              <Lines text={str(b.text)} rules={teamsRules} />
            </p>
          );
        if (b.type === "FactSet")
          return (
            <dl key={i} className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-0.5 text-secondary">
              {items(b.facts).map((f, j) => (
                <Fragment key={j}>
                  <dt className="font-medium">{unescapeMarkdown(str(f.title))}</dt>
                  <dd className="m-0 min-w-0 break-words">{unescapeMarkdown(str(f.value))}</dd>
                </Fragment>
              ))}
            </dl>
          );
        if (b.type === "Input.Text")
          return (
            <div key={i} className="rounded-md border border-line px-2 py-1.5 text-secondary text-muted">
              {str(b.placeholder)}
            </div>
          );
        return null;
      })}
      <div className="flex flex-wrap gap-2 pt-1">
        {items(card.actions).map((a, i) => (
          <span key={i} className={button}>
            {str(a.title)}
          </span>
        ))}
      </div>
    </div>
  );
}

function SlackMessage({ blocks }: { blocks: Item[] }) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-line bg-surface p-4">
      {blocks.map((b, i) => {
        const text = (b.text ?? {}) as Item;
        if (b.type === "header")
          return (
            <p key={i} className="text-section font-semibold">
              {str(text.text)}
            </p>
          );
        if (b.type === "section" && b.text)
          return (
            <p key={i} className="text-body">
              <Lines text={str(text.text)} rules={slackRules} />
            </p>
          );
        if (b.type === "section")
          return (
            <div key={i} className="grid gap-x-4 gap-y-2 text-secondary sm:grid-cols-2">
              {items(b.fields).map((f, j) => (
                <p key={j} className="min-w-0 break-words">
                  <Lines text={str(f.text)} rules={slackRules} />
                </p>
              ))}
            </div>
          );
        if (b.type === "actions")
          return (
            <div key={i} className="flex flex-wrap gap-2 pt-1">
              {items(b.elements).map((e, j) => (
                <span key={j} className={button}>
                  {str(((e.text ?? {}) as Item).text)}
                </span>
              ))}
            </div>
          );
        return null;
      })}
    </div>
  );
}

function Code({ label, children }: { label: string; children: string }) {
  return (
    <div
      role="region"
      aria-label={label}
      // Focusable so keyboard users can scroll it.
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
      tabIndex={0}
      className="max-h-[480px] overflow-auto rounded-md border border-line bg-sunken"
    >
      <pre className="m-0 p-3 font-mono text-caption whitespace-pre">{children}</pre>
    </div>
  );
}

function Views({ label, views }: { label: string; views: { value: string; label: string; body: ReactNode }[] }) {
  return (
    <Tabs defaultValue={views[0]!.value} className="flex min-w-0 flex-col gap-3">
      <TabsList aria-label={label}>
        {views.map((v) => (
          <TabsTrigger key={v.value} value={v.value}>
            {v.label}
          </TabsTrigger>
        ))}
      </TabsList>
      {views.map((v) => (
        <TabsContent key={v.value} value={v.value} className="min-w-0">
          {v.body}
        </TabsContent>
      ))}
    </Tabs>
  );
}

export function TemplatePreview({
  channel,
  preview,
}: {
  channel: TemplateChannel;
  preview: UseQueryResult<NotificationTemplatePreview>;
}) {
  const { t } = useTranslation("admin-notifications");
  const partName = (field: string) =>
    field === "/subject"
      ? channel === "email"
        ? t("editor.subject")
        : t("editor.title")
      : field === "/text"
        ? t("editor.text")
        : t("editor.html");
  if (preview.isError && !preview.isFetching) {
    const e = preview.error;
    const problems = isApiError(e) ? e.fieldErrors : [];
    return (
      <div role="status" className="flex flex-col gap-1 text-secondary">
        <p className="font-medium text-bad">{t("preview.cantWrite")}</p>
        {problems.length ? (
          <ul className="m-0 list-disc pl-5 text-bad">
            {problems.map((p) => (
              <li key={p.field}>{t("preview.problem", { field: partName(p.field), message: p.message })}</li>
            ))}
          </ul>
        ) : (
          <p className="text-bad">{isApiError(e) ? (e.problem.detail ?? e.problem.title) : t("preview.failed")}</p>
        )}
      </div>
    );
  }
  const p = preview.data;
  if (!p) return <p className="text-secondary text-muted">{t("preview.writing")}</p>;
  const subject = (
    <p className="text-secondary">
      <span className="text-muted">{channel === "email" ? t("preview.subject") : t("preview.title")}</span>
      <span className="font-medium">{p.subject}</span>
    </p>
  );
  if (channel === "email")
    return (
      <div className="flex min-w-0 flex-col gap-3">
        {subject}
        <Views
          label={t("preview.email")}
          views={[
            {
              value: "html",
              label: t("editor.html"),
              body: (
                <iframe
                  title={t("preview.emailHtml")}
                  sandbox=""
                  srcDoc={p.html ?? ""}
                  className="h-[520px] w-full rounded-md border border-line bg-white"
                />
              ),
            },
            { value: "text", label: t("editor.text"), body: <Code label={t("preview.emailText")}>{p.text}</Code> },
          ]}
        />
      </div>
    );
  if (channel === "teams")
    return (
      <div className="flex min-w-0 flex-col gap-3">
        {subject}
        <Views
          label={t("preview.teams")}
          views={[
            { value: "card", label: t("preview.card"), body: <TeamsCard card={p.card ?? {}} /> },
            {
              value: "json",
              label: "JSON",
              body: <Code label={t("preview.cardJson")}>{JSON.stringify(p.card, null, 2)}</Code>,
            },
          ]}
        />
      </div>
    );
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {subject}
      <Views
        label={t("preview.slack")}
        views={[
          { value: "message", label: t("preview.message"), body: <SlackMessage blocks={p.blocks ?? []} /> },
          {
            value: "json",
            label: "JSON",
            body: <Code label={t("preview.blocks")}>{JSON.stringify(p.blocks, null, 2)}</Code>,
          },
        ]}
      />
    </div>
  );
}
