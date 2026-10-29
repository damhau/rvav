// A burst on one name prefix (S14.7): BURST people submit a VM of the same
// kind at the same moment, so each submission waits for the name counter
// the one before holds. With a SOLIDserver that takes time to answer, what
// is done while the counter is locked (DNS checks of the names) shows in
// how long the last one waits.
//
//   SCRIPT=burst.js deploy/load/run.sh
import http from "k6/http";
import { check } from "k6";

const BASE = __ENV.RVAV_URL || "http://localhost:3000";
const IDP = __ENV.RVAV_IDP || "http://localhost:8081/realms/rvav";
const BURST = Number(__ENV.BURST || 20);

export const options = {
  scenarios: { burst: { executor: "per-vu-iterations", vus: BURST, iterations: 1, maxDuration: "2m" } },
  summaryTrendStats: ["min", "med", "p(95)", "max"],
};

function uuid() {
  return "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx".replace(/[xy]/g, (c) => {
    const v = (Math.random() * 16) | 0;
    return (c === "x" ? v : (v & 0x3) | 0x8).toString(16);
  });
}

export function setup() {
  const r = http.post(`${IDP}/protocol/openid-connect/token`, {
    grant_type: "password", client_id: "rvav", username: "thomas", password: "rvav", scope: "openid",
  });
  return { token: r.json("access_token"), role: "bu" + String.fromCharCode(97 + Math.floor(Math.random() * 26)) };
}

export default function (data) {
  const r = http.post(
    `${BASE}/api/v1/requests`,
    JSON.stringify({
      catalog_item_id: "itm_rhel9-server",
      version_id: "ver_rhel9-server_1",
      project_id: "prj_web",
      environment: "dev",
      inputs: {
        role: data.role, size: "S", cluster: "GVA-DEV-CL01", datastore: "vsan-gva-dev-cl01",
        network: "dvpg-app-dev-vl310", post_config: ["linux-baseline"],
      },
    }),
    {
      headers: {
        Authorization: `Bearer ${data.token}`, "Content-Type": "application/json",
        "Idempotency-Key": uuid(),
      },
    },
  );
  if (!check(r, { submitted: (x) => x.status === 202 })) console.warn(`${r.status} ${r.body}`);
}
