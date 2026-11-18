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
  integration's, then drops `vcenter_template` and `content_library`.
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

## Consequences
- A template built per site under different names is one entry of the
  picker, and rvav places it where it exists.
- The plan carries the resolved source, so running requests, replays and
  day-2 actions don't read the catalogue again.
- `vcenter_template` and `content_library` are gone from the templates
  API: a client that wrote them writes `sources`.
