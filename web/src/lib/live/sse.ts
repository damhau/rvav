// Server-Sent Events over fetch, so the stream can carry the Authorization
// header (EventSource can't). Reconnects with backoff and resumes with
// Last-Event-ID.
import type { LiveEvent } from "@/lib/api/types";

export interface SseMessage {
  id?: string;
  event?: string;
  data: string;
}

/** Incremental SSE parser: feed it text as it arrives. */
export function createSseParser(onMessage: (m: SseMessage) => void) {
  let buffer = "";
  let id: string | undefined;
  let event: string | undefined;
  let data: string[] = [];

  const dispatch = () => {
    if (data.length) onMessage({ id, event, data: data.join("\n") });
    event = undefined;
    data = [];
  };

  return (chunk: string) => {
    buffer += chunk;
    const lines = buffer.split(/\r\n|\r|\n/);
    buffer = lines.pop() ?? "";
    for (const line of lines) {
      if (line === "") {
        dispatch();
        continue;
      }
      if (line.startsWith(":")) continue;
      const i = line.indexOf(":");
      const field = i === -1 ? line : line.slice(0, i);
      let value = i === -1 ? "" : line.slice(i + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "data") data.push(value);
      else if (field === "event") event = value;
      else if (field === "id") id = value;
    }
  };
}

export type LiveStatus = "idle" | "connecting" | "open" | "reconnecting";

export interface LiveClientOptions {
  url: () => string;
  fetch: (request: Request) => Promise<Response>;
  onEvent: (event: LiveEvent) => void;
  onStatus?: (status: LiveStatus) => void;
  retryDelaysMs?: number[];
}

export class LiveClient {
  private lastId: string | null = null;
  private controller: AbortController | null = null;
  private retryTimer: ReturnType<typeof setTimeout> | undefined;
  private attempt = 0;
  private running = false;
  private readonly opts: LiveClientOptions;

  constructor(opts: LiveClientOptions) {
    this.opts = opts;
  }

  get lastEventId() {
    return this.lastId;
  }

  start() {
    if (this.running) return;
    this.running = true;
    void this.connect();
  }

  stop() {
    this.running = false;
    clearTimeout(this.retryTimer);
    this.controller?.abort();
    this.controller = null;
    this.opts.onStatus?.("idle");
  }

  private status(s: LiveStatus) {
    this.opts.onStatus?.(s);
  }

  private async connect() {
    const controller = new AbortController();
    this.controller = controller;
    this.status(this.attempt === 0 ? "connecting" : "reconnecting");
    const headers = new Headers({ Accept: "text/event-stream" });
    if (this.lastId) headers.set("Last-Event-ID", this.lastId);
    try {
      const response = await this.opts.fetch(new Request(this.opts.url(), { headers, signal: controller.signal }));
      if (!response.ok || !response.body) throw new Error(`Event stream answered ${response.status}`);
      this.attempt = 0;
      this.status("open");
      const reader = response.body.getReader();
      const decoder = new TextDecoder();
      const feed = createSseParser((m) => this.handle(m));
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        feed(decoder.decode(value, { stream: true }));
      }
      throw new Error("Event stream closed");
    } catch {
      if (!this.running || controller.signal.aborted) return;
      const delays = this.opts.retryDelaysMs ?? [1000, 2000, 5000, 10000];
      const wait = delays[Math.min(this.attempt, delays.length - 1)] ?? 10000;
      this.attempt += 1;
      this.status("reconnecting");
      this.retryTimer = setTimeout(() => void this.connect(), wait);
    }
  }

  private handle(m: SseMessage) {
    let event: LiveEvent;
    try {
      event = JSON.parse(m.data) as LiveEvent;
    } catch {
      return;
    }
    this.lastId = m.id ?? event.id ?? this.lastId;
    this.opts.onEvent(event);
  }
}
