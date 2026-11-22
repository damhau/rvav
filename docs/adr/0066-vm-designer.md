# ADR-0066: The VM designer

- Status: accepted
- Date: 2026-11-22
- Builds on the canvas and the composite designer of
  [ADR-0032](0032-one-canvas.md), the multi-VM blueprints of
  [ADR-0024](0024-multi-vm-blueprints.md), the NSX networking of
  [ADR-0028](0028-nsx-networking.md) and the custom resources of
  [ADR-0056](0056-custom-resource-types.md). I1.4 (#177) of the parity
  programme, row C16 of the parity table.

## Context
vRA's Assembler draws a cloud template on a design canvas: machines,
networks, disks, NSX objects and custom resources dragged from a palette,
connected by dragging from one to another, edited in a properties panel,
with the YAML beside it written as the canvas changes and the canvas
redrawn as the YAML is typed.

rvav drew a VM item's topology from its YAML (ADR-0032, D1.2) but only
showed it: components, adapters, segments, groups and rules were written
by hand. The composite designer (D1.3) edits a composite's steps on the
same canvas, with edits made on the YAML document and the editor's
history as the only history.

## Decision
### Where
A VM item's draft opens the designer on its Definition tab: the palette
over the canvas, **Undo** and **Redo** above it, and the selected node's
details under it. A published version keeps the read-only graph.

### The same canvas and the same rules as the composite designer
- **The YAML stays the source of truth, and no positions are stored.** The
  canvas is drawn from the editor's text each time it changes (dagre, as
  for every graph), so a typed change and a canvas edit redraw the same
  way, and a change that leaves the graph's shape alone (a title, a size)
  leaves every node where it was.
- **Each edit is a function from the editor's text to a new text**
  (`vm/edits.ts`), on the `yaml` library's Document, as the composite's
  are (`composite/edits.ts`). Both use one module for the Document helpers
  (`yamlDoc.ts`: loading, saving in the server's style, the copy an alias
  becomes before it is edited), and the designer's state, its history
  buttons, its heading and its focus handling are shared in
  `components/canvas/designer.tsx`.
- **Undo and Redo are the editor's** (CodeMirror's history, each canvas
  edit isolated in it): typed and designed edits are undone in the order
  they were made.
- **A refusal writes nothing.** An edit the definition can't hold throws
  `EditError` with its reason before the text is written: a key taken or
  malformed, a 21st VM, a fifth segment, a ninth group, a disk without a
  size, a network named by nothing, a loop of `depends_on`. The dialog, the
  field or the connection says it.

### What the canvas names
A node's ID is what the edits act on: `component:<key>` (`component:vm` for
an item without components, its one VM being the blueprint's own settings),
`network:<expression>`, `network:auto` (placement's), `segment:<key>`,
`sg:<key>`, `nsx:<id>`, `groups:<expression>` (the groups the request
chooses), `resource:<key>`, `disk:<component>:<index>`, `disk@<index>` for
the blueprint's disks that components without their own take, and `any`. In
the designer each disk is a node of its own (`blueprintGraph(…, {designed:
true})`), attached to its VMs; elsewhere disks stay lines on their VM.

### Palette, connections and removal
- **The palette** adds a VM, a network, a segment, a security group, an NSX
  group, a disk or a custom resource. An entry dragged onto the canvas
  goes on the VM it is dropped on; chosen with the keyboard, it goes on the
  selected VM, or the one picked in its dialog. The dialog asks for what
  the part can't do without (a key, a network's CEL, a disk's size, a
  resource's type from the resource types). A second VM on an item with
  one turns the item's VM into the first entry of `components`, under the
  key given for it; the blueprint's settings stay where they were, the
  defaults of both.
- **A connection** is drawn from a node's dot to another, or chosen in
  **Connect to** in a node's details, which says what it will write or why
  it can't before anything is written. A VM to a network or a segment gets
  an adapter there (its first one's network is `bindings.network`, others
  are `nics`); to a group, its VMs are in it (`groups`); to a disk, the disk
  is moved to it, or copied when it was the blueprint's; one VM to another
  builds the second after the first (`depends_on`). Anything else is
  refused with what to do instead: a firewall rule needs its services, so
  it is written in the YAML; a custom resource follows what its inputs
  read.
- **Removing a node** removes what names it: a network's adapters, a
  group's memberships and rules, a component's place in the others'
  `depends_on`. The last VM can't be removed. **Disconnect** removes one
  edge's field, when one field is the edge (a `depends_on` read from tags
  or cloud-init isn't).
- **A component without its own disks, adapters or groups takes the
  blueprint's** (as the server's `blueprint.ComponentList` does). An edit
  to one component's first gives it its own copy, so the others keep
  theirs.

### Details and the server's problems
The selected node's details edit its fields: a component's key, title,
count (a number or CEL), template, naming and anti-affinity; the single VM's
template, naming and OS family; a network's or an NSX group's expression;
a segment's or a group's key and title; a disk's key, size, storage policy
and datastore; a resource's key, type and title. A key renamed is renamed
where it is read (`depends_on`, adapters, groups, rules, `components.<key>`
in CEL). Fields the details don't hold (guest settings, rules' services, a
resource's inputs) are written in the YAML. The server's refusal of a save,
kept while the text is the one it refused, goes on the node its pointer
falls in, beside the canvas's own problems.

### YAML the canvas can't edit
A text that doesn't parse, a blueprint that isn't a VM item's, or a part the
canvas names by key that it can't tell apart (a list that isn't one, an
entry without a key, two with the same key) leaves the canvas read-only
(`edits.designable`): it still draws what it can, the palette gives way to
the reason, and the YAML edits it until the canvas can again.

### What is lost
Comments, quoting, flow maps and key order survive the canvas's edits, but:
- a node removed takes its comments with it, and a part copied (a shared
  disk, an alias edited) has the comments of what it copies;
- a text in another indentation than the server's is re-indented on the
  first canvas edit;
- new keys go in the server's order (`blueprint.Blueprint`, `Component`,
  `Disk`, `NIC`), not where a reader might put them;
- a save loses every comment, as for composites: the server stores the
  definition as JSON and the editor reloads it from that.

### Accessibility
Everything the mouse does has a keyboard way: the palette's entries are
buttons, the canvas is one Tab stop with the arrow keys between nodes, and
**Connect to**, **Disconnect** and **Remove** are in the details. The focus
goes to what an edit made (the new node's heading, the next connection),
and each node's name says what it is, with its badge ("2 VMs", "Disk").
axe runs on the designer in both themes, in English and French.

## Consequences
- A VM item can be built without writing YAML for its topology: VMs,
  networks, segments, groups, disks, resources and their connections.
- Firewall rules, guest settings and a resource's inputs are still written
  in the YAML; the canvas draws them and refuses to draw a rule's
  connection, saying where to write it.
- The server's validation is unchanged: the designer refuses what the
  server would (keys, counts, limits, loops) before writing, and shows the
  server's refusal of a save on its node.
- The mock backend plans one VM for a new VM item whatever its components,
  so a request in mock mode reaches Ready without building each component.
