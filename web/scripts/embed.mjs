// Copies the production build (web/dist) into the Go server's embed directory
// (server/internal/ui/dist), so the next `go build` embeds the real UI.
//
//   npm run build && npm run embed
//
// The directory keeps its .gitkeep/.gitignore; everything else in it is build
// output and is replaced. Nothing here is committed. Run `npm run embed -- --clean`
// to empty it again (the binary then serves the placeholder page).
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const source = resolve(here, "../dist");
const target = resolve(here, "../../server/internal/ui/dist");
const keep = new Set([".gitkeep", ".gitignore"]);

mkdirSync(target, { recursive: true });
for (const name of readdirSync(target)) {
  if (!keep.has(name)) rmSync(join(target, name), { recursive: true, force: true });
}

if (process.argv.includes("--clean")) {
  console.log(`emptied ${target}`);
  process.exit(0);
}

if (!existsSync(join(source, "index.html"))) {
  console.error(`No build in ${source}: run \`npm run build\` first.`);
  process.exit(1);
}

cpSync(source, target, { recursive: true });
console.log(`copied ${source} -> ${target}`);
