// The mocks' extensibility refusals against the server's (ADR-0021,
// ADR-0050, ADR-0056): who reads and who changes subscriptions, resource
// types and day-2 forms, a write's unknown field, a subscription's defaults
// and messages, and what a composite's VM step can't be.
import { afterEach, describe, expect, it } from "vitest";
import { api, call, isApiError } from "@/lib/api/client";
import type { components } from "@/lib/api/schema";
import type { Day2FormDefinition, VmResource } from "@/lib/api/types";

type SubscriptionWrite = components["schemas"]["SubscriptionWrite"];
import { db, resetDb } from "./seed";

const as = (persona: string) => {
  localStorage.setItem("rvav.mock.as", persona);
  resetDb();
};

afterEach(() => localStorage.removeItem("rvav.mock.as"));

const refusal = async (p: Promise<unknown>) => {
  try {
    await p;
  } catch (e) {
    if (isApiError(e))
      return { status: e.status, title: e.problem.title, detail: e.problem.detail, errors: e.problem.errors ?? [] };
    throw e;
  }
  throw new Error("it was not refused");
};

/** A write as the server takes it, without the fields that have defaults. */
const asWrite = (w: Partial<SubscriptionWrite> & Record<string, unknown>) => w as SubscriptionWrite;

const webhook = { webhook: { url: "https://cmdb.corp.example/rvav/check", secret: "webhook-signing" } };

describe("the mocks' subscriptions", () => {
  it("are read by an auditor, and changed only with extensibility.manage", async () => {
    as("auditor");
    const { items } = await call(api.GET("/admin/subscriptions"));
    expect(items.length).toBeGreaterThan(0);
    await call(api.GET("/admin/subscriptions/{id}", { params: { path: { id: "sub_cmdb_check" } } }));
    const refused = await refusal(
      call(
        api.POST("/admin/subscriptions", { body: asWrite({ name: "X", topic: "provisioning.post", target: webhook }) }),
      ),
    );
    expect(refused).toMatchObject({ status: 403, title: "Not allowed" });
    expect(
      await refusal(call(api.DELETE("/admin/subscriptions/{id}", { params: { path: { id: "sub_cmdb_check" } } }))),
    ).toMatchObject({ status: 403 });
  });

  it("take the server's defaults, and a write replaces what it leaves out", async () => {
    const sub = await call(
      api.POST("/admin/subscriptions", {
        body: asWrite({ name: " CMDB record ", topic: "provisioning.post", target: webhook }),
      }),
    );
    expect(sub).toMatchObject({
      name: "CMDB record",
      condition: "",
      blocking: false,
      on_failure: "stop_request",
      priority: 100,
      timeout_seconds: 30,
      enabled: true,
    });
    const was = await call(api.GET("/admin/subscriptions/{id}", { params: { path: { id: "sub_cmdb_check" } } }));
    expect(was).toMatchObject({ priority: 10, condition: 'environment == "prd"', blocking: true });
    const put = await call(
      api.PUT("/admin/subscriptions/{id}", {
        params: { path: { id: "sub_cmdb_check" } },
        body: asWrite({ name: was.name, topic: was.topic, target: was.target }),
      }),
    );
    expect(put).toMatchObject({
      id: was.id,
      priority: 100,
      condition: "",
      blocking: false,
      created_at: was.created_at,
    });
    expect(
      await refusal(
        call(
          api.PUT("/admin/subscriptions/{id}", {
            params: { path: { id: was.id } },
            body: asWrite({ name: was.name, topic: was.topic, target: was.target, recovery: webhook }),
          }),
        ),
      ),
    ).toMatchObject({
      status: 422,
      errors: [
        {
          field: "/recovery",
          message: "Only a blocking subscription has a recovery target: it is told when the subscription fails.",
        },
      ],
    });
  });

  it("refuse a field the write doesn't take, before asking who writes", async () => {
    as("auditor");
    expect(
      await refusal(
        call(
          api.POST("/admin/subscriptions", {
            body: asWrite({ name: "X", topic: "provisioning.post", target: webhook, retries: 3 }),
          }),
        ),
      ),
    ).toEqual({
      status: 400,
      title: "The request body has a field this operation doesn't take",
      detail: 'json: unknown field "retries"',
      errors: [],
    });
  });

  it("say what is wrong in the server's words", async () => {
    const { errors } = await refusal(
      call(
        api.POST("/admin/subscriptions", {
          body: asWrite({
            name: " ",
            topic: "provisioning.post",
            condition: "environment ==",
            on_failure: "retry" as never,
            timeout_seconds: 0,
            target: { webhook: { url: "https://example.org/hook", secret: "" } },
          }),
        }),
      ),
    );
    expect(errors.map((e) => [e.field, e.message])).toEqual([
      ["/name", "Give the subscription a name."],
      ["/condition", expect.stringMatching(/^This condition is not valid CEL: /)],
      ["/on_failure", "stop_request or continue_request."],
      ["/timeout_seconds", "From 1 second to an hour."],
      [
        "/target/webhook/url",
        "example.org is not one of the hosts webhooks may call (Administration › Integrations › Allowed hosts).",
      ],
      ["/target/webhook/secret", "A webhook's calls are signed: give the stored secret to sign them with."],
    ]);
    const other = async (target: object) =>
      (
        await refusal(
          call(api.POST("/admin/subscriptions", { body: asWrite({ name: "X", topic: "provisioning.post", target }) })),
        )
      ).errors.map((e) => [e.field, e.message]);
    expect(await other({ ...webhook, awx: { job_template: "x" } })).toEqual([
      ["/target", "Give one target: a webhook, an AWX template or a custom workflow."],
    ]);
    expect(await other({ awx: { job_template: " " } })).toEqual([
      ["/target/awx", "Name the job template, or give its AWX ID."],
    ]);
    expect(await other({ workflow: { key: "nothing" } })).toEqual([
      ["/target/workflow/key", "There is no custom workflow nothing registered."],
    ]);
  });
});

describe("the mocks' resource types", () => {
  it("are read by an auditor, and changed only with extensibility.manage", async () => {
    as("auditor");
    const { items } = await call(api.GET("/admin/resource-types"));
    expect(items.map((t) => t.key)).toContain("load-balancer");
    expect(
      await refusal(call(api.DELETE("/admin/resource-types/{key}", { params: { path: { key: "dns-alias" } } }))),
    ).toMatchObject({ status: 403, title: "Not allowed", detail: "You do not have permission to do this." });
  });

  it("refuse a field the write doesn't take", async () => {
    const t = await call(api.GET("/admin/resource-types/{key}", { params: { path: { key: "dns-alias" } } }));
    expect(
      await refusal(
        call(
          api.PUT("/admin/resource-types/{key}", {
            params: { path: { key: "dns-alias" } },
            body: { title: t.title, create: t.create, property_schema: t.property_schema, icon: "dns" } as never,
          }),
        ),
      ),
    ).toMatchObject({ status: 400, detail: 'json: unknown field "icon"' });
  });
});

describe("the mocks' day-2 forms", () => {
  const key = "awx.install-monitoring-agent";
  const definition: Day2FormDefinition = {
    form_schema: {
      type: "object",
      properties: { monitoring_agent: { type: "string", enum: ["node_exporter", "telegraf"], default: "telegraf" } },
    },
    ui_schema: {
      fields: { monitoring_agent: { label: "Agent", widget: "select" } },
      layout: { kind: "page", sections: [{ key: "main", title: "", columns: 1, fields: ["monitoring_agent"] }] },
    },
    initial_values: {},
  };

  it("are extensibility.manage's, to read them too", async () => {
    as("auditor");
    expect(await refusal(call(api.GET("/admin/day2-forms")))).toMatchObject({ status: 403, title: "Not allowed" });
    expect(
      await refusal(call(api.GET("/admin/day2-forms/{action}", { params: { path: { action: key } } }))),
    ).toMatchObject({ status: 403 });
  });

  it("remove a form that was never saved with 204, and refuse an unknown field or action", async () => {
    await call(api.DELETE("/admin/day2-forms/{action}", { params: { path: { action: key } } }));
    expect(
      await refusal(call(api.DELETE("/admin/day2-forms/{action}", { params: { path: { action: "awx.nothing" } } }))),
    ).toMatchObject({ status: 404 });
    expect(
      await refusal(
        call(
          api.PUT("/admin/day2-forms/{action}", {
            params: { path: { action: key } },
            body: { definition, enabled: true } as never,
          }),
        ),
      ),
    ).toMatchObject({ status: 400, detail: 'json: unknown field "enabled"' });
  });

  it("preview a form starting with its schema's defaults", async () => {
    await call(api.PUT("/admin/day2-forms/{action}", { params: { path: { action: key } }, body: { definition } }));
    const d = db.deployments.find((x) => x.resources.some((r) => r.type === "vsphere.vm"))!;
    const vm = d.resources.find((r): r is VmResource => r.type === "vsphere.vm")!;
    const preview = await call(
      api.POST("/admin/day2-forms/{action}/preview", {
        params: { path: { action: key } },
        body: { deployment_id: d.id, resource_id: vm.id, inputs: {} },
      }),
    );
    expect(preview.form.initial_values).toEqual({ monitoring_agent: "telegraf" });
  });
});

describe("the mocks' composite VM steps", () => {
  async function draftOf(slug: string) {
    const item = await call(
      api.POST("/admin/catalog/items", {
        body: { slug, name: slug, summary: "VMs.", category: "compute", type: "composite" },
      }),
    );
    const at = { params: { path: { itemId: item.id, versionId: item.draft_version!.id } } };
    const draft = await call(api.GET("/admin/catalog/items/{itemId}/versions/{versionId}", at));
    return { at, draft };
  }

  it("refuse on save, where it is written, what a VM step can't be or have", async () => {
    const { at, draft } = await draftOf("vm-flow");
    const blueprint = {
      kind: "composite",
      steps: [
        { key: "web", item: "rhel9-server", when: "inputs.ha", with: { name: "${steps.ask.answer.name}" } },
        {
          key: "way",
          branch: { cases: [{ when: "inputs.ha", steps: [{ key: "spare", item: "rhel9-server" }] }] },
        },
      ],
    };
    const { status, errors } = await refusal(
      call(
        api.PUT("/admin/catalog/items/{itemId}/versions/{versionId}", {
          ...at,
          body: { definition: { ...draft.definition, blueprint }, base_revision: draft.revision },
        }),
      ),
    );
    expect(status).toBe(422);
    const vm = errors.filter((e) => /VM/.test(e.message));
    expect(vm.map((e) => [e.field, e.message])).toEqual([
      [
        "/definition/blueprint/steps/0/when",
        "A VM step has no condition: its VMs are planned when the request is submitted.",
      ],
      [
        "/definition/blueprint/steps/0/with/name",
        "A VM is planned when the request is submitted, so its inputs come from the form, not from steps.",
      ],
      [
        "/definition/blueprint/steps/1/branch/cases/0/steps/0",
        "A VM is planned when the request is submitted, before a branch chooses or a step fails, so it can't be on a side of a branch, on a catch path or in the error handler.",
      ],
    ]);
  });
});
