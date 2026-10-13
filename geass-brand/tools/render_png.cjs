// Rasterise the brand SVGs that need PNG companions.
// Needs @resvg/resvg-js (not a project dependency): install it in a scratch directory and run
//   NODE_PATH=<scratch>/node_modules node geass-brand/tools/render_png.cjs
// SVG text is outlined by build_brand.py, so no fonts are required.
const fs = require("node:fs");
const path = require("node:path");
const { Resvg } = require("@resvg/resvg-js");

const brand = path.resolve(__dirname, "..");
const jobs = [
  ["icons/geass-app-icon.svg", "icons/geass-app-icon-512.png", 512],
  ["icons/favicon.svg", "icons/favicon-32.png", 32],
  ["social/geass-social-card.svg", "social/geass-social-card.png", 1200],
  ["preview/geass-brand-board.svg", "preview/geass-brand-board.png", 1440],
];

for (const [src, dst, width] of jobs) {
  const svg = fs.readFileSync(path.join(brand, src));
  const png = new Resvg(svg, { fitTo: { mode: "width", value: width } }).render().asPng();
  fs.writeFileSync(path.join(brand, dst), png);
  console.log("rendered", dst, `${png.length} bytes`);
}
