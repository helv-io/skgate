// Builds ../../internal/admin/static/codemirror.js and ../../THIRD_PARTY_NOTICES.md. Run: npm ci && npm run build
const esbuild = require("esbuild"), fs = require("fs"), path = require("path");
const root = path.resolve(__dirname, "../..");
esbuild.build({
  entryPoints: [path.join(__dirname, "entry.js")], bundle: true, minify: true, format: "iife", target: "es2020", legalComments: "none",
  outfile: path.join(root, "internal/admin/static/codemirror.js"), metafile: true, logLevel: "info",
}).then((r) => {
  // every package that ended up in the bundle, with its licence text
  const pkgs = new Map();
  for (const f of Object.keys(r.metafile.inputs)) {
    const m = /node_modules\/((?:@[^/]+\/)?[^/]+)\//.exec(f);
    if (!m || pkgs.has(m[1])) continue;
    const dir = path.join(__dirname, "node_modules", m[1]), pj = JSON.parse(fs.readFileSync(path.join(dir, "package.json"), "utf8"));
    const file = fs.readdirSync(dir).find((n) => /^licen[cs]e(\.md|\.txt)?$/i.test(n));
    pkgs.set(m[1], { version: pj.version, license: pj.license, text: file ? fs.readFileSync(path.join(dir, file), "utf8").trim() : "" });
  }
  let out = "# Third-party notices\n\nThe admin editor (`internal/admin/static/codemirror.js`) is a bundle of the packages below, built by `tools/codemirror` (see docs/development.md, \"Code box\"). Their licences require this notice to travel with the binary. This file is written by `npm run build`; do not edit it by hand.\n\n";
  out += "| Package | Version | Licence |\n|---|---|---|\n";
  for (const [n, p] of [...pkgs].sort()) out += `| ${n} | ${p.version} | ${p.license} |\n`;
  for (const [n, p] of [...pkgs].sort()) out += `\n## ${n} ${p.version} (${p.license})\n\n${p.text ? p.text.split("\n").map((l) => (l ? "    " + l : "")).join("\n") : "    (the package ships no licence file; it declares " + p.license + ")"}\n`;
  fs.writeFileSync(path.join(root, "THIRD_PARTY_NOTICES.md"), out);
});
