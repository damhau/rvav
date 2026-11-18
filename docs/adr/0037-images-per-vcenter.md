# ADR-0037: Images per vCenter

- Status: accepted
- Date: 2026-11-18
- Extends the templates of [ADR-0020](0020-administration.md), the
  several vCenters and content libraries of
  [ADR-0024](0024-multi-vm-blueprints.md) and the placement of
  [ADR-0036](0036-placement-targets.md). T1.4 (#137).

## Context
A catalogue template (Administration › Templates and networks) named one
vCenter template or content library item. With several vCenters, the same
operating system is a template of another name in each, built by each
site's pipeline; rvav could only clone in the vCenters that have a
template of that exact name. vRA's image mappings name a template per
region (cloud zone), with cloud-init and constraints of their own.

## Decision

### Sources per vCenter
- A catalogue template has one **source** per vCenter: a VM template's
  name, or an item of a content library (`template`, `content_library`).
  Table `vm_template_sources (template_key, vcenter, template,
  content_library)`, one row per vCenter.
- A source's vCenter is a name of `GET /admin/vcenters`. The vCenter
  integration's is stored as `''` and presented with its name: in the
  inventory, it is any vCenter that isn't one of those added, as
  `vcenters.Set` connects to it. A source naming a vCenter rvav doesn't
  manage, two sources on the same vCenter, and a template without a source
  are refused at save.
- Migration 00057 gives each template a source in each vCenter whose
  inventory has it (name and content library), else one on the vCenter
  integration's, then drops `vcenter_template` and `content_library`. It
  adds `user_data`, `constraints` and `linked_clone` to `vm_templates`
  (below).
- The API's `VmTemplate.sources` replaces `vcenter_template`,
  `content_library` and `in_inventory`; each source says whether its
  vCenter has it now. The template picker (`rvav.templates`) disables a
  template none of whose vCenters has it.

### Resolution
When a blueprint's `template` binding gives an offered template's key:
1. Placement chooses the vCenter and the cluster as before, among the
   clusters of the vCenters the template has a source in, and that has it
   (when the inventory knows the vCenter's templates). The vCenters left
   out are the preview's `placement.image.excluded`.
2. The VMs are cloned from that vCenter's source: the plan's `template`
   and `library` are the source's.
3. No vCenter left: `no_template`, "RHEL 9.4 has no template in vCenter
   vc02 now, where the clusters allowed here are." A bound cluster in a
   vCenter without a source: `no_template` on the cluster's input.

A blueprint that names its own vCenter template (no binding) keeps it and
is placed where vCenter has it, as before; an offered template with a
source of that name gives its customization specification.

### Its own cloud-init user data
- A Linux template may have **user data** of its own (`user_data`), a Go
  template over the same values as a blueprint's (`.vm`, `.inputs`,
  `.project`, `.environment`, `.request`, `.components`; inputs printed
  through `json`). Checked at save as a blueprint's, and refused for a
  Windows template, for a template with a customization specification, and
  when it doesn't start with `#cloud-config` or `#!`. It should read
  `.inputs` only when every item offering it has those inputs: a missing
  value fails the build.
- A VM cloned from a template with user data is configured by cloud-init
  through guestinfo, as a blueprint's `cloud_init` does, even when the
  item has none. An item that names a customization specification can't
  pick such a template: its preview says so.
- The plan carries the template's user data (`VMUnit.image_user_data`);
  both are rendered when the VM is customized, then **merged**:
  - Both `#cloud-config`: one document. Maps are merged key by key, lists
    are the template's entries then the item's (`packages`, `runcmd`,
    `write_files` add up), any other value is the item's (a scalar, or a
    key whose types differ).
  - Otherwise (a script on either side): a MIME multipart document, the
    template's part first. cloud-init handles both parts; scripts run in
    their order.
  - Only one side: that one, unchanged.

  A merge rvav does itself, rather than cloud-init's `merge_how`, so that
  the user data a VM got reads as one document and is the same whatever
  the image's cloud-init version.

### Its own constraint tags
- A template may have **constraint tags** of its own (`constraints`:
  `compute`, `storage`, `network`), written as a blueprint's
  ([ADR-0035](0035-constraint-tags.md)) but without `${...}`: they don't
  depend on the request.
- They are added to the component's (blueprint's and component's, after
  their inheritance) before placement, and the preview lists them with
  the others. A storage constraint applies to the VMs' datastore, not to
  added disks with constraints of their own.

## Consequences
- A template built per site under different names is one entry of the
  picker, and rvav places it where it exists.
- The plan carries the resolved source, so running requests, replays and
  day-2 actions don't read the catalogue again.
- `vcenter_template` and `content_library` are gone from the templates
  API: a client that wrote them writes `sources`.
- What an operating system needs everywhere (an agent, NTP, a tag on its
  clusters) is said once, on the template, instead of in each blueprint.
- Lists merged by concatenation can repeat an entry both sides give (a
  package, a `users` entry): cloud-init takes it twice, harmlessly for
  packages; a template's user data shouldn't create the users items do.
