# ADR-0040: Files attached to requests

- Status: accepted
- Date: 2026-11-18
- Extends the forms of [ADR-0019](0019-catalog-administration.md). F1.2
  (#148).

## Context
vRA forms have a file upload field: a requester attaches a firewall rule
export, a certificate or a design document, and the workflow reads it. rvav
forms sent JSON only. A file brings what JSON values don't: bytes rvav
can't check like a value, a size that a JSON body limit doesn't fit, a
content type a browser might act on, and someone who must be allowed to
read it after the request.

## Decision

### The field
- A `file` widget takes one file. Its input is an object in the form's
  schema (`type: object`). The field names the types it takes, `accept`
  (extensions such as `.csv`, MIME types such as `application/pdf`, or a
  family such as `image/*`), and its largest file, `max_size_mb`, from 1 to
  25 MB, 10 by default.
- rvav takes the types of a fixed list: PDF, PNG, JPEG, GIF, WebP, plain
  text and logs, CSV, Markdown, JSON, YAML, XML, shell and PowerShell
  scripts, PEM, CRT and CER certificates, ZIP, gzip, and Office Open XML
  documents. HTML and SVG are not among them: a browser runs what they
  hold. An `accept` entry that names nothing on the list is refused on
  save, and a field without `accept` can't be published.
- An extension stands for its MIME type, so `.jpg` takes `.jpeg` files
  too.
- A file field can't be edited by approvers, have an initial value, or be
  secret: approvers and initial values would name a file the requester
  didn't attach, and a file is kept as it was sent, not sealed.

### Upload
- `POST /forms/files?version_id&project_id&field&name` takes the file's
  bytes as the body (`application/octet-stream`), when it's chosen, not
  with the request. JSON bodies stay small, and the requester learns at
  once that a file isn't taken.
- The caller must be able to request the item in the project, as for a
  request. A catalog administrator may attach to any version, so that the
  designer's preview works.
- rvav reads no more than the field's largest file plus one byte, and the
  endpoint never reads more than 25 MB (`http.MaxBytesReader`). A
  `Content-Length` over the limit is refused before any byte is read. Too
  large gives 413.
- The file's name is its own, without folders, at most 255 bytes, with no
  control characters. Its extension must be one the field takes. Its
  first 512 bytes are sniffed (`http.DetectContentType`), and the result
  must be what a file of that extension holds: a `.csv` that is a PDF, or
  a `.txt` that is HTML, is refused. Either refusal gives 415. The type the
  client sends is ignored.
- The file is kept in Postgres (`request_files`, `bytea`) with its owner,
  name, size, canonical MIME type and SHA-256, and waits for its request.
  A file that waits more than a day goes, and no one has more than 20
  waiting. The answer is the file's record: `{id, name, size,
  content_type, sha256}`.

### Request
- The form's value is the record, or only its `id`. At evaluation, rvav
  replaces it with the record it keeps, so a client can't change a name,
  size or checksum. A file that isn't the requester's, or no longer
  waits, is refused (`gone`); one the field no longer takes, after the
  item changed, is refused too (`not_allowed`).
- Submitting gives the request its files, in the same transaction as the
  request. A request that Temporal didn't start is removed, and its files
  wait again, so a retry finds them.
- An approver's edit and a composite's sub-request evaluate the request
  again with its files.
- AWX jobs, workflows and events get the record, never the bytes. A
  playbook that needs the content downloads it from rvav with a token
  allowed to.

### Download
- `GET /files/{id}` sends the file to its requester or owner, the
  request's approvers, and its project's administrators
  (`request.manage`). Others who see the request get 403, and those who
  don't get 404. A waiting file is only its owner's.
- It's always a download: `Content-Type: application/octet-stream`,
  `Content-Disposition: attachment` with the file's name,
  `X-Content-Type-Options: nosniff`, a `Content-Security-Policy` of
  `default-src 'none'; sandbox`, and `Cache-Control: private, no-store`.
  The web app fetches it with the user's token and saves it as a file. It
  never shows it in the page.

### The web app
- The field is a button that opens the browser's file chooser, which also
  takes a file dropped on it. It checks the size before sending, shows
  the file's name and size once kept, and says why rvav refused a file.
- While a file is being sent, the form waits: Submit, Save as draft and
  a wizard's Next stay off and say which file is still sending (F1.6), so
  no request leaves without it.
- The request's page lists a file as a button that downloads it.
- nginx takes 25 MB on `/api/v1/forms/files`, and 10 MB elsewhere.

### Pictures for image fields
- An image field shows a picture at an address, or one rvav keeps, named
  `upload:<id>`. A catalog administrator (`catalog.manage`) uploads it to
  `POST /admin/catalog/images` as an octet-stream body: PNG, JPEG, GIF or
  WebP, 2 MB at most, whose content is sniffed as for a file field. SVG is
  refused, as it can hold scripts.
- The picture is kept in `form_images`, apart from requests: it belongs to
  the catalog, and versions name it. Saving a version naming a picture
  rvav doesn't keep is refused (`unknown`). A picture is never removed,
  as published versions may show it.
- `GET /forms/images/{id}` sends it to anyone signed in, with its checked
  type, `nosniff`, a sandboxing Content-Security-Policy, and
  `Cache-Control: private`. The web app fetches it with the user's token
  and shows it from a `blob:` URL, which the web server's policy allows.

## Consequences
- The database grows with what requesters attach: 25 MB at most for each
  file field of a request. Backups hold the files. They are kept with
  their request.
- Files aren't encrypted at rest beyond what the database's storage does.
  Don't use file fields for secrets.
- Sniffing catches a file that isn't what its name says. It doesn't
  check what a valid file holds: a PDF or an Office document can carry
  what its reader runs. rvav never opens a file, and sends it only as a
  download.
- Service accounts that read files need a role that allows it in the
  project, such as project administrator. An auditor sees the record but
  can't download the file.
- No virus scanning: an installation that needs it scans in the workflow
  that reads the file.
- A picture for image fields belongs to the rvav that keeps it: YAML and
  Git carry its `upload:<id>`, not the picture, so another installation
  refuses the version until the picture is uploaded there. Pictures are
  never removed, so the table grows with what catalog administrators
  upload, 2 MB at a time.
