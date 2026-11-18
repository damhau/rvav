// What a component reads, as the server's parsers find it
// (internal/blueprint/references.go): code, not comments or string literals.
import { describe, expect, it } from "vitest";
import { celCode, dependsOn, templateCode } from "./dependsOn";

const blueprint = (web: Record<string, unknown>) => ({
  kind: "vm",
  components: [{ key: "db" }, { key: "cache" }, { key: "web", ...web }],
});

const userData = (text: string) => dependsOn(blueprint({ guest: { cloud_init: { user_data: text } } })).get("web");
const tags = (expr: string) => dependsOn(blueprint({ bindings: { tags: expr } })).get("web");

describe("the components a component is built after", () => {
  it("are those its depends_on names and its user data and tags read", () => {
    expect(userData("#cloud-config\nwrite_files:\n  - content: {{ .components.db.fqdn | json }}\n")).toEqual(["db"]);
    expect(userData("{{ range $.components.cache.addresses }}{{ . }}{{ end }}")).toEqual(["cache"]);
    expect(tags("{'db': components.db.names[0]}")).toEqual(["db"]);
    expect(dependsOn(blueprint({ depends_on: ["cache"] })).get("web")).toEqual(["cache"]);
  });

  it("leave out what user data says outside its actions, in comments and in strings", () => {
    expect(userData("#cloud-config\n# needs .components.db first\nruncmd: [echo]\n")).toEqual([]);
    expect(userData("{{/* .components.db once it is up */}}{{- /* .components.cache */ -}}")).toEqual([]);
    expect(userData('{{ printf "%s" ".components.db" }}{{ `.components.cache` }}')).toEqual([]);
    expect(userData('{{ "}}" }} {{ .components.cache.fqdn }}')).toEqual(["cache"]);
    // A variable other than $ is not the template's data.
    expect(userData("{{ $x := . }}{{ $x.components.db.fqdn }}")).toEqual([]);
  });

  it("leave out what CEL says in comments and string literals", () => {
    expect(tags("{'role': 'components.db'} // components.cache later")).toEqual([]);
    // A raw string's backslash escapes nothing.
    expect(tags(`{"a": r"C:\\" + components.db.names[0]}`)).toEqual(["db"]);
    expect(tags(`{"a": "\\"" + components.db.names[0]}`)).toEqual(["db"]);
    expect(tags("{'''x ' components.db''': components.cache.names[0]}")).toEqual(["cache"]);
  });
});

describe("the code of", () => {
  it("CEL keeps everything but literals and comments", () => {
    expect(celCode("a + 'b' // c\n+ d")).toBe("a +   \n+ d");
  });

  it("a template keeps its actions only", () => {
    expect(templateCode("x {{ .a }} y {{/* z */}} {{ .b }}")).toBe(" .a \n .b \n");
  });
});
