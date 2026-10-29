# Accessibility

rvav's web app aims at WCAG 2.2 AA ([ux.md](ux.md)). This page records the
1.0 audit (S14.8): what is checked on every change, what the audit found
and fixed, and what still needs a person.

## Checked on every change

`web/e2e/accessibility.spec.ts` runs with the other Playwright tests in
`pnpm e2e`, so in `make check` and CI. It runs on the mock API.

- **axe** (WCAG 2.0, 2.1 and 2.2, levels A and AA):
  - on all 20 pages and in each of their tabs;
  - on a detail page reached from each list (requests, deployments,
    catalog items, projects, subscriptions);
  - on an approval opened in the inbox;
  - in the light and the dark themes.
- **Structure, as a screen reader navigates it:** on every page,
  - one banner, a navigation and one main region;
  - one `h1` first;
  - headings that don't skip a level.
- **Reflow:** at 320 CSS pixels, a 1280-pixel window zoomed to 400%
  (WCAG 1.4.10), no page scrolls sideways. Wide tables scroll inside
  themselves.
- **Keyboard only:**
  - the skip link comes first;
  - Tab reaches the catalog's RHEL 9 server, then every field of its form
    (the role typed in, the size cards, the cluster, datastore and network
    pickers, the post-configuration jobs, the lease, the justification);
  - the focused element is always visible;
  - Enter on "Submit request" sends it and opens the request's page.
- **Dialogs:** opened from the keyboard, a dialog takes the focus and keeps
  it while tabbing. Escape closes it and gives the focus back to its button.

## Found and fixed (S14.8)

| Finding | WCAG | Fix |
|---|---|---|
| The account button was named "Account", without the name and role it shows (on every page). | 2.5.3 Label in name | Named by what it shows, with "Account:" read first. |
| The `faint` text colour fell to 4.2:1 on the light theme's sunken and selected backgrounds, and 4.3:1 on the dark theme's selected rows. | 1.4.3 Contrast | `#5f6a74` (at least 4.6:1) in light, `#94a0a9` (at least 5:1) in dark. |
| The deployment list's environment and lease filters were links marked `aria-pressed`, which links can't have. | 4.1.2 Name, role, value | `aria-current`, as the status filters next to them. |
| The approvals inbox's tabs pointed (`aria-controls`) at a panel that didn't exist. | 4.1.2 | The list and the open approval are the tab's panel. |
| The "Change" buttons of network mappings were named "Map … by hand", without their text. | 2.5.3 | "Change: map … by hand". |
| The YAML editor's syntax colours (CodeMirror's defaults, made for white) failed contrast in both themes, and the editor couldn't be focused when read-only, to scroll it from the keyboard. | 1.4.3, 2.1.1 | Highlighted with the app's colours; the read-only editor takes the focus. |
| At 400% zoom, nine pages scrolled sideways. The "Actions" column headers' screen-reader text escaped its table's scroll container, and eleven admin tables had no scroll container at all. | 1.4.10 Reflow | Scroll containers are positioned, so what they hold stays in them; the eleven tables scroll in their own container. |

Already in place before the audit:
- a skip-to-content link;
- live regions for a request's progress (its timeline) and its job output
  (a log);
- reduced motion respected;
- status shown with an icon and text, never colour alone;
- menus and dialogs (Radix) with focus management.

## What still needs a person

Automated checks find only part of the problems. Before 1.0 a person
should do these, ideally someone who uses them every day:

- **Screen readers:** NVDA and JAWS with Chrome or Edge on Windows,
  VoiceOver with Safari. Request a VM, follow it to Ready, approve one,
  and run a day-2 action. The automated checks cover names, roles,
  landmarks and headings. They can't judge whether what is read makes
  sense, or whether live updates are announced at the right moments.
- **Speech input** (Dragon, Voice Control): every control can be said by
  its visible name. 2.5.3 is checked by axe, but not in use.
- **Windows high-contrast (forced colours):** focus rings, the selected
  state of cards and filters, and status icons stay visible.
- **Zoom and text spacing in a real browser** at 200% and 400%, and with
  the WCAG 1.4.12 text spacing applied.

Storybook was an open item since slice 1. It isn't needed for this: the
checks run on the real pages, with their data, in both themes.
