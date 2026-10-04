// The Test page's tools table in a real browser: a description looks clickable (dotted underline, pointer) only while a
// click shows more, the Name column is about 30% on wide screens, long names wrap, and phones keep the card layout.
// Usage: node descline.js <base url> <chrome> <puppeteer-core dir> <cookies json>
const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  const rows = () => pg.evaluate(() => [...document.querySelectorAll("#tools-table tbody tr[data-filter-row]")].map(tr => {
    const name = tr.querySelector("td.primary"), cell = tr.querySelector("td.wrap"), el = cell.querySelector("[data-desc-line], summary"), cs = getComputedStyle(el);
    const r = tr.getBoundingClientRect(), n = name.getBoundingClientRect(), c = cell.getBoundingClientRect();
    return {
      name: tr.querySelector("code").textContent, kind: el.tagName, underline: cs.textDecorationLine + " " + cs.textDecorationStyle, cursor: cs.cursor, expands: el.classList.contains("expands"),
      role: el.getAttribute("role"), tab: el.getAttribute("tabindex"), cut: el.scrollWidth > el.clientWidth + 1, h: Math.round(el.getBoundingClientRect().height),
      nameShare: Math.round(100 * n.width / r.width), descShare: Math.round(100 * c.width / r.width), nameH: Math.round(n.height), scrollsSideways: document.documentElement.scrollWidth > document.documentElement.clientWidth,
    };
  }));
  const by = (rs, n) => rs.find(r => r.name === n);
  for (const w of [1280, 1024, 390]) {
    await pg.setViewport({ width: w, height: 900 });
    await pg.goto(url + "/admin/upstreams/fake/test");
    await wait(500);
    const rs = await rows();
    ok(rs.length === 5, w + ": five tools " + rs.length);
    ok(!rs[0].scrollsSideways, w + ": no sideways page scroll");
    const short = by(rs, "short_tool"), more = by(rs, "more_tool"), mid = by(rs, "mid_tool"), long = by(rs, "long_tool");
    ok(short.kind === "SPAN" && !short.expands && /^none/.test(short.underline) && short.role === null && short.tab === null, w + ": a short description is plain text " + JSON.stringify(short));
    ok(more.kind === "SUMMARY" && /dotted/.test(more.underline), w + ": text after the first line is a real expander " + JSON.stringify(more));
    ok(long.expands && /dotted/.test(long.underline) && long.cursor === "pointer" && long.role === "button" && long.tab === "0", w + ": a cut-off line is underlined and clickable " + JSON.stringify(long));
    if (w >= 1024) ok(!mid.expands && /^none/.test(mid.underline), w + ": a line that fits is not underlined " + JSON.stringify(mid));
    if (w === 390) ok(mid.expands && /dotted/.test(mid.underline), w + ": the same line is cut on a phone, so it is " + JSON.stringify(mid));
    if (w >= 1024) {
      ok(Math.abs(short.nameShare - 30) <= 3 && Math.abs(short.descShare - 70) <= 3, w + ": Name and Description are about 30/70: " + short.nameShare + "/" + short.descShare);
      const wrapName = by(rs, "a_tool_with_a_rather_long_name_that_has_to_wrap_somewhere_in_the_name_column");
      ok(wrapName.nameH > short.nameH && wrapName.nameShare <= 33, w + ": a long name wraps inside its column " + wrapName.nameH + " vs " + short.nameH);
    }
    // clicking opens the cut-off line to its whole text, and again closes it
    const before = long.h;
    await pg.evaluate(() => [...document.querySelectorAll("[data-desc-line]")].find(e => e.textContent.startsWith("A long single")).click());
    await wait(150);
    const open = (await rows()).find(r => r.name === "long_tool");
    ok(open.h > before * 1.5, w + ": a click shows the whole line " + before + " -> " + open.h);
    await pg.evaluate(() => [...document.querySelectorAll("[data-desc-line]")].find(e => e.textContent.startsWith("A long single")).click());
    await wait(150);
    ok((await rows()).find(r => r.name === "long_tool").h === before, w + ": a second click closes it");
    // keyboard
    await pg.evaluate(() => [...document.querySelectorAll("[data-desc-line]")].find(e => e.textContent.startsWith("A long single")).focus());
    await pg.keyboard.press("Enter"); await wait(150);
    ok((await rows()).find(r => r.name === "long_tool").h > before * 1.5, w + ": Enter opens it");
    // every details block on the page has something to show beyond its summary
    const fake = await pg.evaluate(() => [...document.querySelectorAll("details:not(.verbgroup)")].filter(d => d.textContent.trim() === (d.querySelector("summary") || {}).textContent.trim()).length);
    ok(fake === 0, w + ": no details block without content: " + fake);
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
