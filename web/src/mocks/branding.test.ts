// The mock's branding against the server's checks (internal/brand,
// internal/core/branding.go).
import { describe, expect, it } from "vitest";
import { apiUrl, rawFetch } from "@/lib/api/client";

const base = apiUrl("");
const send = (url: string, init?: RequestInit) => rawFetch(new Request(url, init));

async function upload(variant: string, body: BodyInit) {
  const r = await send(`${base}/admin/branding/logos/${variant}`, { method: "PUT", body });
  return { status: r.status, message: (await r.json()) as { errors?: { message: string }[]; detail?: string } };
}

const refusal = async (body: BodyInit) => {
  const { status, message } = await upload("light", body);
  return [status, message.errors?.[0]?.message ?? message.detail];
};

const svg = (inner: string, attrs = "") => `<svg xmlns="http://www.w3.org/2000/svg"${attrs}>${inner}</svg>`;

function jpeg(width: number, height: number) {
  const b = new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0, 4, 0, 0, 0xff, 0xc0, 0, 11, 8, 0, 0, 0, 0, 1, 1, 0x11, 0]);
  new DataView(b.buffer).setUint16(13, height);
  new DataView(b.buffer).setUint16(15, width);
  return b;
}

describe("the mock's logos", () => {
  it("take a drawing with shapes, text, paint and its own references", async () => {
    const drawing = svg(
      '<defs><linearGradient id="g"><stop offset="0" stop-color="#fff"/></linearGradient></defs>' +
        '<rect width="10" height="10" fill="url(#g)"/><use xlink:href="#g"/><text>Corp</text>' +
        '<image href="data:image/png;base64,iVBORw0KGgo="/>',
      ' xmlns:xlink="http://www.w3.org/1999/xlink" xmlns:inkscape="http://www.inkscape.org/namespaces/inkscape" inkscape:version="1.3"',
    );
    expect((await upload("light", drawing)).status).toBe(200);
    expect((await upload("dark", jpeg(640, 200))).status).toBe(200);
  });

  it("refuse what isn't on the server's allow-list", async () => {
    expect(await refusal(svg('<animate attributeName="href" to="javascript:alert(1)"/>'))).toEqual([
      422,
      "The SVG has a <animate> element, which a logo can't have.",
    ]);
    expect(await refusal(svg('<iframe src="https://x.example"/>'))).toEqual([
      422,
      "The SVG has a <iframe> element, which a logo can't have.",
    ]);
    expect(await refusal(svg('<image href="data:image/svg+xml;base64,PHN2Zy8+"/>'))).toEqual([
      422,
      'The SVG links to "data:image/svg+xml;base64,PHN2Zy8+": a logo can only refer to its own parts, and embed PNG, JPEG, GIF or WebP pictures.',
    ]);
    expect(await refusal(svg('<rect onclick="x()"/>'))).toEqual([422, "The SVG has an event handler (onclick)."]);
    expect(await refusal(svg('<rect fill="url(https://x.example/a)"/>'))).toEqual([
      422,
      "The SVG has a fill attribute that fetches or runs something.",
    ]);
    const fetches = [422, "The SVG has a style that fetches or runs something."];
    expect(await refusal(svg("<style>@import 'https://x.example/a.css';</style>"))).toEqual(fetches);
    expect(await refusal(svg('<style>@imp<!---->ort "https://x.example/a.css";</style>'))).toEqual(fetches);
    expect(await refusal(svg('<style>svg{background-image:image-set("https://x.example/p.png" 1x)}</style>'))).toEqual(fetches); // prettier-ignore
    expect(await refusal(svg('<style>@font-face{font-family:x;src:src("https://x.example/f.woff")}</style>'))).toEqual(fetches); // prettier-ignore
    expect(await refusal(svg('<style><g/>@import "https://x.example/a.css";</style>'))).toEqual([
      422,
      "The SVG has a <g> element inside its <style>.",
    ]);
    expect(await refusal(svg("<style>svg{background:u\\72l(https://x.example/p.png)}</style>"))).toEqual([
      422,
      "The SVG has a CSS escape in its style, which could spell what it fetches.",
    ]);
    expect(await refusal(svg('<rect style="fill:u\\72l(https://x.example/p#a)"/>'))).toEqual([
      422,
      "The SVG has a CSS escape in its style attribute, which could spell what it fetches.",
    ]);
    expect(await refusal('<!DOCTYPE svg [<!ENTITY x "y">]>' + svg("<text>&x;</text>"))).toEqual([
      422,
      "The SVG has a DOCTYPE or an entity: export it again as plain SVG.",
    ]);
    expect(await refusal("<html><body></body></html>")).toEqual([415, "A logo is a PNG, JPEG or SVG picture."]);
  });

  it("read a JPEG's size, and refuse one that can't be read", async () => {
    expect(await refusal(jpeg(4096, 100))).toEqual([
      422,
      "The picture is 4096×100 pixels: a logo is 2048 pixels wide and tall at most.",
    ]);
    expect(await refusal(new Uint8Array([0xff, 0xd8, 0xff, 0xe0, 0, 4]))).toEqual([
      422,
      "The picture can't be read: unexpected EOF.",
    ]);
  });

  it("are for the light or the dark theme", async () => {
    expect((await upload("sepia", svg("<rect/>"))).status).toBe(400);
    expect((await send(`${base}/branding/logos/constructor`)).status).toBe(404);
    expect((await send(`${base}/admin/branding/logos/sepia`, { method: "DELETE" })).status).toBe(400);
  });
});

describe("the mock's branding", () => {
  const put = (body: unknown) =>
    send(`${base}/admin/branding`, { method: "PUT", body: JSON.stringify(body) }).then(async (r) => ({
      status: r.status,
      body: (await r.json()) as { errors?: { field: string; code: string }[]; detail?: string; name?: string },
    }));

  it("refuses a field it doesn't take, as the server decodes strictly", async () => {
    const r = await put({ name: "Corp", acent: "#5b2a86" });
    expect(r.status).toBe(400);
    expect(r.body.detail).toBe('json: unknown field "acent"');
    const check = await send(`${base}/admin/branding/accent-check`, {
      method: "POST",
      body: JSON.stringify({ accent: "#5b2a86", theme: "dark" }),
    });
    expect(check.status).toBe(400);
  });

  it("says a malformed accent is invalid, and one that doesn't read is a contrast problem", async () => {
    expect((await put({ accent: "purple" })).body.errors).toEqual([expect.objectContaining({ field: "/accent", code: "invalid" })]); // prettier-ignore
    expect((await put({ accent: "#ffff00" })).body.errors).toEqual([expect.objectContaining({ field: "/accent", code: "contrast" })]); // prettier-ignore
  });

  it("collapses the name's spaces and refuses another control character", async () => {
    const ok = await put({ name: " Corp\t portal\n" });
    expect(ok.status).toBe(200);
    expect(ok.body.name).toBe("Corp portal");
    expect((await put({ name: "Corp\u0007" })).body.errors).toEqual([expect.objectContaining({ field: "/name", code: "invalid" })]); // prettier-ignore
  });
});
