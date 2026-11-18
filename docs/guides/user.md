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
| Project admin | Act on every VM of the project, hand them over, delete them. |

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
2. **The item's page:** open an item to read its description, the version
   that requests use, whether it needs approval, how long it usually takes,
   and what it builds: its VMs, networks and groups, or a composite's steps
   in the order they run. **Request** opens the form. If your roles in the
   project don't let you request, the page says so.
3. **The form:** environment first (dev, tst, prd), then what the item
   asks. The panel beside it shows what you will get, as you fill it in:
   - the VMs' names;
   - the size and the quota it uses;
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
4. **Lease and reason:** how long you need it, within what the environment
   allows. In production the reason is what approvers read.
5. **Options:**
   - how many;
   - when to start (now, or at a set time);
   - for someone else: they own it and are told.
6. **Submit request.**

A form you leave can be kept as a **draft** (Resume or Discard it later).
A draft never keeps passwords or other secret fields. Secret fields are
never shown again after you submit: they go only to the automation that
needs them.

## Following a request

**Requests** lists every request in the project, newest first, under your
drafts. Search by number, title or person, choose a status, or select
**Only mine**. The address keeps what you choose, so a link or a reload
shows the same list. Select a row to open its request. **Overview** shows
your open requests too, with what needs you.

The request's page shows each step as it runs: the address reserved, the
DNS record, the VM cloned and customised, each AWX job with its tasks and
output. It updates live. You are told when it is ready, or why it failed,
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
  - **Roll back:** removes the VMs, addresses and DNS records it made. A
    composite's AWX, workflow and REST call steps aren't undone by it.
  - Once rolled back (by you, or by a composite that undoes what it built
    when it fails), there is nothing left to roll back: **Retry** runs it
    all again, without a new approval.
- **Request again:** a new request with the same answers.

## Your deployments

**Deployments** lists what your requests made. Search by name, IP or
owner, choose a status or an environment, or select **Lease ends within 7
days** or **Only mine**. The address keeps what you choose. Select a row to
open its deployment, or its **⋯** menu for its actions. To act on several
at once, select their checkboxes: power them on or off, shut down their
guests, take a snapshot, or delete them. A deployment shows:
- its VMs, their addresses and DNS records;
- their state in vCenter, live;
- its history.

**Actions** depend on the VM and your role. They run as requests, with
their own steps:
- **Power:** on, off, shut down or restart the guest, reset.
- **Snapshots:** take, revert, delete.
- **Size and hardware:**
  - resize;
  - add or extend a disk;
  - add, move or remove a network adapter;
  - mount or unmount an ISO.
- **NSX security groups:** add the VM to a group its item offers, or
  remove it from one. A deployment's page shows its groups, the firewall
  rules into them, and a VM taken out of a group in NSX; adding it again
  puts it back.
- **Rename, extend the lease.**
- **Ownership:** hand over to someone else, or move to another project.
- **The AWX actions your organisation offers** (install an agent, and so
  on).
- **Delete.**

Some actions need approval: deleting in production, going over a soft
quota.

- **Console:** opens the VM's screen in the browser, or in VMware Remote
  Console.
- **Changed outside rvav:** a VM changed in vCenter (CPU, memory, disks,
  networks) shows **drift**: what rvav made beside what vCenter has.
  Accept the changes to make them rvav's. Otherwise, change the VM back in
  vCenter.
- **Leases:** you are told before a lease ends. Extend it, or let it end.
  What happens at the end (power off then delete, or delete) is the
  environment's policy.

## Approving

**Approvals** lists the requests waiting for you, oldest first. Each shows:
- what is asked and why;
- the rules that made it need approval;
- its cost in quota.

**Deciding:**
- **Approve:** you may change the inputs your organisation lets approvers
  change, such as a smaller size. The change is recorded and the
  requester told.
- **Reject:** with a reason, which the requester reads.
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
- revoke it when it's no longer used.

A token acts with the roles given to you in rvav itself, not those from
your groups, so it can't approve. The [API reference](../operations/README.md#api) describes every
call.
