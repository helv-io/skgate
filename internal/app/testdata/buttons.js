// Button columns in a real browser (optional; see layout_test.go). Usage: node buttons.js <pagesDir> <chrome> <puppeteer-core dir>
// In every table, the buttons of a row's action cell have one width and the same left edge as the same
// position in every other row, at several window widths.
const [dir, chrome, pp] = process.argv.slice(2);
const puppeteer = require(pp);
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  let bad = 0;
  for (const file of ["upstreams", "keys", "clients"]) {
    for (const width of [1280, 900, 420]) {
      const pg = await b.newPage(); await pg.setViewport({ width, height: 900 });
      await pg.goto("file://" + dir + "/" + file + ".html");
      const res = await pg.evaluate(() => {
        const t = document.querySelector("main .table");
        const rows = [...t.tBodies[0].rows].map(r => [...r.querySelectorAll(".actions .act")].map(a => a.getBoundingClientRect())).filter(r => r.length);
        const out = [];
        if (rows.length < 2) out.push("need two rows, got " + rows.length);
        const w = rows[0].map(r => Math.round(r.width));
        rows.forEach((r, i) => r.forEach((x, j) => {
          if (Math.abs(Math.round(x.left) - Math.round(rows[0][j].left)) > 1 && r.length === rows[0].length) out.push("row " + i + " button " + j + " left " + Math.round(x.left) + " vs " + Math.round(rows[0][j].left));
          if (r.length === rows[0].length && Math.round(x.width) !== w[j]) out.push("row " + i + " button " + j + " width " + Math.round(x.width) + " vs " + w[j]);
          if (Math.round(x.width) < 70) out.push("row " + i + " button " + j + " narrower than the shared minimum: " + Math.round(x.width));
        }));
        return out;
      });
      if (res.length) { bad++; console.log("FAIL", file, width, JSON.stringify(res)); }
      await pg.close();
    }
  }
  await b.close();
  console.log(bad ? "FAILED " + bad : "ALL OK"); process.exit(bad ? 1 : 0);
})();
