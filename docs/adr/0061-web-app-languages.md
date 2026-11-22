# ADR-0061: The web app in English and French

- Status: accepted
- Date: 2026-11-22
- Builds on the notifications of [ADR-0023](0023-notifications.md), whose
  per-person locale already chose the language of what rvav sends, and on
  the contract-first slices of
  [ADR-0006](0006-contract-first-vertical-slices.md). O1.3 (#169), vRA
  parity A33.

## Context
vRA's portal speaks the languages of the people who use it. rvav sent
its notifications in English or French, as each person chose, but every
page was English, with its text written in the components: a French
speaker read French emails and an English portal.

Three things make a translation hard to keep. Text built from pieces
(`${n} request${n === 1 ? "" : "s"}`) can't be reordered in another
language and has English plural rules in it. Dates and numbers formatted
in one fixed language stay in it. And a folder converted once fills up
again with English the next time someone adds a button, unless something
refuses it.

## Decision

### i18next, a namespace per area
The web app uses i18next with react-i18next and the browser language
detector. Each area of the app has a namespace: `common`, `shell`,
`account`, `forms`, `canvas`, `catalog`, `requests`, `deployments`,
`approvals`, `resources`, `reports`, `overview`, `notifications`, and
`admin` with an `admin-<folder>` namespace for each administration
folder. Its texts are in `web/src/locales/<language>/<namespace>.json`.

Each language is one chunk, with all its namespaces, loaded before the
app renders and before a new language shows. A page never shows keys
while its texts load, and React doesn't suspend.

Keys say what the text is for (`tokens.revokeTitle`), not what it says,
so a reworded text keeps its key. Their types come from the English
files: `t("a.key")` fails the typecheck when English has no such key.

### One language for the app and what is sent
`NotificationPreferences.locale` becomes the person's language. `/me`
returns it as `Me.locale` once chosen, and `PUT /me/locale` sets it
without touching the channel rules. The account menu lists **English**
and **Français**, each in its own words and marked with `lang`. The
notification preferences keep their **Language** select, which sets the
same value.

The language shown is the person's choice, else the first of the
browser's languages that rvav speaks, else English. A regional code
(`fr-CH`) shows its language (`fr`). Choosing a language shows it at
once, without a reload. Every component that calls `useTranslation`
renders again and `<html lang>` follows. If the server refuses the
choice, the previous language comes back with a toast. The browser keeps
the choice under `rvav.locale` for the pages shown before `/me` answers,
such as the sign-in page and the loading screen. The app waits for the
person's language before its first page, so it doesn't flash in the
browser's.

### Whole sentences
A sentence is one text, with its values interpolated
(`"{{name}} is ready"`) and its plural forms chosen by `count`. English
has `_one` and `_other`, and French `_one`, `_many` and `_other`. Markup
inside a sentence goes through `<Trans>` with named tags, so a
translation can move the link. Values are interpolated after the markup
is parsed, so a name someone typed can't become markup. Lists are joined
by `formatList` (`a, b et c`), not by `join(", ")`.

### Dates and numbers in the language shown
`web/src/lib/format.ts` formats dates, times, relative times, numbers,
sizes and lists with `Intl`, in the language shown, and caches one
formatter per language and options. Elsewhere, ESLint refuses
`toLocaleString`, `toLocaleDateString`, `toLocaleTimeString` and
`new Intl.*Format`.

### Checks that keep it whole
- **`resources.test.ts`** compares every namespace in English and in
  French. It checks for the same keys, no empty text, the plural forms
  each language needs, and the same variables and tags. It also checks
  French typography: a no-break space before `: ; ! ?` and `»`, and the
  straight apostrophe.
- **ESLint's `i18next/no-literal-string`** refuses literal text in JSX
  and in the attributes a person reads or hears (`aria-label`, `title`,
  `placeholder`, `alt`, and the text props of rvav's building blocks). It
  also refuses a toast's literal text. It runs in the folders listed in
  `translated` in `web/eslint.config.js`, each added once converted.
  Names that read the same in every language (`rvav`, `vCenter`, `AWX`
  and the like) are allowed.
- **The unit tests run in English.** The vitest setup pins `lng: "en"`
  and fails on a missing English text. Playwright pins the browser to
  `en-GB`. `e2e/french.spec.ts` chooses French and goes through the
  shell, API tokens, the overview, the notifications and a request form.
  It runs axe in both themes with `lang="fr"`, and checks reflow at
  320 px with a French browser.

### What stays English
- **The server's messages.** Problem titles, field errors, policy
  reasons and a test message's result are written by the server in
  English, and the app shows them as they come. The web app's own
  framing around them is translated. The follow-up is for the API to
  read `Accept-Language`, or the person's language, and answer in it.
- **The mock backend**, whose messages stand in for the server's.
- **Identifiers and names:** request numbers, keys, CEL, YAML, product
  names, and what people typed (items, projects, comments).

## Consequences
- A French speaker reads the portal and what rvav sends in French, from
  one setting.
- Every new text is written twice, and the checks refuse a missing one.
  A pull request that adds a button adds its French too.
- A third language is a folder of JSON files and an entry in
  `locales`. Its plural rules come from `Intl.PluralRules`, and the
  resources test asks for its forms.
- Server messages in French come later, through the API.

## Alternatives considered
- **FormatJS (`react-intl`) with ICU messages.** ICU's plural and
  select syntax is richer, but i18next's namespaces, lazy backends and
  typed keys fit a large app converted folder by folder.
- **One file per language.** Simpler to load, but every agent and every
  pull request would edit the same two files. A namespace per area keeps
  changes apart, and one chunk per language keeps loading simple.
- **Extracting texts with a tool from the English source.** It finds
  literal strings, but not the sentence built from pieces, which is what
  makes a translation wrong. Conversion is by hand, and the lint rule
  keeps it.
