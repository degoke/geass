// Builds the raster icons browsers and phones ask for from the brand SVGs:
//   public/favicon.ico            16, 32 and 48 px frames (browsers request /favicon.ico by default)
//   public/apple-touch-icon.png   180 px, opaque (iOS fills transparency with black anyway)
// Run it after the brand art changes: `pnpm icons`. The SVG favicon (public/favicon.svg) is copied by hand from
// geass-brand/icons/favicon.svg, and the output is committed, so a normal build never needs this script.
import { readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const here = (p) => fileURLToPath(new URL(p, import.meta.url));
const favicon = await readFile(here("../public/favicon.svg"));
const appIcon = await readFile(here("../../geass-brand/icons/geass-app-icon.svg"));

const render = (svg, size) => sharp(svg, { density: (72 * size) / 32 * 4 }).resize(size, size).png().toBuffer();

// An ICO is a 6-byte header, one 16-byte directory entry per image, then the images. PNG frames are allowed.
function ico(frames) {
  const header = Buffer.alloc(6);
  header.writeUInt16LE(1, 2); // type: icon
  header.writeUInt16LE(frames.length, 4);
  let offset = 6 + 16 * frames.length;
  const entries = frames.map(({ size, png }) => {
    const entry = Buffer.alloc(16);
    entry.writeUInt8(size >= 256 ? 0 : size, 0); // width
    entry.writeUInt8(size >= 256 ? 0 : size, 1); // height
    entry.writeUInt16LE(1, 4); // colour planes
    entry.writeUInt16LE(32, 6); // bits per pixel
    entry.writeUInt32LE(png.length, 8);
    entry.writeUInt32LE(offset, 12);
    offset += png.length;
    return entry;
  });
  return Buffer.concat([header, ...entries, ...frames.map((f) => f.png)]);
}

const frames = await Promise.all([16, 32, 48].map(async (size) => ({ size, png: await render(favicon, size) })));
await writeFile(here("../public/favicon.ico"), ico(frames));

const touch = await sharp(appIcon, { density: 576 })
  .resize(180, 180)
  .flatten({ background: "#000000" })
  .png()
  .toBuffer();
await writeFile(here("../public/apple-touch-icon.png"), touch);

console.log("Wrote public/favicon.ico (16, 32, 48) and public/apple-touch-icon.png (180)");
