const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
const rgb = s => s.match(/[\d.]+/g).map(Number);
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  for (const w of [390, 1280]) {
    await pg.setViewport({ width: w, height: 900 });
    await pg.goto(url + "/admin/upstreams");
    const sw = await pg.evaluate(() => [...document.querySelectorAll("button.toggle")].map(e => {
      const s = getComputedStyle(e), k = getComputedStyle(e, "::before");
      return { alias: e.closest("tr").querySelector("a.name code").textContent, label: e.textContent.trim(), role: e.getAttribute("role"), checked: e.getAttribute("aria-checked"),
        disabled: e.disabled, color: s.color, opacity: parseFloat(s.opacity), cursor: s.cursor, knob: k.content, track: k.width, pos: k.backgroundPosition };
    }));
    const by = (a, l) => sw.find(x => x.alias === a && x.label === l);
    const off = by("plain", "Not in /mcp"), on = by("plain", "Enabled"), dis = by("od", "Not in /mcp");
    ok(sw.length === 4 && sw.every(x => x.role === "switch" && (x.checked === "true") === /^(Enabled|In \/mcp)$/.test(x.label)), w + ": every toggle is a switch with aria-checked " + JSON.stringify(sw));
    ok(on && on.knob === '""' && on.track === "28px", w + ": a switch draws a track " + JSON.stringify(on));
    ok(on && on.pos !== off.pos, w + ": the knob moves with the state " + on.pos + " vs " + off.pos);
    ok(off && !off.disabled && off.opacity === 1, w + ": 'not in /mcp' is a normal, neutral switch");
    const [r, g, bl] = rgb(off.color);
    ok(r < 160 && g > 100 && Math.abs(r - g) < 40 && Math.abs(g - bl) < 40, w + ": 'not in /mcp' is neutral, not red: " + off.color);
    ok(dis && dis.disabled && dis.opacity < 0.6 && dis.cursor === "not-allowed", w + ": the disabled switch looks disabled " + JSON.stringify(dis));
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
