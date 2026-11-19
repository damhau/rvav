# Using rvav

rvav is where you get VMs and the services built on them, and look after
them afterwards. This guide is for people who request and own VMs, and for
approvers. Administrators have [their own guide](admin.md).

## Signing in and projects

You sign in with your organisation's account. What you see depends on your
**project roles**, which come from your groups:

| Role | What you can do in the project |
|---|---|
| Viewer | See its requests and deployments. |
| Member | Request, and act on your own VMs. |
| Project admin | Act on every VM of the project, hand them over, delete them; choose who sees its deployments, and write its policies. |

Your organisation can also give you roles of its own, which allow other
things. The project you work in is chosen at the top of the page. Your
roles in it show under your name.

To find out why you can't do something, open **What I may do** in your
menu: for a project, or for all of rvav, it lists each permission, held or
not, with the roles that grant it and where each comes from (given to you,
through a group, or set by rvav). A permission no role of yours grants is
one to ask your project admin or administrator for.

## Requesting a VM

1. **Catalog:** search by name or summary, or choose a category or kind in
   the toolbar. Each card shows what kind of item it is (a VM blueprint, a
   composite, an AWX job template) and a line on what it gives you. Star
   the ones you use often. **Request** on a card opens its form directly.
   The catalog shows the items shared with you in the current project: an
   item can be shared with all the project's members, or only with chosen
   people, groups or roles. If an item you used before is missing, ask a
   catalog admin; a draft you saved for it can't be submitted.
2. **The item's page:** open an item to read its description, the version
   that requests use (and the earlier ones still offered, when a catalog
   admin keeps several), whether it needs approval, how long it usually takes,
   and what it builds: its VMs, networks and groups, or a composite's steps
   in the order they run. **Request** opens the form. If your roles in the
   project don't let you request, the page says so.
3. **The form:** environment first (dev, tst, prd), then what the item
   asks. When the item offers more than one version, **Version** comes
   before the environment, set to the newest. To request an earlier one,
   pick it there: the form changes to that version's and keeps what you
   typed in the fields it has too. The request and its deployments keep
   the version you picked. If an administrator retires that version
   before you submit, the form moves to a version still offered, keeps
   what you typed, and says why under **Version**. The panel beside the form shows what you will
   get, as you fill it in:
   - the VMs' names;
   - the size and the quota it uses: for each resource, the quota with
     the least left, such as the project's or yours in the project;
   - **Limits after this request:** how many deployments you, or the
     project, would have, and how large the largest deployment would be,
     against the deployment limits that apply;
   - whether someone must approve it, and who;
   - the lease, and the placement.

   For an item that builds more than one VM, or runs several steps, the
   **Topology** under the fields draws what it builds: its servers and
   how many, their networks and NSX segments, the security groups and the
   rules between them (a composite: its steps, in the order they run,
   those that run at the same time side by side). The counts and names
   follow what you fill in. **List** gives the same as a list; select a
   node to read all it says. A step that calls a REST API shows its method
   and the values it reads, not the address.
4. **Deployment** (for an item that makes deployments): optionally, a
   name and a description of what it is for.
   - Left empty, rvav names a one-VM deployment after its VM. A
     deployment of several VMs takes the name its item sets, or the item's
     name and the request number. When another deployment of the project
     has that name, rvav adds the request number.
   - A name you give is unique in the project, whatever the case: the
     form refuses one that a deployment of the project already has, unless
     that deployment is deleted, and a VM name the project's naming gives,
     such as `web-dev-app007`.
   - When the request makes several deployments, they are numbered:
     `Payroll 1`, `Payroll 2`.
   - The description shows with the deployment. Approvers read the
     reason, not the description.
5. **Lease and reason:** how long you need it, within what the environment
   allows. The lease can depend on the item or the inputs: the field says
   which rule or policy sets it, how many times you can extend it, and how
   long the deployment can live in all. In production the reason is what
   approvers read.
6. **Options:**
   - how many;
   - when to start (now, or at a set time);
   - for someone else: they own it and are told.
7. **Submit request**, at the foot of the summary (**Submit for approval**
   when someone must approve it). A field with a problem says what it is
   under the field.

Some policies only warn: a lease longer than the environment's soft
maximum, or a deployment beyond a soft deployment limit, for example. The
summary then shows the warning, and the lease list offers the longer
leases. To go on, select **Submit request anyway**. The request keeps the warning: its page lists it under
**Policy warnings**, for you, its approvers and the project's admins.

Some forms show their sections in another layout, which the item's
administrator chooses:
- **Wizard:** one page at a time, with the steps listed above it. **Next**
  checks the page first: if an input needs fixing, you stay on the page
  and the focus moves to that input. **Back** returns to the page before.
  The last step, **Review**, lists what you entered, page by page, with
  passwords and other secrets hidden. **Change** goes back to a page. The
  deployment's name, the lease, the reason, the options and **Submit
  request** are on that step.
- **Tabs:** one tab per section. To move between tabs, use the arrow keys.
  A tab holding an input that needs fixing shows a warning mark and says
  how many need fixing.

A page or tab whose fields don't apply to what you chose is left out.

Some forms check the whole request when you submit it, sometimes with
another service, such as whether a cost centre is open. In a wizard, the
check runs when you move to **Review**: if it refuses an input, you stay
on that input's page, and the focus moves to it with the message under
it. A message about the whole request shows above the form. If the check
can't run, the request can't be submitted: try again later, and if it
keeps failing, tell the catalog's administrators.

A form you leave can be kept as a **draft** (Resume or Discard it later).
A draft never keeps passwords or other secret fields. A secret field
hides what you type: to check it, select the eye button beside it (or tab
to it and press Space), and select it again to hide it. Secret fields are
never shown again after you submit: they go only to the automation that
needs them.

A date or time field takes only the dates the item allows. A time is in
your browser's time zone, which the field names beside it; rvav keeps the
time zone with the value, so the moment you meant is the one the
automation gets.

Some fields take several values:
- **Tags:** type a tag, then press Enter or type a comma. The field may
  suggest tags as you type. To remove a tag, select its cross, or press
  Backspace in the empty field to remove the last one.
- **Key and value pairs:** select **Add a pair**, then type the key and its
  value. A key can be given only once: if you repeat one, the field says so
  and leaves that pair out until you change it.
- **Tables:** select **Add a row**, then fill in its cells; the first cell
  takes the focus. Each row has a delete button. The table may need a
  number of rows, which it says beside **Add a row**. A problem names its
  row and column, for example "Row 2, Size (GB): Enter 10 or more."
- **Lists of choices:** select each one you want. When you change a field
  the list depends on, it may offer other choices, and the ones it no
  longer offers are cleared.
- **Dual lists:** move what you want from **Available** to **Chosen, in
  order**: select it and then **Add**, or double-click it. With the
  keyboard, tab to a list, move with the arrow keys, Home and End, and
  press Enter to move the option to the other list. The order of the
  chosen list counts: to change it, select **Move up** or **Move down**,
  or press Alt+Up or Alt+Down in that list.

A link in a form opens in a new tab, so what you typed stays.

A group of fields is one value made of several sub-fields, each with its
own label. A problem names its sub-field, for example "Keep (days): Enter
1 or more."

To pick a person, type two letters or more of their name or email, then
select them in the list, or move to them with the arrow keys and press
Enter. A project, deployment or VM field lists only those you may see.

A search field works the same way: type two letters or more, then pick
what it finds. It lists 20 at most: if it finds more, type more to narrow
them down. If the search fails, the field says so: select **Try again**.

A tree shows choices inside one another, such as folders. To open a node,
select its arrow or press the Right arrow key; to close it, press the Left
arrow key. To choose a node, select it, or move to it with the arrow keys
and press Enter. You can also type a letter to move to the next node that
starts with it. Under the tree, **Chosen** shows the path to your choice.

To attach a file, select **Choose a file**, or drop the file on the field.
Under the field, rvav says which types it takes and how large a file may
be. rvav checks the file as soon as you choose it and says why it refuses
one: too large, of another type, or not what its name says. While the
file is sent, the form waits: you can submit, save a draft or go to the
next page once it is. To change the
file, select **Replace**; to remove it, select the cross beside its name.
A file you attach but don't request is deleted after a day. Once you
submit, the request keeps the file: select its name on the request's page
to download it. The request's approvers and the project's administrators
can download it too.

## Following a request

**Requests** lists every request in the project, newest first, under your
drafts. Search by number, title or person, choose a status, or select
**Only mine**. The address keeps what you choose, so a link or a reload
shows the same list. Select a row to open its request. **Overview** shows
your open requests too, with what needs you.

The top of a request's page shows its status, number, project, who asked,
and how many of its steps are done. **Cancel request**, **Request again** and
**Open deployment** are beside it, when they apply. Below, the page shows
each step as it runs: the address reserved, the DNS record, the VM cloned and
customised, each AWX job with its tasks and output. It updates live. You are told when it is ready, or why it failed,
in the app and on your chosen channels.

A composite's request (a PostgreSQL cluster, say) shows its steps as a
graph, in the order they run, each with its status: not started, running,
done, failed, skipped, or undone (what a failure took back). It updates
live and stays where you put it. Select a step, with the mouse or with the
arrow keys and Enter, to see what it did under the graph and its AWX
job's output beside it, or, for a REST call, what it answered
(`POST answered 201`). **Timeline** shows the same steps as a list, with
their times and tasks.

- **Waiting for approval:** the approvers and their deadline are shown.
  Cancel request still works.
- **Waiting for a person:** a composite's step can stop and ask someone, in
  **Waits for a person** beside the graph. If it asks you, its form is there
  (or **Approve** and **Reject**). Otherwise you see who it waits for and until
  when, not what it asks. Cancel request closes the question.
- **Waiting for a time or a condition:** a composite's step can wait for a
  duration, for a date, or for another system to say yes, such as a change being
  approved. The graph shows it **Waiting**. Select it, or open **Timeline**, to
  see what it waits for and until when, and what its last check found. There is
  nothing for you to do, and Cancel request stops it at once.
- **Waiting for a signal:** a composite's step can wait until another
  system sends the request a signal, or until something happens in rvav,
  such as another request finishing. The graph shows it **Waiting**, and
  the timeline says what for and until when. Those who manage the
  project's requests send a signal with `rvavctl requests signal` or the
  API; you can't send it yourself.
- **Loops and catch paths:** a step that runs once for each item of a list
  shows each pass under it on the timeline. A step that failed but whose
  failure was handled stays **Failed**, and the request goes on.
- **A branch:** a composite can take one way or another, depending on what you
  asked for or what an earlier step found. On the graph each way says **taken** or
  **not taken** once it has chosen, and the steps of the others show **Skipped**,
  which isn't a failure. On a form the graph shows the ways there are, not the values
  they compare. A Retry keeps the way it chose.
- **A message:** a composite's step can tell you, the owner or others something. It
  arrives as a notification like any other, on the channels you keep on for **A
  request's message** in your preferences, and it isn't sent again by a Retry.
- **Failed:** the error names the step and the system. On a composite's
  graph, the step that failed is selected.
  - **Retry from the failed step** once the cause is fixed: done steps
    aren't repeated. A failed REST call is made again, even a POST that
    may have reached its service: when in doubt, check there first. A step
    that was rejected asks again, and its first answer stays on the page. A
    wait starts again, with its whole time.
  - **Roll back:** removes the VMs, addresses and DNS records it made,
    and first deletes its custom resources with their type's delete
    step. A composite's AWX, workflow and REST call steps aren't undone
    by it.
  - Once rolled back (by you, or by a composite that undoes what it built
    when it fails), there is nothing left to roll back: **Retry** runs it
    all again, without a new approval. The rollback gave back the
    request's quota and its deployments' names, so the retry takes them
    again: it's refused when a hard quota or deployment limit no longer
    has room, or when another deployment of the project took one of the
    names since. Submit a new request instead.
  - A retried day-2 action, such as a resize, is checked against the
    quotas and deployment limits as they are when you retry it.
- **Request again:** a new request with the same answers.

## Requests on a schedule

An item that makes no deployment, such as a custom workflow or an AWX job,
can run again and again on a schedule: a certificate renewed every week,
a report every night.

To make a schedule:
1. Fill in the item's form as you would for a request.
2. Under **Start**, select **Again and again, on a schedule**.
3. Name the schedule, and choose when it runs:
   - **At a fixed interval:** every so many minutes, hours or days, from
     5 minutes to 31 days. Intervals count from 00:00 UTC, so every day
     runs at midnight UTC.
   - **At the times of a cron expression:** five fields (minute, hour, day
     of month, month and day of week), read in the time zone you give. For
     example, `0 2 * * 1-5` runs at 2:00 on weekdays.
4. Select **Create schedule**.

Each run is a request of its own, made as you, with the form's version,
environment and answers. Policies, approvals and quota check it as they
check a request you submit. A schedule doesn't keep passwords or other
secret fields: leave them empty, or have the item read a stored secret. An
item that makes deployments can't run on a schedule, because each run
would make another.

**Schedules**, on the **Requests** page, lists the schedules you own in the
project, or all of them if you manage the project's requests. Each row
shows how the schedule repeats, its next run, and its last run, with the
request it started or why it didn't start one:
- A run is skipped while the request of the run before hasn't finished,
  waiting for approval included, and while the schedule is paused.
- A run that a policy, the quota or the form refuses, or that your access
  no longer allows, starts nothing and says why.
- A request that a schedule started names it at the top of its page.

From a schedule's menu, you or the project's admins can:
- **Show its runs:** its next five times and its last 20 runs.
- **Pause** it, with an optional reason that shows with it, and **Resume**
  it. The times it misses while paused are skipped.
- **Delete** it: it doesn't run again. The requests it started stay, and
  still name it.

To change a schedule, delete it and make another. When the item's version
that a schedule requests is retired, its runs are refused: make a new
schedule.

## Your deployments

**Deployments** lists what your requests made in the project you work in.
To list the deployments of every project you're in, select **All my
projects**: a **Project** column then names each one's project.

A project may show its deployments to their owners only. You then see the
deployments you own (you requested them, they were requested for you, or
they were handed over to you) and their requests, and the requests you
made, but not your colleagues'. **Requests** then lists the requests you
made or own, with no **Only mine**. Project admins and supervisors still see all of them. To
let a colleague look after one of yours, hand it over to them. You always
see the deployments you own, whatever your role in their project.

To narrow the list:
- search by name, IP or owner;
- choose a status (**Lease ended** lists the deployments whose lease has
  ended, whatever their status), an environment, a catalog item or the VM
  template their VMs were cloned from. Only the values some deployment has
  are offered;
- type a name or email under **Owner** and pick the person, or select
  **Only mine**;
- select **Lease ends within 7 days**.

To sort, select a column's header; select it again to reverse the order.
The whole list is sorted, not only the deployments loaded. To see the
deployments as cards, select **Cards** beside **Table**: sort them with
**Sort by**. Rvav remembers the layout you chose the next time you open
the list. The address keeps your filters, sort and layout, so you can
bookmark it or send it to a colleague, who sees what their projects let
them see.

To keep a set of filters, select **Views**, then **Save as a new view**,
and name it. To show it again, pick it from **Views**. When you change the
filters of the view you opened, **Update** saves the new ones under its
name. You can also rename or delete the view shown. Your views are yours
alone: no one else sees them.

Select a row or a card's name to open its deployment, or its **⋯** menu
for its actions. To act on several at once, in the table, select their
checkboxes: power them on or off, shut down their guests, take a snapshot,
or delete them. A deployment's page shows its status and owner at the top,
and:
- **Overview:** its VMs, their addresses and DNS records, and their state
  in vCenter, live; its details, lease and placement. A VM whose address
  comes from DHCP shows the address it reports, once it has one, and a VM
  with an IPv6 address lists it beside its IPv4 one. Under **Custom
  resources**, what its item's resource types made, such as a load
  balancer: each with its type, its key and its properties;
- **Topology:** what it holds, drawn as a graph: its VMs, the networks and
  NSX segments their adapters are on, the security groups they are in, the
  firewall rules into those groups, and its custom resources, after what
  they were made from;
- **History:** the day-2 actions and updates that ran on it (the request
  that created it is under **Details**);
- **Inputs:** what it was requested with, or last updated with.

On **Topology**, each VM is a node, with an edge for each network adapter.
When the deployment has several components, each component's VMs sit in a
frame named after it, and an arrow shows which component was built after
which. A component of more than four VMs is one node with their count:
select it to list its VMs. To move between nodes with the keyboard, use the
arrow keys, **Home** and **End**; press **Enter** to select one. To read the
same as a list, select **List**.

To see everything about one VM, select its name in the **Overview** list,
or its node on **Topology**. Its properties open in a side panel:
- its guest OS, power state and VMware Tools state;
- each network adapter: network, MAC address, IPv4 and IPv6 addresses, and
  whether the address comes from DHCP or is fixed;
- each disk: size, datastore and provisioning;
- its snapshots and the ISO image it has mounted;
- where it runs: vCenter, cluster, host, resource pool, folder and
  datastores;
- the custom properties it was built with. A property that reads a secret
  input shows `••••`;
- a link that opens the VM in the vSphere Client. The link appears once rvav
  has synced the VM's vCenter.

**Actions**, at the top of the page, lists every action that applies, by
group. For a deployment with several VMs, each VM's actions are in a
submenu with its name, and in the **⋯** menu of its row. A custom
resource's actions are in a submenu with its name under **Custom
resources**, and in its menu on the **Overview**: its type's day-2
actions, chosen by an administrator. An action you
can't run now says why. They depend on the VM and your role, and run as
requests, with their own steps:
- **Power:** on, off, shut down or restart the guest, reset, suspend. A
  suspended VM stops where it is: **Power on** resumes it. While it's
  suspended, you can't change its hardware.
- **Snapshots:** take, revert, delete.
- **Size and hardware:**
  - resize;
  - add a disk, on a datastore the VM's placement rule allows (the VM's
    first disk's by default) and under a storage policy: the rule's when
    it names one, else any of the VM's vCenter, or the datastore's
    default;
  - extend a disk (a linked clone's disk from its template can't be
    extended: add a disk instead);
  - remove a disk: its file and data are deleted from the datastore. You
    can't remove the boot disk, a linked clone's disk from its template,
    or a disk of a VM with snapshots. Unmount the disk's file systems in
    the guest first;
  - add, move or remove a network adapter;
  - mount or unmount an ISO;
  - edit the VM's vSphere tags, among those of the categories its
    environment lets you change. Its other tags stay as they are. A
    category that takes one tag per VM takes one here too. The VM's
    properties list its tags, as the last inventory sync read them.
- **NSX security groups:** add the VM to a group its item offers, or
  remove it from one. A deployment's page shows its groups, the firewall
  rules into them, and a VM taken out of a group in NSX; adding it again
  puts it back.
- **Update:** change the deployment's inputs, move it to its item's new
  version, or add and remove VMs. See
  [Updating a deployment](#updating-a-deployment).
- **Rename, extend the lease.** A deployment named after its VM takes the
  VM's new name, unless another deployment of the project has it.
- **Edit deployment:** change its name and description. No other
  deployment of the project that isn't deleted may have the name, whatever
  its case. Its VMs keep their names.
- **Unregister** (administrators): rvav forgets a VM or a whole
  deployment and leaves it running in vCenter, with its addresses and DNS
  records, so it can be onboarded again.
- **Ownership:** hand over to someone else, or move to another project. A
  project where a deployment already has its name is offered disabled:
  rename it first.
- **The AWX actions your organisation offers** (install an agent, and so
  on).
- **Delete.** A step that fails in vCenter, EfficientIP or NSX stops the
  deletion, and the deployment shows as failed: delete it again once the
  cause is fixed. To finish deleting it anyway, select **Go on past steps
  in vCenter, EfficientIP or NSX that fail**. rvav passes over each step
  that fails, and the last step, **Forget what was left**, lists what
  stays there. A VM that stays keeps its IP addresses and DNS records, so
  they aren't given to another while it runs. rvav forgets it and the
  deployment shows as deleted: delete what was left in vCenter,
  EfficientIP or NSX, or have an administrator onboard a VM that was
  left.

An action that loses something, such as Delete, Power off or Revert to a
snapshot, asks first and says what changes. Delete also asks you to type the
deployment's name. Some actions need approval: deleting in production, going
over a soft quota.

Some actions only warn: the menu says **warns** beside them, and their
dialog says why, for example "Web Platform's VMs serve the checkout: tell
the on-call team before you power one off." To go on, select the action's
button, which then ends in **anyway**, such as **Power off anyway**. The
request keeps the warning: open it from **History** to read it under
**Policy warnings**. Actions on several deployments at once leave out
those whose action warns: run it on each one.

- **Console:** opens the VM's screen in the browser, or in VMware Remote
  Console.
- **Changed outside rvav:** a VM changed in vCenter (CPU, memory, disks,
  networks, tags) shows **drift**: what rvav made beside what vCenter has.
  Tags you change with Edit tags are rvav's, not drift.
  Accept the changes to make them rvav's. Otherwise, change the VM back in
  vCenter.
- **Leases:** you are told before a lease ends. Extend it, or let it end.
  What happens at the end (power off then delete, or delete) is set when
  you request the deployment: by the lease rule that matched it, or by
  the environment's lease policy. A later change to the rule or policy
  doesn't change it. An extension beyond the
  policy, or past the deployment's lifetime, asks the project's
  approvers, or, under a soft policy, warns you and needs no approval. The
  deployment's **Lease** panel says which rule or policy sets its lease,
  how many extensions you used, how long you may extend it, how many days
  it may still live, and when its policy is soft.

## Updating a deployment

An update changes a deployment in place: its size, its disks, the number
of VMs of a component, or the version of its item. To update a
deployment:

1. On the deployment's page, select **Actions**, then **Update**.
   The item's form opens with the deployment's inputs. When the item has
   a newer version, the page says so, and the update moves the deployment
   to it. When the item offers several versions, pick the one to move to
   under **Version**: the newest is preselected, and the deployment's own
   version, while it is still offered, updates only its inputs. The
   inputs you already changed stay changed.
2. Change the inputs. Secret inputs aren't shown: give them again, or
   their field's default applies.
3. Read **What the update does**, beside the form. As you type, it lists:
   - the changes, in the order rvav makes them. A change that deletes
     something, such as a disk and its data or a VM, is marked
     **Deletes**;
   - **Can't be changed in place:** each change rvav can't make to the
     existing VMs, and why;
   - **Quota after this update:** for each resource, the quota with the
     least left afterwards, such as the project's or the owner's in the
     project. A smaller size or fewer VMs frees quota;
   - **Limits after this update:** the deployment's vCPU and memory
     afterwards, against the deployment limits that apply. Only an update
     that grows the deployment can go beyond one;
   - whether the update waits for approval, and from whom.
4. Select **Review the update**. rvav asks you to confirm and names what
   the update deletes.
5. Select **Update**, or **Submit for approval**. The request's page
   opens and shows its steps.

rvav makes these changes to existing VMs: CPU and memory, disks that grow
or are added or removed, DHCP network adapters, and the AWX jobs a new
version adds. It refuses changes that need a VM built again, such as
another template, cluster, datastore or network, a disk that shrinks, or
tags and custom properties. To make one of those, change it back, or
delete the deployment and request it again.

An update changes only the disks rvav built from the item, never a disk
you added with **Add a disk**. On a VM that was onboarded, or built before
rvav recorded its disks, rvav can't tell which disks are the item's: the
update refuses to grow or remove them. To change those disks, use the
VM's disk actions instead.

The same goes for DHCP network adapters: an update moves or removes only
those rvav built from the item, never one you added with **Add a network
adapter**. On a VM built before rvav recorded its adapters, rvav goes by
their places: check the adapters the preview names. When an update failed
part-way, update the deployment again: rvav adds a disk or an adapter only
to the VMs that don't have it yet.

When the item has custom resources, such as a load balancer, the update
makes, updates and deletes them with their type's steps, once the VMs are
built: **What the update does** lists **Add**, **Update** or **Delete**
and the resource's name, a deletion marked **Deletes**. A resource is
updated only when its inputs may change. If its type can't update its
resources, the change is under **Can't be changed in place**: ask an
administrator to give the type an update step, or request a new
deployment. The custom resources a composite made stay as they are.

When the item's form has a VM count, change it to scale out or in:
- **Scale out:** the new VMs are named and addressed as in a new request,
  and their numbers follow the last one.
- **Scale in:** rvav removes the VMs an administrator adopted into the
  deployment first, then those with the highest numbers, with their
  addresses and DNS records. A VM protected from deletion can't be
  removed.

To finish a deployment that failed, update it. With the same inputs, the
update builds the VMs that are missing and runs the AWX jobs again. If
the update fails, the deployment keeps its version and inputs: retry the
request from its page once the cause is fixed.

When nothing changes, or a change is refused, **Review the update** stays
unavailable beside the summary. In a form with steps, the review step's
**Review the update** says why instead of asking you to confirm. Approvers
can't change an update's inputs: if they reject it, change it and update
again.

## Resources

**Resources** lists the VMs in the deployments of the project you work in,
one row per VM, whatever deployment holds it. To list the VMs of every
project you're in, select **All my projects**. Each row shows the VM's
power state, its IPv4 and IPv6 addresses, its deployment, environment,
vCenter and cluster, its vCPUs and memory, its disks (how many, and their
total size) and the networks it's on.

To find VMs:
- search by name, IP address or FQDN;
- choose an environment, a power state, a vCenter, a cluster or a network.
  Only the vCenters, clusters and networks your VMs use are offered.

To sort, select a column's header. The address keeps your filters and sort,
and **Views** saves them under a name, as on **Deployments**.

To see everything about a VM, select its name: its properties open in a
side panel, and the list stays as it is. To open its deployment, select
the deployment's name.

To search from anywhere, press **Ctrl+K** (**⌘K** on a Mac), type at least
two characters, and select **Find … in Resources**.

## Signing in to your VMs

When the item gives the VMs a user, the deployment's **Overview** shows
it under **Remote access**, with how you sign in as that user:
- **With the SSH key given for it:** the public key you gave in the
  request, or one your organisation stores. Sign in with its private key,
  as `ssh <user>@<address>`.
- **With the password given for it:** the password you gave in the
  request, or one your organisation stores.
- **With the deployment's key:** rvav made a key pair for the deployment.
  You download its private key once, below.

To download the deployment's private key:
1. On the deployment's page, under **Remote access**, select **Download
   the private key**. Only the deployment's owner can.
2. Select **Show the private key**. rvav shows the key and forgets it: no
   one can download it again, you included.
3. Select **Save as** to save it as a file, or **Copy** to put it in your
   password manager. If your browser doesn't let rvav copy, rvav says
   **Not copied**: select the key in its box and copy it yourself.
4. Sign in with the command shown:
   `chmod 600 <file> && ssh -i <file> <user>@<address>`.

If you close the panel before you save or copy the key, rvav asks first:
once you close it, no one can download the key again.

Afterwards, **Remote access** says who downloaded the key and when. If
you lose the key, sign in another way (a password, the console) and add
a new key to the user's `~/.ssh/authorized_keys`, or request a new
deployment. Deleting the deployment's VMs forgets a key no one
downloaded.

## Approving

**Approvals** lists the requests waiting for you, oldest first, with why
each needs approval, who asked, and when it is due. A request waits for you
when its level names you or your group, a role you hold in its project,
such as Supervisor, or you are the requester's manager. **Decided** lists those
you have approved or rejected. Select a row to open it in a side panel,
which shows:
- what is asked and why;
- the rules that made it need approval;
- its cost in quota: **Quota after** shows, for vCPU and memory, how much
  of the quota with the least room left is used once it's done, and whose
  quota that is: the project's, the project's in one environment, the
  quota of the person it's for, or one for all of rvav. For example,
  "6 / 8 vCPU (Thomas Nguyen's quota in Web Platform) · 12 / 64 GB (Web
  Platform)".

**Deciding:**
- **Approve:** you may change the inputs your organisation lets approvers
  change, such as a smaller size. The change is recorded and the
  requester told. A smaller or equal size always fits. A larger one is
  checked, for what it adds, against the hard quotas and deployment
  limits of the person the request is for, and refused when it doesn't
  fit. To approve as asked, without opening it, choose
  **Approve** in the row's **⋯** menu.
- **Reject:** with a comment saying why, which the requester reads.
  **Reject…** in the row's **⋯** menu opens the side panel at the comment.
- **Delegate:** hand the decision on.

A composite's step can ask you something in the middle of a run. You are told as
for an approval, and it is in **Approvals** as `A step asks: <its name>`, with a
link to the request's page, where you answer. If it has a form, **Send answer**
sends it, and what you answer goes to the steps after it: leave secrets out. If
it hasn't, **Approve** lets it go on and **Reject**, with a reason the requester
reads, fails it. You answer once, and can't hand it on. A Teams or Slack card can
approve or reject; a form is answered on the page.
- **From Teams or Slack:** when the notification arrives there, Approve
  and Reject work from the message.

You can't approve your own request.

## Notifications

The bell lists what concerns you. Its **All notifications and
preferences** page chooses the channels for each kind: in the app, email,
Teams, Slack. A test message checks that each channel works.

## API tokens

**API tokens** (your menu) are for scripts and tools acting as you:
- name a token and give it an expiry;
- it is shown once;
- revoke it when it's no longer used: rvav asks first, as what uses it is
  refused from then on.

A token acts with the roles given to you in rvav itself, not those from
your groups, so it can't approve. The [API reference](../operations/README.md#api) describes every
call.

When a script requests an item or runs a day-2 action, it sends only the
inputs it chooses. rvav gives each input it leaves out the value the
form starts with in the web app: the initial value, or the default its
rules work out. To leave an input that has a default empty, send it as
`null`.

## The command line

[rvavctl](cli.md) requests, follows requests, runs day-2 actions and
decides approvals from a terminal, signed in as you.
