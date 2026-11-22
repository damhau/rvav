import { afterEach, describe, expect, it } from "vitest";
import { i18n } from "@/lib/i18n";
import { ApiError } from "./client";
import { problemText } from "./problem";

const problem = (status: number, title: string, detail?: string) =>
  new ApiError(status, { type: "about:blank", title, status, detail, request_id: "req_1" });

afterEach(async () => {
  await i18n.changeLanguage("en");
});

describe("a problem's words", () => {
  it("words a 404 and a 403 that say nothing more in the language shown", async () => {
    await i18n.changeLanguage("fr");
    expect(problemText(problem(404, "Not found", "It does not exist, or you cannot see it."))).toEqual({
      title: { text: "Introuvable" },
      detail: { text: "Cela n'existe pas, ou vous ne pouvez pas le voir." },
    });
    expect(problemText(problem(404, "Not found", "Not found"))).toEqual({
      title: { text: "Introuvable" },
      detail: { text: "Cela n'existe pas, ou vous ne pouvez pas le voir." },
    });
    expect(problemText(problem(403, "Forbidden"))).toEqual({
      title: { text: "Non autorisé" },
      detail: { text: "Vous n'avez pas la permission de faire cela." },
    });
  });

  it("keeps the server's detail when it says more, marked as English", async () => {
    await i18n.changeLanguage("fr");
    expect(problemText(problem(403, "Not allowed", "Only the project's administrators can do this."))).toEqual({
      title: { text: "Non autorisé" },
      detail: { text: "Only the project's administrators can do this.", lang: "en" },
    });
    expect(problemText(problem(404, "Deployment not found", "It was deleted."))).toEqual({
      title: { text: "Deployment not found", lang: "en" },
      detail: { text: "It was deleted.", lang: "en" },
    });
    expect(problemText(problem(409, "Not possible right now", "It is being deleted."))).toEqual({
      title: { text: "Not possible right now", lang: "en" },
      detail: { text: "It is being deleted.", lang: "en" },
    });
  });

  it("reads as the server's in English", () => {
    expect(problemText(problem(404, "Not found", "Not found"))).toEqual({
      title: { text: "Not found" },
      detail: { text: "It does not exist, or you cannot see it." },
    });
  });
});
