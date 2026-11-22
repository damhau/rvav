// What the server answers a write whose body it decodes strictly
// (httpapi.decodeStrict): a field the operation doesn't take is refused.
import { HttpResponse } from "msw";

/** The server's 400 when the body names a field outside `takes`, or nothing. */
export function unknownField(body: unknown, takes: readonly string[]) {
  if (typeof body !== "object" || body === null || Array.isArray(body)) return undefined;
  const extra = Object.keys(body).find((k) => !takes.includes(k));
  if (extra === undefined) return undefined;
  return HttpResponse.json(
    {
      type: "https://rvav.dev/problems/bad-request",
      title: "The request body has a field this operation doesn't take",
      status: 400,
      detail: `json: unknown field "${extra}"`,
      request_id: "req_mock",
    },
    { status: 400, headers: { "Content-Type": "application/problem+json" } },
  );
}
