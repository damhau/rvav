# ADR-0059: Notification templates and branding

- Status: accepted
- Date: 2026-11-19
- Builds on the notifications of [ADR-0023](0023-notifications.md), the
  administration of [ADR-0020](0020-administration.md) and the custom
  roles of [ADR-0034](0034-custom-roles.md). O1.5 (#171), vRA parity A31
  and A32.

## Context
vRA lets administrators change the emails it sends and put their
company's name, logo and colours on the portal. rvav's notification
templates were files embedded in the binary ("Administrators don't edit
them yet", ADR-0023), every page said rvav in rvav's blue, and the
sign-in page knew nothing about the organisation it signs people into.

Two risks shape the design. A template is code an administrator writes:
a typo must not stop notifications, and text a requester typed (a
request title, a comment) must not become markup in an email, a Teams
card or a Slack message. A colour and a logo end up in every page: a
light accent makes text unreadable and fails the accessibility checks
the web app keeps clean, and an SVG can carry scripts.

## Decision

### What a template is
For each kind of notification (11 kinds, the test message excepted),
channel (`email`, `teams`, `slack`) and language (`en`, `fr`), a template
has:

| Part | Email | Teams | Slack |
|---|---|---|---|
| `subject` | The subject | The card's title | The header block |
| `text` | The plain-text body | The card's text, in Teams Markdown | The section, in Slack `mrkdwn` |
| `html` | The HTML body | — | — |

Parts are Go templates: `text/template` for the subject and texts,
`html/template` for the HTML. What rvav writes around them stays rvav's:
an email's greeting, facts table, button and footer (its layout), and a
card's facts and buttons, so an approval's **Approve** and **Reject**
keep working whatever the text says.

The built-in templates stay in code (`internal/notify/templates`, YAML
per kind and language). An edited template, saved in
`notification_templates`, overrides the built-in one for its kind,
channel and language; **Reset to built-in** deletes it. A rvav upgrade
that improves a built-in template reaches every template nobody edited.

### Variables
Each kind lists its variables, with their type and meaning, in the API
(`GET /api/v1/admin/notification-templates`) and the editor:

- every kind: `.Portal`, `.User.Name`, `.Link`, `.Preferences`,
  `.Title`, `.Locale`;
- kinds about a request: `.Request.Number`, `.Request.Title`,
  `.Request.Item`, `.Project.Name`, `.Project.Environment`;
- kinds about a deployment: `.Deployment.Name`, `.Project.*`;
- per kind: `.Approval.Name`, `.Approval.Reasons` (a list) and
  `.Approval.Why` for approvals, `.Lease.*` for leases, `.Ready` for a
  request done, `.PreviousOwner` for a handover, `.Body` where the
  notification has one.

Two functions help: `date` writes a date in the template's language, and
`join` joins a list.

### Checked before saving
Saving (`PUT /api/v1/admin/notification-templates/{kind}`) refuses, with
the part and the line:

- a part that doesn't parse;
- a field that isn't one of its kind's variables, in any branch: rvav
  walks the parse tree, following `with`, `range` and `$variables`, so
  `{{if .Ready}}{{.Requst.Number}}{{end}}` is refused even though the
  sample takes the other branch;
- `{{define}}`, `{{block}}` and `{{template}}`, a `range` over
  anything but a list variable, and a `range` inside another, which
  writes a list's length squared, or to the power of how deep it goes;
- an empty subject or text, an HTML part on Teams or Slack, a part over
  1 KB (subject), 20 KB (text) or 200 KB (HTML);
- a template that fails, or writes nothing, with the kind's sample data.

The same checks run on a preview (`POST …/{kind}/preview`), which writes
the template with the kind's sample data and the portal's branding: an
email's subject, text and HTML (shown in a sandboxed `iframe`, scripts
off), a Teams card's JSON or Slack's blocks.

If an edited template still fails when a notification is sent, such as
one written into the database by hand, rvav logs a warning and sends the
built-in template: a template never stops a notification.

What a template writes is bounded too, since the sample's lists are
short and a real one can be long: the subject and text fail past 64 KB
written, the HTML past 512 KB. `printf` is Go's with widths and
precisions of 100 at most, and none taken from the data (`*`): a width
of a million builds a megabyte in one call, before the limit counts it.

### Escaping per channel
Values reach templates escaped for the channel; what the administrator
wrote is kept as markup:

- HTML: `html/template` escapes values in their context.
- Teams: values are escaped for Markdown.
- Slack: values have `&`, `<` and `>` escaped, so `<!channel>` in a title
  stays text; a composite's message (`step_message`) is sent as plain
  text, as before. The message's top-level text, which Slack shows in
  its notifications, is the subject with `&`, `<` and `>` escaped.
- Subjects are plain text everywhere; line breaks are folded.

### Branding
**Administration › Branding** (`PUT /api/v1/admin/branding`) sets:

- **Portal name:** 40 characters at most, no control characters; empty
  is rvav. It shows in the menu, the sign-in page, the browser's title,
  emails (the logo's alternative text, the footer, and the sender's name
  when `RVAV_SMTP_FROM` is an address alone) and the chat buttons
  (**Open in Corp Cloud**).
- **Accent colour** (`#rrggbb`): the colour of links, buttons and the
  selection, in both themes. rvav computes each theme's palette (the
  accent, the text on it, a tinted background) and checks WCAG 2.2's
  4.5:1 for text against every background the web app puts accent text
  on. In the light theme the colour is used as given or refused, with the
  closest darker colour that passes as a suggestion; the dark theme takes
  it lightened as much as it needs. `POST /api/v1/admin/branding/accent-check`
  checks a colour before saving.
- **Logos**, one for the light theme and one for the dark theme
  (`PUT /api/v1/admin/branding/logos/{variant}`, the picture as the
  body): PNG, JPEG or SVG, 256 KB and 2048 pixels a side at most. An SVG
  is parsed and refused unless it holds only drawing elements (shapes,
  text, paint, filters, embedded raster pictures) and their attributes:
  no scripts, event handlers, links, animations, `foreignObject`,
  DOCTYPE or entities, no style that imports or fetches, and no reference
  outside the file. A style is read as the browser reads it: its text
  joined across the comments that split it, no element inside it, and
  no CSS escape (`\`), there or in an attribute, since one could spell
  `url(` unseen; `image-set()`, `image()`, `cross-fade()`, `src()` and
  `element()` fetch without `url(`. Drawing tools' metadata (Inkscape,
  Illustrator) is accepted. It is refused rather than cleaned, so what
  is saved is what the administrator checked. A logo is served with its
  own type, `nosniff` and a sandboxing `Content-Security-Policy`. The
  dark theme shows the light logo when there is no dark one.

The check is a filter, which a new CSS feature could pass. What holds
then is how a logo is shown: the web app shows it only through `<img>`,
where browsers run no script and fetch nothing an SVG refers to; emails
carry PNG and JPEG logos only; and the logo's own URL, opened directly,
answers with `default-src 'none'` and `sandbox`.

Emails carry the light logo inline (`cid:`) when it is a PNG or JPEG;
mail clients that block SVG get the portal's name instead. The email
button takes the accent.

Changes are audited: `settings.branding.update`,
`settings.branding_logo.set`, `settings.branding_logo.remove`,
`settings.notification_template.update` and
`settings.notification_template.reset`, in the `settings` category of
audit forwarding. Both pages need `system.manage` (administrators).

### For anyone
`GET /api/v1/branding` answers without a sign-in, for the sign-in page:
the name, both palettes and the logos' URLs. It has an ETag and
`Cache-Control: no-cache`, so browsers revalidate it on every load and
get a 304 while it is the same. A logo's URL names its content
(`?v=<hash>`), so it is cached for good and a new logo is a new URL. The
web app reads the branding before it renders and keeps rvav's own colours
when no accent is set.

## Consequences
- Administrators change what people receive without a release, per
  language, and see it before saving.
- Edits don't follow rvav's improvements to a built-in template: the
  list shows which templates are edited, and **Reset to built-in** goes
  back.
- A third language needs its built-in templates and layout first.
- A brand colour that fails the light theme can't be used as is: the
  suggestion is the nearest that can.

## Alternatives considered
- **Whole-message templates (layout included).** More freedom, but an
  administrator could drop the approval buttons, the facts or the
  preferences link. Rejected.
- **Checking fields only by running the sample.** Misses branches the
  sample doesn't take. The parse tree is walked instead.
- **Cleaning SVG logos.** A cleaner that rewrites the file changes what
  was approved and must be as good as a browser's parser. Refusing is
  simpler to trust.
- **Deriving both themes from the colour.** Changing the light colour
  silently would show a different brand than the one chosen; it is
  refused there with a suggestion, and derived only for the dark theme.
