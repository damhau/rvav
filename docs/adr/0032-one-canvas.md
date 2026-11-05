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

D1 ([implementation plan](../implementation-plan.md#d1-one-canvas-after-n1-built))
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
  be anything. The CEL it keeps (a step's `when`, a component's `count`,
  a disk's `size_gb`, an adapter's `network`, and the `network`,
  `groups`, `template` and `post_config` bindings) has its string
  literals masked (`policy.MaskLiterals`): each literal's text becomes
  `…` in its own quotes and comments go, so `inputs.token ==
  'tok_live_x'` reads `inputs.token == '…'` and a map picking a network
  by environment reads `{'…':'…'}[inputs.env]`, while what it reads and
  how (identifiers, operators, numbers, booleans) stay as written. An
  empty literal stays. A binding that is only a literal, or a list of
  them, stays whole: it is the network, template or post-configuration
  the item uses, which the canvas draws by name. One that doesn't parse
  is `…`. The literals are found by cel-go's parser and masked where
  they are written, so the rest is the author's text; the mocks'
  `topologyOf` does the same with a scanner. Its lists keep their
  positions, so a pointer means the same in both. AWX and workflow items
  have none.
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
  composites (they don't nest; a workflow item only while its workflow
  can start), with their form's inputs; the AWX templates imported from
  AWX, with their survey's questions and their ID in AWX; and the
  custom workflows registered for composite steps and enabled, with their
  input schema's properties. A workflow offered to some projects only (a
  workflow step's, or a workflow item's) says which (`project_ids`): the
  server refuses a request from another. Each says what it runs, whether it is planned with
  the request (a VM item: its inputs come from the form only, it has no
  condition or undo) and its outputs (a VM step's `name`, `fqdn`, `ip`,
  `moref`, `deployment_id` and `vms`; an AWX step's `job_id`; a custom
  workflow's whatever it returns). One endpoint rather than an item's form
  each, since the picker needs them all, and `/admin/workflows` is for
  administrators only. A template no one imported can still be named by
  hand; its step gives its job's ID.
- **An AWX template is named by its name and its ID.** A job template and
  a workflow template may share a name, so the picker tells them apart
  (`awx_workflow`). Picked from the list, a template is written as
  `awx.job_template` and `awx.template_id` (the server launches it by ID,
  as an item made from it: ADR-0016), and the step is found in the list
  by that ID, whatever the template is called since. Picking another
  template replaces both and drops the previous one's `title`; one named
  by hand has no ID. An undo is launched as a job template by name only
  (`undo.job_template`), so its picker offers job templates.
- **Edits are made on the YAML document, not on its data.** Each canvas
  edit is a function from the editor's text to a new text
  (`composite/edits.ts`), made with the `yaml` library's Document API
  (`parseDocument`, then the nodes' `set`, `delete` and list splices, then
  `toString`), not `parse` then `stringify`: comments, quoting, inline
  maps and key order survive, and only the nodes edited change. They
  survive the canvas's edits, not a save: the server stores the
  definition as JSON, and the editor reloads `toYaml(stored.definition)`,
  without comments, as an export has them. A text in
  the server's style (indent 2, no line width, the form `toYaml` writes)
  comes back byte for byte but for the edit; another indentation is made
  the server's on the first canvas edit. New keys go in a fixed order
  (`key`, `title`, what it runs, `when`, `with`, `undo`). Renaming a key
  renames `steps.<key>` in the composite's conditions and bindings. An
  edit changes its step and no other, whatever YAML anchors and aliases
  (`&common`, `*common`) the steps share: a part of the step that is an
  alias becomes a copy of what it named before it is edited, and an alias
  elsewhere whose anchor the edit changed, removed or moved after it
  becomes a copy of what it named (a rename changes every step, aliases
  with their anchor). A text that doesn't parse can't be edited on the
  canvas: the editor says why and the last graph stays.
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
  workflow not registered for composite steps, one not offered to every
  project the composite is offered to, from the targets' `project_ids`
  and the item's entitlements, a VM step with a condition, an undo or an
  input from a step), in its words, once the targets are loaded; and the server's last refusal of a save, kept with the text it
  refused and shown while the text is that one. All go on the node their
  pointer falls in (`addProblems`, `nodeForPointer`), a group's on its
  frame, and in the list under the graph.

### A request's run (D1.4)
- **Composites only.** A composite's request page draws its run on the
  graph of its steps, the timeline its list view. A VM item's request
  keeps the timeline alone. Its graph is drawn by network, not by time: a
  run makes the segments, then one step for all the security groups and
  their rules, then each component in turn, so the groups would all turn
  at once and the components one after the other, which says less than
  the timeline, where each component's step already holds its VMs' steps.
  A single-VM item's graph is one node. A composite's graph is the order
  its steps run in, so the run fits it.
- **The version requested, as its requester saw it.** The graph comes from
  `GET /requests/{id}/topology`: the topology of the version the request
  was made from, not the item's latest, projected as the form's
  (`blueprint.Topology`), for whoever may read the request. It is a call
  of its own, not a field of `Request`, since `request.updated` events
  replace the request and lists carry it, and a version's topology never
  changes: the page reads it once. Day-2 requests run an action and have
  none.
- **Steps to nodes.** A composite's steps run as `step:<key>`, the node's
  ID. The steps before them (submitted, approval, the checks of blocking
  subscriptions, and any other the server adds before the first) are the
  Request node's; those after (ready, a rollback) the Done node's, whose
  status is the ready step's. A node's status is its step's, from the event stream: not
  started, running, done, failed, skipped, or **undone**, which the step
  doesn't store: a step is undone when it was cancelled after its
  `/undo` step ran, and a VM step is undone when the request was rolled
  back (its VMs deleted). A node without a step yet is not started.
- **Live, without moving.** The graph is built once from the topology;
  events change only the statuses, which don't size the nodes, so the
  layout and the view stay where the reader put them. The selection
  doesn't follow the running step either.
- **A node selected** shows its part of the run: its steps as the
  timeline shows them, with their AWX jobs' tasks and errors, and the
  page's AWX output panel shows its job. A failed request opens with the
  node it failed at selected (the node holding the request's error step,
  however deep), its error and the request's own **Retry from the failed
  step** and **Roll back**, with their permissions; with another node
  selected they stay under the graph, once. The failure is an alert where
  it first appears only: moved by the selection or to the timeline, it is
  not read out again. Once rolled back (a `rollback`
  step done, by hand or by `rollback_on_failure`), it says what was
  deleted and offers only **Retry**, which then runs every step after the
  approval again, as the server does; Roll back would find nothing left.
  A retry resets the steps and deletes the rollback with no step event: a
  page open elsewhere reads the steps again when the request leaves
  failed (`lib/live/bridge.ts`).
- **The timeline is the list view.** It says all the graph does, in run
  order, and more (each step's time, detail and tasks); it replaces the
  canvas's own list there (`Canvas`'s `list` prop) and keeps its live
  region.

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
  A request's run has its **Timeline** there instead (D1.4).
- The selected node's details, untruncated, are under the canvas; in the
  composite designer they are the node's editor, every edit a labelled
  control, so the designer needs no mouse. **Add a step** opens on what
  the step runs; an added step's editor takes the focus, a removed step's
  goes to the step before it, and a moved step's stays on its button.
  When what had the focus goes (an undo removing the step whose editor
  had it), it goes to the selected node or the graph's Tab stop, so
  Ctrl+Z and Ctrl+Y go on working; a variable added to a step or its
  undo focuses its row. Nodes are announced as buttons (React Flow's
  `aria-roledescription` "node" is dropped).
- D1.5 checks the pages that use the canvas with the keyboard only (a
  step added, bound, published, requested and followed, and a failed run
  retried: `web/e2e/one-canvas.spec.ts`) and with axe in both themes (the
  three-tier item's request form, a composite draft with a step
  selected, a run failed and running: `accessibility.spec.ts`).

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
  tests, and, for requesters to see it, to `blueprint.Topology` and the
  mocks' `topologyOf`, which keep only what they list. Both are checked
  against one fixture, `internal/seed/testdata/topology.json` (the seeded
  VM and composite items' blueprints, and probes of what a requester must
  not read, with their topology): `TestTopologyFixture` keeps it what
  `blueprint.Topology` gives (rewritten with `-update`, the change read in
  review), and `web/src/mocks/topology.test.ts` checks `topologyOf`
  against it.
