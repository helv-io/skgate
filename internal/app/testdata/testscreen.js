const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  for (const w of [390, 1280]) {
    await pg.setViewport({ width: w, height: 900 });
    await pg.goto(url + "/admin/upstreams/fake/test");
    const info = await pg.evaluate(() => {
      const top = e => e.getBoundingClientRect().top;
      const pill = document.querySelector(".pill.ok"), table = document.querySelector("#tools-table");
      const rows = [...table.querySelectorAll("tr[data-filter-row]")];
      const sums = rows.map(r => r.querySelector("summary")).filter(Boolean);
      const extras = [...document.querySelectorAll("a")].map(a => a.textContent.trim()).filter(t => t === "Test again" || t === "Back" || t === "Process");
      return { pill: pill && pill.textContent, order: !!(pill && table && top(pill) < top(table)), extras, rows: rows.length,
        oneLine: sums.every(s => s.getBoundingClientRect().height < 50 && getComputedStyle(s).whiteSpace === "nowrap"), overflowing: sums.some(s => s.scrollWidth > s.clientWidth) };
    });
    ok(/^OK · 3 tools · \d+(\.\d)? m?s$/.test(info.pill) && info.order && info.rows === 3 && info.extras.length === 0, w + ": summary, then tools, no Back or Test again " + JSON.stringify(info));
    ok(info.oneLine && (w > 400 || info.overflowing), w + ": descriptions are one line, the long one is clipped " + JSON.stringify(info));
    // opening a description shows all of it
    const h = await pg.evaluate(() => { const d = document.querySelector("details.desc.long") || [...document.querySelectorAll("details.desc")].find(x => x.textContent.includes("sentence")); const before = d.getBoundingClientRect().height; d.open = true; return { before, after: d.getBoundingClientRect().height, more: d.querySelector("p") && d.querySelector("p").getBoundingClientRect().height > 0 }; });
    ok(h.after > h.before && h.more, w + ": opening shows the whole description " + JSON.stringify(h));
    // filter
    await pg.type("input[data-filter]", "ALPHA");
    let f = await pg.evaluate(() => ({ shown: [...document.querySelectorAll("#tools-table tr[data-filter-row]")].filter(r => !r.hidden && r.offsetParent !== null).map(r => r.querySelector("code").textContent), count: document.querySelector("[data-filter-count]").textContent, empty: document.querySelector("tr[data-filter-empty]").hidden }));
    ok(f.shown.join() === "alpha_tool" && f.count === "1 of 3" && f.empty, w + ": filter by name, case-insensitive " + JSON.stringify(f));
    await pg.$eval("input[data-filter]", e => { e.value = ""; });
    await pg.type("input[data-filter]", "kangaroo");
    f = await pg.evaluate(() => ({ shown: [...document.querySelectorAll("#tools-table tr[data-filter-row]")].filter(r => !r.hidden && r.offsetParent !== null).map(r => r.querySelector("code").textContent), count: document.querySelector("[data-filter-count]").textContent, empty: document.querySelector("tr[data-filter-empty]").hidden }));
    ok(f.shown.join() === "gamma_tool" && f.count === "1 of 3", w + ": filter matches the description text too " + JSON.stringify(f));
    await pg.$eval("input[data-filter]", e => { e.value = ""; });
    await pg.type("input[data-filter]", "zzz");
    f = await pg.evaluate(() => ({ shown: [...document.querySelectorAll("#tools-table tr[data-filter-row]")].filter(r => !r.hidden).length, empty: !document.querySelector("tr[data-filter-empty]").hidden && document.querySelector("tr[data-filter-empty]").offsetParent !== null }));
    ok(f.shown === 0 && f.empty, w + ": nothing matches says so " + JSON.stringify(f));
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
