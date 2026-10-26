import { describe, expect, it, vi } from "vitest";
import type { LiveEvent } from "@/lib/api/types";
import { createSseParser, LiveClient, type SseMessage } from "./sse";

const heartbeat = (id: string): LiveEvent => ({ id, type: "heartbeat", at: "2026-10-26T14:00:00Z" });

function stream(chunks: string[], keepOpen = false) {
  const enc = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    start(c) {
      chunks.forEach((ch) => c.enqueue(enc.encode(ch)));
      if (!keepOpen) c.close();
    },
  });
}

describe("createSseParser", () => {
  it("parses messages split across chunks, with comments and multi-line data", () => {
    const got: SseMessage[] = [];
    const feed = createSseParser((m) => got.push(m));
    feed(": connected\n\nid: e1\nevent: heart");
    feed('beat\ndata: {"a":\ndata: 1}\n\nid: e2\r\ndata: x\r\n\r\n');
    expect(got).toEqual([
      { id: "e1", event: "heartbeat", data: '{"a":\n1}' },
      { id: "e2", event: undefined, data: "x" },
    ]);
  });
});

describe("LiveClient", () => {
  it("delivers events and resumes with Last-Event-ID after the stream drops", async () => {
    const seen: string[] = [];
    const requests: Request[] = [];
    let call = 0;
    const fetchFn = vi.fn((req: Request) => {
      requests.push(req);
      call += 1;
      const body =
        call === 1
          ? stream([`id: evt_1\ndata: ${JSON.stringify(heartbeat("evt_1"))}\n\n`])
          : stream([`id: evt_2\ndata: ${JSON.stringify(heartbeat("evt_2"))}\n\n`], true);
      return Promise.resolve(new Response(body, { headers: { "Content-Type": "text/event-stream" } }));
    });
    const statuses: string[] = [];
    const client = new LiveClient({
      url: () => "http://localhost/api/v1/events?topics=requests",
      fetch: fetchFn,
      onEvent: (e) => seen.push(e.id),
      onStatus: (s) => statuses.push(s),
      retryDelaysMs: [5],
    });
    client.start();
    await vi.waitFor(() => expect(seen).toEqual(["evt_1", "evt_2"]));
    expect(requests[0]!.headers.get("Last-Event-ID")).toBeNull();
    expect(requests[1]!.headers.get("Last-Event-ID")).toBe("evt_1");
    expect(statuses).toContain("reconnecting");
    client.stop();
    expect(statuses.at(-1)).toBe("idle");
  });

  it("keeps retrying when the server answers with an error", async () => {
    const fetchFn = vi
      .fn<(r: Request) => Promise<Response>>()
      .mockResolvedValueOnce(new Response("down", { status: 503 }))
      .mockResolvedValue(new Response(stream([], true), { headers: { "Content-Type": "text/event-stream" } }));
    const statuses: string[] = [];
    const client = new LiveClient({
      url: () => "http://localhost/api/v1/events?topics=requests",
      fetch: fetchFn,
      onEvent: () => {},
      onStatus: (s) => statuses.push(s),
      retryDelaysMs: [5],
    });
    client.start();
    await vi.waitFor(() => expect(statuses).toContain("open"));
    expect(fetchFn).toHaveBeenCalledTimes(2);
    client.stop();
  });
});
