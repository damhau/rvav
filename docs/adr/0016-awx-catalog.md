# ADR-0016: AWX templates become catalog items

- Status: accepted
- Date: 2026-10-27

## Context
S6 ([implementation plan](../implementation-plan.md#s6-awx-integration))
asks that AWX templates be requestable from the catalog with their surveys
as forms, chosen by organisation or label, kept in step with AWX, and that a
survey changed in AWX be flagged for the catalog admins. Slice 1 seeded AWX
items by hand: a form, and the template's name in the blueprint.

## Decision

### Which templates
The sync service reads, every `RVAV_AWX_SYNC_INTERVAL` (5 minutes), the job
templates and workflow job templates of `RVAV_AWX_ORGANIZATION` that carry
one of `RVAV_AWX_CATALOG_LABELS` (`catalog` by default). AWX can't filter
workflow templates on labels, so rvav filters those itself. Admins can also
sync at once (`POST /api/v1/admin/awx/templates/sync`). Putting the label on
a template is the AWX admin's way of publishing it.

### A new template becomes an item
- If an AWX item already launches a template of that name and kind (the
  seeded ones, or one made by hand), it is linked and its form is kept.
- Otherwise rvav makes an item: name from the template's, its description
  as the summary, category from a `category:<key>` label (else
  Automation), published at once as version "AWX #1", open to every
  project. Approval policies apply as to any item.
- The form comes from the survey: text and textarea (length limits),
  integer and float (bounds), multiple choice (segmented up to four
  choices, else a select), multiselect (checklist), required questions and
  defaults. Password questions are left out, since rvav would store the
  answer with the request; AWX uses the survey's default. Variables that
  aren't identifiers, or are `rvav`, are left out too. Each left-out
  question is a warning.
- Requests run as AWX items always have: the answers as `extra_vars`, with
  rvav's context under `rvav`.

### Keeping in step
Each sync records the template's survey and launch settings. The survey the
item's form was made from is kept, so:
- a changed survey is flagged with what changed, a line per change
  ("listen_port: maximum 65535 → 32767"); the item's form stays as it was
  until a catalog admin changes it (version editing comes with S8);
- launch settings that break rvav's launch are warnings: variables not
  prompted on launch (AWX would ignore rvav's context), limit not prompted
  (the template can't run on one VM);
- a template no longer chosen (deleted, label removed) is flagged as no
  longer in AWX; its item stays, so requests and history keep their
  meaning.

`GET /api/v1/admin/awx/templates` lists all this for catalog admins and
administrators, and the admin Catalog items page shows it.

## Consequences
- Publishing to the catalog is an AWX-side decision (the label), and every
  project may request what is published; entitlements per project come
  with the catalog administration screens (S8).
- A form made from a survey can be extended by a catalog admin later; the
  survey diff then tells them what to carry over.
