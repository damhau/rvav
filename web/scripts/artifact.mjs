// Builds dist-mockup/rvav-mockup.html: one self-contained page (inline CSS + JS)
// in the shape the claude.ai Artifact host expects (no html/head/body tags).
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const dir = "dist-mockup/assets";
const files = readdirSync(dir);
const read = (ext) =>
  files.filter((f) => f.endsWith(ext)).map((f) => readFileSync(join(dir, f), "utf8")).join("\n");

const css = read(".css");
const js = read(".js").replaceAll("</script", "<\\/script");

const html = `<title>rvav Console</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;500&family=IBM+Plex+Sans:wght@400;500;600&display=swap">
<style>${css}</style>
<div id="root"></div>
<script type="module">${js}</script>
`;
writeFileSync("dist-mockup/rvav-mockup.html", html);
console.log(`dist-mockup/rvav-mockup.html ${(html.length / 1024).toFixed(0)} kB`);
