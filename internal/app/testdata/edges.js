// Right-edge check in a real browser (optional; see layout_test.go). Usage: node edges.js <pagesDir> <chrome> <puppeteer-core dir>
// Each page is one saved admin page (css next to it). At several widths, every visible input, select,
// textarea, pairs row and button row of a form must end at the right edge of the form column, which every top-level field fills.
const [dir, chrome, pp] = process.argv.slice(2);
const puppeteer = require(pp);
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  let bad = 0;
  for (const file of ["upstreams", "edit_managed", "edit_remote", "clients", "import"]) {
    for (const width of [1280, 700, 420]) {
      const pg = await b.newPage(); await pg.setViewport({ width, height: 1400 });
      await pg.goto("file://" + dir + "/" + file + ".html");
      await pg.evaluate(() => document.querySelectorAll("form.form details").forEach(d => d.open = true));
      const res = await pg.evaluate(() => {
        const f = document.querySelector("main form.form"); if (!f) return { err: "no form.form" };
        const vis = e => e.getBoundingClientRect().width > 0;
        const ctl = [...f.querySelectorAll("input:not([type=hidden]):not([type=checkbox]),select,textarea")].filter(vis);
        const edge = Math.round(f.getBoundingClientRect().right); // the form column; a top-level field fills it
        const top = [...f.querySelectorAll(".field>input,.field>select,.field>textarea")].filter(vis).find(e => !e.closest(".group,details,.pairs"));
        if (top && Math.abs(Math.round(top.getBoundingClientRect().right) - edge) > 1) return { edge, n: ctl.length, bad: ["top-level field " + top.name + " does not fill the column"] };
        const bad = [];
        // a control ends at the edge, or shares its row/pair with siblings that do
        for (const e of f.querySelectorAll(".field,.group,details,.pairs,.pair,.row,input:not([type=hidden]):not([type=checkbox]),select,textarea")) {
          if (!vis(e) || e.closest("template")) continue;
          const r = Math.round(e.getBoundingClientRect().right);
          if (e.matches("input,select,textarea") && e.parentElement.matches(".row,.pair")) continue; // checked via the parent
          if (e.matches(".row") && e.closest(".field")) continue;
          if (Math.abs(r - edge) > 1) bad.push((e.tagName + "." + e.className + "[" + (e.name || "") + "] " + r + " != " + edge));
        }
        return { edge, n: ctl.length, bad };
      });
      if (res.err || res.bad.length) { bad++; console.log("FAIL", file, width, JSON.stringify(res)); }
      await pg.close();
    }
  }
  await b.close();
  console.log(bad ? "FAILED " + bad : "ALL OK"); process.exit(bad ? 1 : 0);
})();
