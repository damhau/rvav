# Load test

rvav's 1.0 gate asks for 200 people using it at once while 500 requests an
hour are made, with the API's 95th percentile under 300 ms (S14.7). This
page covers:
- how the test runs;
- the environment it ran on;
- its results;
- the bottlenecks it found and what was done about them.

## The test

`deploy/load/rvav.js` (k6) goes through the web app's nginx, as a browser
would. People sign in with Keycloak: each virtual user gets a token by
password grant and keeps it until shortly before it expires. Two scenarios
run together:

- **200 people** (`USERS`) browse after a two-minute ramp-up. Each opens a
  page, makes the API calls the web app makes for it, then reads for 5 to
  15 seconds. The pages are:
  - home: who they are, notifications, recent requests and deployments;
  - the catalog, an item and its form;
  - the request list, a request and its steps;
  - the deployment list, a deployment, its history and its actions;
  - the approvals inbox.
- **500 requests an hour** (`RATE`), at a constant rate, as two people of
  the Web Platform project:
  - 40%: RHEL 9 servers (a VM, an address, a DNS record, the
    `linux-baseline` AWX job);
  - 30%: deletes of the VMs the test made;
  - 15%: snapshots;
  - 15%: power actions.

  Day-2 actions are made as the VM's owner. All the VMs share one name
  prefix (`web-dev-ld`), so requests contend for its name counter, as a
  team's do.

`deploy/load/burst.js` submits 20 VMs of one name prefix at the same
moment.

**Thresholds** (the run fails when one is crossed):
- reads: 95th percentile under 300 ms, under 1% errors;
- writes (submissions): 95th percentile under 1 s, under 1% errors;
- 99% of checks passing.

After the run, `deploy/load/run.sh` waits for every request made to finish
and reports:
- their outcomes and failures;
- how long VMs took;
- the server's latency by route, integration and step (Prometheus);
- the containers' CPU and memory each minute.

The results go to `deploy/load/results/<time>/`.

```sh
RVAV_AUTH_MODE=oidc RVAV_FAKE_EFFICIENTIP_LATENCY=100ms docker compose \
  -f deploy/docker-compose.yml -f deploy/observability/docker-compose.observability.yml up -d --build --wait
deploy/load/run.sh                       # USERS=200 RATE=500 DURATION=60m
SCRIPT=burst.js deploy/load/run.sh       # BURST=20
```

`run.sh` lifts the project's quotas for the run and puts them back.

## Environment

**The machine:** one workstation running everything (rvav and what it
talks to), with:
- an AMD Ryzen 9 7950X (16 cores, 32 threads) and 46 GB of memory;
- Linux 6.18 under WSL2, Docker 29.1.

**The compose stack**, one replica each:
- the API, the worker and the sync;
- the web app (nginx 1.30);
- PostgreSQL 16.4 and Temporal 1.25;
- Keycloak 25;
- vcsim, the vCenter simulator;
- the simulated AWX (each task 1.5 s);
- the fake SOLIDserver, answering every call in 100 ms
  (`RVAV_FAKE_EFFICIENTIP_LATENCY`), as a SOLIDserver across a network
  would.

The fake systems answer faster and more steadily than real ones. A VM
built here takes 12 s: vcsim clones at once, and the simulated
`linux-baseline` job takes most of that time. The test measures rvav,
Temporal and PostgreSQL under load, not vCenter, AWX or SOLIDserver.

## Results

Run on 2026-10-29: 60 minutes after the ramp-up, 200 people, 500
requests an hour. **Every threshold passed.**

| | Calls | Median | 95th percentile | 99th percentile | Max | Errors |
|---|---|---|---|---|---|---|
| Reads (k6, through nginx) | 220,542 | 3.1 ms | 13.4 ms | 41.4 ms | 109 ms | 0 |
| Submissions (k6) | 500 | 31 ms | 251 ms | 259 ms | 301 ms | 3 (0.6%) |

**The 500 submissions:**
- 497 accepted, and all of them succeeded:
  - 186 VMs;
  - 156 deletes;
  - 76 snapshots;
  - 79 power actions.
- 3 snapshots refused: the VM already had the 3 snapshots the
  development environment allows. That is the policy at work, not an
  error.

**VMs:** 12.3 s from start to Ready (median and 95th percentile alike).

**The server's side (Prometheus):**
- **API, 95th percentile:** under 5 ms for every read route, 24 ms to
  submit, 46 ms for a day-2 action.
- **Steps, 95th percentile:**
  - vSphere: 0.1 s;
  - IPAM and DNS: 0.5 s;
  - AWX: 14 s (the simulated job).

**CPU and memory, per container** (100% is one core of 32; sampled each
minute):

| Container | CPU (avg / max) | Memory |
|---|---|---|
| PostgreSQL | 21% / 39% | 297 MB |
| API | 15% / 27% | 37 MB |
| Temporal | 8% / 30% | 279 MB |
| Web (nginx) | 3% / 5% | 34 MB |
| Sync | 2% / 34% (full reads) | 22 MB |
| Worker | 1% / 8% | 33 MB |

At this load rvav uses a fraction of one core per process. The
submission rate is far from what a worker or PostgreSQL can take, and
the browsing load is mostly PostgreSQL reads.

**Before the fixes below,** a 15-minute run of the same test passed
too:
- reads: median 7.5 ms, 95th percentile 12.8 ms;
- the server's read routes: 95th percentile about 10 ms;
- the API at 20% CPU.

Removing the per-call user writes halved the median read latency (the
95th percentile hardly moved), and took
the API from 20% CPU to 15%.

## Bottlenecks found

1. **DNS checks while the name counter is locked.** A submission locks the
   name prefix it gives names from until it commits. Under that lock it
   asks DNS whether its name is free, unless its evaluation already asked
   about that very name. When several requests for one prefix arrive
   together, each finds the name it looked at taken by the one before. It
   then asks DNS about the next name with the lock held, and the others
   wait.
   - **Before:** 20 VMs submitted together took 1.54 s (median) and
     2.83 s at worst, against 260 ms for one alone. That is one 100 ms
     SOLIDserver call each, one after the other.
   - **The fix:** a submission now looks up, before taking the lock and all
     at once, as many names ahead as other submissions for that prefix are
     under way in its process. Under the lock its name is answered from
     memory. A request submitted alone looks up nothing more than before.
   - **After:** 0.79 s (median) and 1.0 s at worst. `TestConcurrentNames`
     checks that requests submitted together still get distinct names and
     skip names in DNS.
   - **What remains:** the submissions still take turns (about 30 ms each)
     for the rest of their transaction. With several API replicas, each
     looks ahead for its own submissions only.
2. **Every API call signed in with OIDC wrote the user.** Each call locked
   and updated the user's row and their groups (two transactions), so a
   page's parallel calls from one person took turns on that row, and every
   read wrote to the database. It cost half the read latency (median
   7.5 ms before, 3.1 ms after).
   - **The fix:** a token's user and groups are now remembered once it has
     been checked, for its lifetime or 5 minutes, whichever is shorter. Its
     signature and expiry are still checked on every call, and roles are
     read every time, so a change of role in rvav applies at once.
   - A change of name or groups at the identity provider shows within
     5 minutes.
3. **Found by the test, not by load:** a day-2 policy's reason already
   ending with a full stop got a second one in the API's answer. Fixed.

## Not covered

- **The live stream** (`/api/v1/events`): each browser keeps one open. k6
  doesn't hold them.
- **The console:** VMRC and WebMKS need a real vCenter.
- **Real integrations:** vCenter, AWX and SOLIDserver under rvav's load.
  That is for the lab; the alerts in [observability.md](observability.md)
  watch their latency and errors in production.
