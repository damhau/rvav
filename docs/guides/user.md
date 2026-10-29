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

The project you work in is chosen at the top of the page. Your role in it
shows under your name.

## Requesting a VM

1. **Catalog:** find the item by name, category or the filter. Star the
   ones you use often.
2. **The form:** environment first (dev, tst, prd), then what the item
   asks. The panel beside it shows what you will get, as you fill it in:
   - the VMs' names;
   - the size and the quota it uses;
   - whether someone must approve it, and who;
   - the lease, and the placement.
3. **Lease and reason:** how long you need it, within what the environment
   allows. In production the reason is what approvers read.
4. **Options:**
   - how many;
   - when to start (now, or at a set time);
   - for someone else: they own it and are told.
5. **Submit request.**

A form you leave can be kept as a **draft** (Resume or Discard it later).
A draft never keeps passwords or other secret fields. Secret fields are
never shown again after you submit: they go only to the automation that
needs them.

## Following a request

The request's page shows each step as it runs: the address reserved, the
DNS record, the VM cloned and customised, each AWX job with its tasks and
output. It updates live. You are told when it is ready, or why it failed,
in the app and on your chosen channels.

- **Waiting for approval:** the approvers and their deadline are shown.
  Cancel request still works.
- **Failed:** the error names the step and the system.
  - **Retry from the failed step** once the cause is fixed: done steps
    aren't repeated.
  - **Roll back:** removes what was made.
- **Request again:** a new request with the same answers.

## Your deployments

**Deployments** lists what your requests made. Filter by status,
environment or a lease ending soon. A deployment shows:
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
