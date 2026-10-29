// rvav's load test (S14.7, docs/operations/load-test.md): 200 people using
// the web app at once while 500 requests an hour are made, through the web
// app's nginx with Keycloak sign-in, as in production. deploy/load/run.sh
// runs it against the compose stack.
//
//   USERS   people browsing at once (200)
//   RATE    requests an hour: VMs, and day-2 actions on the VMs it made (500)
//   DURATION  how long both last after a 2-minute ramp-up (60m)
import http from "k6/http";
import { check, sleep } from "k6";
import { Counter } from "k6/metrics";

const BASE = __ENV.RVAV_URL || "http://localhost:3000";
const IDP = __ENV.RVAV_IDP || "http://localhost:8081/realms/rvav";
const USERS = Number(__ENV.USERS || 200);
const RATE = Number(__ENV.RATE || 500);
const DURATION = __ENV.DURATION || "60m";
const PROJECT = "prj_web";
// The test's VMs are web-dev-ld001…: one name prefix, so requests made
// together contend for it, as a team's do.
const ROLE = "ld";

export const options = {
  scenarios: {
    people: {
      executor: "ramping-vus",
      exec: "browse",
      stages: [
        { duration: "2m", target: USERS },
        { duration: DURATION, target: USERS },
        { duration: "30s", target: 0 },
      ],
      gracefulRampDown: "10s",
    },
    requests: {
      executor: "constant-arrival-rate",
      exec: "submit",
      rate: RATE,
      timeUnit: "1h",
      startTime: "2m",
      duration: DURATION,
      preAllocatedVUs: 5,
      maxVUs: 30,
    },
  },
  thresholds: {
    // The 1.0 gate: reads under 300 ms at the 95th percentile.
    "http_req_duration{kind:read}": ["p(95)<300"],
    "http_req_duration{kind:write}": ["p(95)<1000"],
    "http_req_failed{kind:read}": ["rate<0.01"],
    "http_req_failed{kind:write}": ["rate<0.01"],
    checks: ["rate>0.99"],
  },
  summaryTrendStats: ["avg", "med", "p(95)", "p(99)", "max"],
};

const submitted = new Counter("rvav_requests_submitted");

// Each virtual user signs in as one of the realm's people and keeps its
// token (15 minutes at Keycloak) until 2 minutes before it ends.
let session = null;

function signIn(user) {
  const r = http.post(
    `${IDP}/protocol/openid-connect/token`,
    { grant_type: "password", client_id: "rvav", username: user, password: "rvav", scope: "openid" },
    { tags: { kind: "auth" } },
  );
  check(r, { "signed in": (x) => x.status === 200 });
  const body = r.json();
  return { user, token: body.access_token, until: Date.now() + (body.expires_in - 120) * 1000 };
}

function as(user) {
  if (!session || session.user !== user || Date.now() > session.until) session = signIn(user);
  return { Authorization: `Bearer ${session.token}` };
}

function get(user, path, name) {
  const r = http.get(`${BASE}/api/v1${path}`, { headers: as(user), tags: { kind: "read", name } });
  check(r, { [`${name} 200`]: (x) => x.status === 200 });
  return r;
}

function post(user, path, body, name) {
  const r = http.post(`${BASE}/api/v1${path}`, JSON.stringify(body), {
    headers: { ...as(user), "Content-Type": "application/json", "Idempotency-Key": uuid() },
    tags: { kind: "write", name },
  });
  check(r, { [`${name} 202`]: (x) => x.status === 202 });
  if (r.status === 202) submitted.add(1, { name });
  else console.warn(`${name}: ${r.status} ${r.body}`);
  return r;
}

function uuid() {
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const v = (Math.random() * 16) | 0;
    return (c === "x" ? v : (v & 0x3) | 0x8).toString(16);
  });
}

const pick = (list) => list[Math.floor(Math.random() * list.length)];

// What a person does in the app: one page at a time (the calls the web app
// makes for it), then reads it for 5 to 15 seconds.
const people = ["lea", "thomas", "marc"];
const pages = [
  function home(u) {
    get(u, "/me", "me");
    get(u, "/me/notifications?limit=20", "notifications");
    get(u, `/requests?project_id=${PROJECT}&limit=5`, "requests");
    get(u, `/deployments?project_id=${PROJECT}&limit=5`, "deployments");
  },
  function catalog(u) {
    get(u, `/catalog/items?project_id=${PROJECT}`, "catalog");
    get(u, `/catalog/items/rhel9-server?project_id=${PROJECT}`, "catalog item");
    get(u, `/catalog/items/rhel9-server/form?project_id=${PROJECT}`, "form");
  },
  function requests(u) {
    const list = get(u, `/requests?project_id=${PROJECT}&limit=25`, "requests");
    const items = list.status === 200 ? list.json("items") : [];
    if (items.length) {
      const id = pick(items).id;
      get(u, `/requests/${id}`, "request");
      get(u, `/requests/${id}/steps`, "request steps");
    }
  },
  function deployments(u) {
    const list = get(u, `/deployments?project_id=${PROJECT}&limit=25`, "deployments");
    const items = list.status === 200 ? list.json("items") : [];
    if (items.length) {
      const id = pick(items).id;
      get(u, `/deployments/${id}`, "deployment");
      get(u, `/deployments/${id}/history`, "deployment history");
      get(u, `/deployments/${id}/actions`, "deployment actions");
    }
  },
  function approvals(u) {
    get(u, "/approvals", "approvals");
  },
];

export function browse() {
  const u = people[(__VU - 1) % people.length];
  pick(pages)(u);
  sleep(5 + Math.random() * 10);
}

// A request: 40% are VMs, 30% deletes of the test's own VMs (which keep
// the subnet from filling up), 15% snapshots, 15% power actions.
export function submit() {
  let u = Math.random() < 0.5 ? "thomas" : "lea";
  const r = Math.random();
  const vms =
    r < 0.4
      ? []
      : get(u, `/deployments?project_id=${PROJECT}&status=ready&q=web-dev-${ROLE}&limit=100`, "test vms").json("items") ||
        [];
  if (vms.length === 0) {
    post(
      u,
      "/requests",
      {
        catalog_item_id: "itm_rhel9-server",
        version_id: "ver_rhel9-server_1",
        project_id: PROJECT,
        environment: "dev",
        inputs: {
          role: ROLE,
          size: "S",
          cluster: "GVA-DEV-CL01",
          datastore: "vsan-gva-dev-cl01",
          network: "dvpg-app-dev-vl310",
          post_config: ["linux-baseline"],
        },
      },
      "submit vm",
    );
    return;
  }
  const d = pick(vms);
  // Day-2 actions as the VM's owner: a member may only delete their own.
  const owner = { "lea.martin@corp.example": "lea", "thomas.nguyen@corp.example": "thomas" }[d.owner && d.owner.email];
  if (owner) u = owner;
  if (r < 0.7) {
    post(u, `/deployments/${d.id}/actions`, { action: "delete", inputs: {} }, "delete");
    return;
  }
  const detail = get(u, `/deployments/${d.id}`, "deployment").json();
  const vm = (detail.resources || []).find((x) => x.type === "vsphere.vm");
  if (!vm) return;
  if (r < 0.85) {
    post(u, `/deployments/${d.id}/actions`, { action: "snapshot_create", resource_id: vm.id, inputs: { name: `load ${uuid().slice(0, 8)}` } }, "snapshot");
  } else {
    const action = detail.vms_powered_on > 0 ? "power_off" : "power_on";
    post(u, `/deployments/${d.id}/actions`, { action, resource_id: vm.id, inputs: {} }, action);
  }
}
