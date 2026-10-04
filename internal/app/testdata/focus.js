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
    await pg.goto(url + "/admin/keys");
    await pg.evaluate(() => { document.querySelector("form[action='/admin/keys/create'] details").open = true; });
    // Tab through the create form: every stop must show a ring (an outline of at least 2px, not "none")
    await pg.focus("form[action='/admin/keys/create'] input[name=label]");
    const seen = [];
    for (let i = 0; i < 9; i++) {
      await pg.keyboard.press("Tab");
      seen.push(await pg.evaluate(() => { const e = document.activeElement, s = getComputedStyle(e); return { name: e.getAttribute("name") || e.getAttribute("data-expiry-set") || e.tagName, style: s.outlineStyle, width: parseFloat(s.outlineWidth), color: s.outlineColor }; }));
    }
    const names = seen.map(s => s.name);
    for (const n of ["rate", "expires", "1d", "7d", "30d", "90d", "1y", "never"]) {
      const s = seen.find(x => x.name === n);
      ok(s && s.style === "solid" && s.width >= 2, w + "px: " + n + " shows a focus ring: " + JSON.stringify(s) + " seen " + names);
    }
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
