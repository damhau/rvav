import createClient, { type Middleware } from "openapi-fetch";
import { getAccessToken, handleUnauthorized } from "@/lib/auth";
import type { paths } from "./schema";
import type { FieldError, Problem } from "./types";

/** A failed API call, carrying the server's RFC 9457 problem details. */
export class ApiError extends Error {
  readonly status: number;
  readonly problem: Problem;

  constructor(status: number, problem: Problem) {
    super(problem.detail ?? problem.title);
    this.name = "ApiError";
    this.status = status;
    this.problem = problem;
  }

  get requestId(): string {
    return this.problem.request_id;
  }

  get fieldErrors(): FieldError[] {
    return this.problem.errors ?? [];
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

type Transport = (request: Request) => Promise<Response>;
let transport: Transport = (request) => globalThis.fetch(request);

/** Swap how requests are sent: the in-process mock transport uses this. */
export function setTransport(t: Transport) {
  transport = t;
}

/** Send a raw request through the same transport and auth as the typed client (used by the event stream). */
export async function rawFetch(request: Request): Promise<Response> {
  const token = await getAccessToken();
  if (token) request.headers.set("Authorization", `Bearer ${token}`);
  return transport(request);
}

export function apiUrl(path: string) {
  return `${globalThis.location.origin}/api/v1${path}`;
}

const auth: Middleware = {
  async onRequest({ request }) {
    const token = await getAccessToken();
    if (token) request.headers.set("Authorization", `Bearer ${token}`);
    return request;
  },
};

export const api = createClient<paths>({
  baseUrl: apiUrl(""),
  fetch: (request: Request) => transport(request),
  // The spec uses style: form, explode: false for list filters: status=running,failed
  querySerializer: { array: { style: "form", explode: false } },
});
api.use(auth);

function asProblem(response: Response, body: unknown): Problem {
  if (body && typeof body === "object" && "title" in body && "status" in body) {
    const p = body as Problem;
    return { ...p, request_id: p.request_id || response.headers.get("X-Request-Id") || "unknown" };
  }
  return {
    type: "about:blank",
    title: response.statusText || "The request failed",
    status: response.status,
    request_id: response.headers.get("X-Request-Id") ?? "unknown",
  };
}

interface FetchResult<T> {
  data?: T;
  error?: unknown;
  response: Response;
}

/**
 * Await an openapi-fetch call and return its data, or throw an ApiError.
 * Network failures become an ApiError with status 0.
 */
export async function call<T>(promise: Promise<FetchResult<T>>): Promise<T> {
  let result: FetchResult<T>;
  try {
    result = await promise;
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") throw e;
    throw new ApiError(0, {
      type: "about:blank",
      title: "Can't reach the rvav API",
      detail: "Check your connection and try again.",
      status: 0,
      request_id: "none",
    });
  }
  const { data, error, response } = result;
  if (!response.ok || error !== undefined) {
    if (response.status === 401) await handleUnauthorized();
    throw new ApiError(response.status, asProblem(response, error));
  }
  return data as T;
}
