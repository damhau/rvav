# ADR-0032: One canvas for an item's topology, the composite designer and a request's run

## Status
Accepted (2026-11-05). Revises [ADR-0007](0007-extensibility-model.md)'s
"There is no visual workflow designer": composite items get one, on a
canvas whose source of truth stays the YAML.

## Context
ADR-0007 chose no visual designer: simple chains are composite items
written in YAML, everything else a playbook or code with review and tests.
The fear was vRO's: a designer that grows into a programming language
nobody reviews.

Composites have grown since ([ADR-0022](0022-custom-workflows-and-composites.md)):
parallel groups, `when` conditions, CEL bindings to earlier steps' outputs,
undo steps. Written in YAML, the author keeps in their head which steps
run together and which outputs exist at each step; `CompositeOutline`
lists the steps under the YAML, but not what runs beside what. A multi-VM
item ([ADR-0024](0024-multi-vm-blueprints.md),
[ADR-0028](0028-nsx-networking.md)) is a topology (components, their
adapters on networks and segments, security groups and the rules between
them) that a list doesn't show either. A request's run is a timeline, in
which a parallel group is a run of lines. And people coming from vRA
expect its design canvas.

D1 ([implementation plan](../implementation-plan.md#d1-one-canvas-after-n1))
asks for one canvas used three ways: an item's topology (D1.2), the
composite designer (D1.3) and a request's run (D1.4).

## Decision

### One canvas, drawn from the blueprint
- `web/src/components/canvas` holds a model, `CanvasGraph` (nodes, edges,
  parallel groups, problems), built from a blueprint by `blueprintGraph`,
  and `Canvas`, which draws it. The three uses are the same component with
  different props: read only, with a node's editor beside it, or with each
  node's run status.
- **The YAML stays the source of truth.** The graph is derived from the
  blueprint each time it changes. Nothing about the graph is stored: no
  positions, no layout, no IDs beyond the blueprint's own keys. The
  designer's edits (D1.3) are functions from a blueprint to a blueprint,
  applied to the same draft the Definition tab edits, so the YAML and the
  canvas are two views of one document. Git
  ([ADR-0029](0029-catalog-in-git.md)), the API and reviews see only YAML.
- **The server's checks stay the authority.** The canvas shows the
  structural checks the server makes (`lib/composite.ts`, shared with the
  mock) on the node they are about, and the server's problems at their
  JSON pointer on the node that pointer falls in (`nodeForPointer`).
- **Requesters see a topology, not the blueprint.** The request form
  draws from its form's `topology` (D1.2): the part of the blueprint the
  canvas draws (`blueprint.Topology`), without guest settings, sizes,
  naming, tags, placement or AWX variables, and of a composite step's
  bindings only the `steps.<key>` they use, since a binding's literal may
  be anything. Its lists keep their positions, so a pointer means the
  same in both. AWX and workflow items have none.
- **No scripting language.** Conditions and bindings stay CEL, as in
  ADR-0022; there is no code node. Work that needs a language is an AWX
  playbook or a custom workflow: ADR-0007's levels 2 and 3 are unchanged,
  and D2's new step kinds keep to this.

### Borrowed ideas, no code
- **Windmill:** typed step inputs, each bound to the form's values or an
  earlier step's outputs through a picker that knows them (D1.3).
- **Kestra:** the code and the graph side by side, each editing the same
  document (D1.3).
- **n8n:** a run drawn on the graph that ran, a node opening its output
  (D1.4).

Only the ideas: no code, styles or assets from them (their licences
differ, and none fits rvav's design system).

### React Flow, laid out by dagre
- **React Flow** (`@xyflow/react`, MIT) draws: panning, zooming, edges,
  and nodes that are our own React components in the app's tokens. Only
  its base stylesheet is imported; its CSS variables point at our tokens,
  so light and dark follow the app's theme. Its attribution stays (the
  library asks to hide it only with a Pro subscription), in the faint text
  colour.
- **dagre** (`@dagrejs/dagre`, MIT) lays out: a layered layout that is
  small, synchronous and deterministic, the same in a unit test as in the
  browser. A node's size is fixed by what it shows, so nothing is
  measured.
- **Not ELK.** elkjs (EPL-2.0, about 8 MB unpacked, asynchronous and
  usually run in a web worker) is for compound graphs, ports and
  orthogonal routing. rvav's graphs don't need them: parallel groups don't
  nest and composites don't nest (ADR-0022), so a parallel group is laid
  out as one node the size of its steps and its steps placed inside it.
  If composites nest, or D2's branches need ports, ELK can replace dagre
  inside `layoutGraph` without changing the model or the component.
- The layout is automatic: nodes can't be moved by hand, since positions
  would be state outside the YAML.

### How a blueprint is drawn
- **A composite, top to bottom:** the request's inputs, then the steps in
  order, then Done. A parallel group is a dashed frame, "At the same time",
  with its steps side by side; edges join the frame, not each step. A
  `when` dashes the edge into its step, labelled "when", and the condition
  shows on the step. What a step runs, the earlier steps its bindings use
  and its undo are lines on the step. Edges are order, not data: one edge
  per binding would tangle the graph, so bindings are listed on the step
  and edited in its picker. Done says whether a failure undoes what was
  built.
- **A VM item, left to right, in columns:** networks (segments, networks
  named in the item, the request's network, or placement's choice), then
  the components (one VM for an item without components), then the
  security groups and existing NSX groups they are in. Adapters are edges
  labelled with their key (and DHCP); membership edges are dashed; rules
  are arrows labelled with their services, red when they drop or reject. A
  rule between two groups the VMs are in is drawn as a bracket on their
  right, so the groups stay in one column; a rule's other sources (any, an
  NSX group only allowed in) sit before the groups.
- **Problems:** a node with problems has a warning border and says how
  many; the list view and the node's details say what they are.
- **A run:** each node's status as an icon and its word, the border in the
  status's colour. The layout doesn't depend on the status, so nothing
  moves while a request runs.

### The composite designer (D1.3)
- **Where:** a composite draft's Definition tab: the canvas beside the
  YAML editor, as for any item, with **Add a step**, **Undo** and **Redo**
  above it and the selected node's editor under it. A step's editor holds
  every edit of it: move it earlier or later, run it with the step before,
  remove it, its key, title, what it runs, its condition, its inputs'
  bindings and its undo. The Request node lists the form's inputs; Done
  says whether a failure undoes what was built. Published versions keep
  the read-only graph.
- **What a step can run comes from the server**, in one call:
  `GET /admin/catalog/composite-steps` (catalog admins and administrators)
  lists what ADR-0022 allows a step to name. Published items other than
  composites (they don't nest), with their form's inputs; the AWX
  templates imported from AWX, with their survey's questions; and the
  custom workflows registered for composite steps, with their input
  schema's properties. Each says what it runs, whether it is planned with
  the request (a VM item: its inputs come from the form only, it has no
  condition or undo) and its outputs (a VM step's `name`, `fqdn`, `ip`,
  `moref`, `deployment_id` and `vms`; an AWX step's `job_id`; a custom
  workflow's whatever it returns). One endpoint rather than an item's form
  each, since the picker needs them all, and `/admin/workflows` is for
  administrators only. A template no one imported can still be named by
  hand; its step gives its job's ID.
- **Edits are made on the YAML document, not on its data.** Each canvas
  edit is a function from the editor's text to a new text
  (`composite/edits.ts`), made with the `yaml` library's Document API
  (`parseDocument`, then the nodes' `set`, `delete` and list splices, then
  `toString`), not `parse` then `stringify`: comments, quoting, inline
  maps and key order survive, and only the nodes edited change. A text in
  the server's style (indent 2, no line width, the form `toYaml` writes)
  comes back byte for byte but for the edit; another indentation is made
  the server's on the first canvas edit. New keys go in a fixed order
  (`key`, `title`, what it runs, `when`, `with`, `undo`). Renaming a key
  renames `steps.<key>` in the composite's conditions and bindings. A
  text that doesn't parse can't be edited on the canvas: the editor says
  why and the last graph stays.
- **Undo and redo are the editor's.** A canvas edit is written into the
  editor as one transaction of the smallest change, isolated in its
  history (`isolateHistory`), so CodeMirror's history holds typed and
  designed edits in the order they were made. **Undo** and **Redo** above
  the canvas, Ctrl+Z, and Ctrl+Shift+Z or Ctrl+Y outside the text fields,
  go back through both; there is no second history to keep in step. A
  text from outside (loading, a save that changes it, **Undo changes**)
  starts the history again.
- **Parallel groups:** a step is added after the selected one, at the
  same time as it, or at the end. A step added beside one on its own makes
  a group of the two; a group left with one step becomes that step again.
  Moving a step on its own swaps it with the slot beside it; moving one in
  a group takes it out, before or after the group; **Run with the step
  before** joins the previous slot (or its group).
- **Bindings:** a step's inputs are its target's (an item's form, a
  template's survey, a workflow's schema), in their order; an AWX or
  workflow step may bind more variables. Each is bound through a picker
  that offers what exists at that point: the form's inputs, the request's
  `environment` and `project`, and the outputs of the steps in the slots
  before it, not of those beside it in its group nor after it (none for a
  VM step, planned from the form), grouped by where they come from; or
  **A value or CEL expression**, where a number or a boolean is written as
  one and anything else is CEL. A custom workflow's outputs are open, so
  the picker says to write them as CEL. A required input left unbound is
  said on the input, not as a problem: AWX takes extra variables, and the
  server doesn't refuse it.
- **Problems on their step:** the structural checks (`lib/composite.ts`),
  what the server would refuse when the item is requested
  (`core.evaluateComposite`: an item that isn't published, a custom
  workflow not registered for composite steps, a VM step with a condition,
  an undo or an input from a step), in its words, once the targets are
  loaded; and the server's last refusal of a save, kept with the text it
  refused and shown while the text is that one. All go on the node their
  pointer falls in (`addProblems`, `nodeForPointer`), a group's on its
  frame, and in the list under the graph.

### Accessibility
- The graph is one Tab stop, on the node last visited (roving tabindex).
  The arrow keys move to the nearest node that way (the next layer, or
  the next node in one), Home and End to the first and last; Enter or
  Space selects a node, Escape clears the selection. React Flow's own
  selection, deletion and connection keys are off, and the mouse wheel
  scrolls the page rather than zooming; zoom has its own buttons.
- Each node is a button whose accessible name says all it shows, how it
  relates to the others ("Runs after", "At the same time as", "Adapter
  nic1 on Application", "Allows HTTPS from Any") and its status. Edges are
  named too.
- A **List** view shows the same model in reading order: a composite's
  steps as an ordered list, a parallel group as a list within it; a VM
  item's nodes with their relations. Selection is shared with the graph.
- The selected node's details, untruncated, are under the canvas; in the
  composite designer they are the node's editor, every edit a labelled
  control, so the designer needs no mouse. **Add a step** opens on what
  the step runs; an added step's editor takes the focus, a removed step's
  goes to the step before it, and a moved step's stays on its button.
- D1.5 checks the pages that use the canvas with the keyboard only and
  with axe in both themes.

## Consequences
- Composite authors and readers of a multi-VM item get a picture, and the
  YAML is still what is reviewed, diffed and kept in Git: nothing to
  migrate, and the YAML tab keeps working without the canvas.
- ADR-0007's consequence that vRO's GUI users must move to YAML is
  softened: they get a designer, though not one that runs code.
- Two dependencies (React Flow with its d3 parts, and dagre), loaded only
  with the routes that use the canvas.
- The designer knows what the server refuses at request time only as far
  as `scope.ts` repeats `evaluateComposite`'s checks; a new check there
  should be repeated, or it shows only once a request is refused.
- Large VM items can be dense on an automatic layout. The list view and
  zoom are the fallback; ELK is the next step if layouts need it.
- The model is the contract between the three uses: a new blueprint field
  that should show on the canvas is a change to `blueprintGraph` and its
  tests.
