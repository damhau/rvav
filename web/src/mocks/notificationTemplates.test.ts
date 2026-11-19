// The mocks' notification templates against the server's:
// internal/notify/testdata/templates.json, which TestTemplatesFixture keeps
// current. The mocks hold a copy (the web app's build sees web/ only).
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { TemplateError, checkFields, execute, parse } from "./gotemplate";
import copy from "./notificationTemplates.json";
import { templateFixture } from "./notificationTemplates";

// Vitest runs in web/, where its configuration is.
const server = JSON.parse(
  readFileSync(resolve(process.cwd(), "../internal/notify/testdata/templates.json"), "utf8"),
) as unknown;

const approval = templateFixture.kinds.find((k) => k.kind === "approval_needed")!;
const vars = approval.variables.map((v) => v.name);
const lists = approval.variables.filter((v) => v.type === "list of text").map((v) => v.name);

const problemOf = (src: string) => {
  try {
    checkFields(parse(src), vars, lists);
    return "";
  } catch (e) {
    if (e instanceof TemplateError) return e.message;
    throw e;
  }
};

describe("the mocks' notification templates", () => {
  it("are the server's", () => {
    expect(copy).toEqual(server);
  });

  it("write every built-in template with its kind's sample", () => {
    for (const k of templateFixture.kinds)
      for (const [channel, byLocale] of Object.entries(k.defaults))
        for (const [locale, parts] of Object.entries(byLocale)) {
          const kVars = k.variables.map((v) => v.name);
          const kLists = k.variables.filter((v) => v.type === "list of text").map((v) => v.name);
          const values = k.sample[locale as "en" | "fr"];
          for (const src of [parts.subject, parts.text, parts.html].filter(Boolean) as string[]) {
            const nodes = parse(src);
            expect(() => checkFields(nodes, kVars, kLists), `${k.kind} ${channel} ${locale}`).not.toThrow();
            expect(execute(nodes, values)).not.toContain("<no value>");
          }
        }
  });

  it("write the sample as Go does", () => {
    const fr = approval.sample.fr;
    expect(execute(parse(approval.defaults.email.en.subject), fr)).toBe(
      "Approval needed: RHEL 9 server web-prd-front01 (REQ-10077)",
    );
    expect(execute(parse("{{range $i, $r := .Approval.Reasons}}{{if $i}}, {{end}}{{$r}}{{end}}"), fr)).toBe(
      "Production VMs need the infrastructure leads' approval, 8 vCPU is above the project's size for prd",
    );
    expect(execute(parse("{{with .Request}}{{.Number}}{{end}} {{len .Approval.Reasons}}"), fr)).toBe("REQ-10077 2");
    expect(execute(parse('{{printf "%s!" .Request.Number | html}}'), fr)).toBe("REQ-10077!");
    expect(
      execute(parse("<b>{{.Request.Title}}</b>"), { ".Request.Title": "<i>x</i>" }, (s) => s.replace("<", "&lt;")),
    ).toBe("<b>&lt;i>x</i></b>");
  });

  it("refuse what the server refuses", () => {
    expect(problemOf("{{.Request.Number")).toMatch(/^line 1: /);
    expect(problemOf("{{if .Request.Number}}x")).toMatch(/^line 1: unexpected EOF/);
    expect(problemOf("{{.Deployment.Name}}")).toBe("line 1: .Deployment is not a variable of this kind");
    expect(problemOf("{{.Request.Owner}}")).toBe(
      "line 1: .Request has no field Owner: .Request.Owner is not a variable of this kind",
    );
    expect(problemOf("{{range .Request.Number}}{{end}}")).toMatch(/range goes over a list variable/);
    expect(problemOf("{{range .Approval.Reasons}}{{.Name}}{{end}}")).toBe(
      "line 1: the items of .Approval.Reasons are text: they have no field Name",
    );
    expect(problemOf("{{nope .Request.Number}}")).toBe('line 1: function "nope" not defined');
    expect(problemOf('{{define "x"}}{{end}}')).toMatch(/can't define others/);
    expect(problemOf("{{$.Request.Number}} {{$n := .Request.Title}}{{$n}}")).toBe("");
  });
});
