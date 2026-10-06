// The SI helper status line (si_status) never moves a button: on the tools page (Suggest names and selection), the
// add and edit pages (Suggest configuration) and the OpenAPI form (Repair), at phone and desktop widths, every
// button, link button and field keeps its box when the line appears, changes stage, shows an error and clears.
// The line sits on its own line under the button's row.
// Usage: node sistatus.js <base> <chrome> <puppeteer-core> <cookies> <spec>...  spec = kind|path (kind: describe, suggest, repair)
const [url, chrome, pp, cookiesJSON, ...specs] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) { bad.push(m); console.log("FAIL", m); } };
const wait = ms => new Promise(r => setTimeout(r, ms));
const LONG = "the helper model answered with something that is not a list of tools, so nothing was filled; try again or pick another model";
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  pg.on("dialog", d => d.accept());
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setRequestInterception(true);
  let held = null, holdPath = "";
  pg.on("request", r => {
    if (holdPath && r.method() === "POST" && new URL(r.url()).pathname === holdPath) { held = r; return; }
    r.continue();
  });
  const boxes = () => pg.evaluate(() => [...document.querySelectorAll("button, a.act, a.btn, input:not([type=hidden]), select, textarea")]
    .filter(e => e.offsetParent !== null || getComputedStyle(e).position === "fixed")
    .map(e => { const r = e.getBoundingClientRect(); return [e.tagName + ":" + (e.textContent || e.name || "").trim().slice(0, 30), Math.round(r.left * 2) / 2, Math.round(r.top * 2) / 2, Math.round(r.width * 2) / 2, Math.round(r.height * 2) / 2]; }));
  const same = (a, b) => {
    if (a.length !== b.length) return "count " + a.length + " -> " + b.length;
    for (let i = 0; i < a.length; i++) if (a[i].some((v, j) => typeof v === "number" ? Math.abs(v - b[i][j]) > 0.5 : v !== b[i][j])) return JSON.stringify(a[i]) + " -> " + JSON.stringify(b[i]);
    return "";
  };
  const line = sel => pg.evaluate(s => {
    const btn = document.querySelector(s), row = btn.closest(".row"), st = row.nextElementSibling;
    if (!st || !st.hasAttribute("data-si-status")) return null;
    const rr = row.getBoundingClientRect(), sr = st.getBoundingClientRect();
    return { text: st.textContent, bad: st.classList.contains("bad"), hidden: st.hidden, h: sr.height, below: sr.top >= rr.bottom - 0.5, inRow: row.contains(st), title: st.title };
  }, sel);
  for (const [w, mobile] of [[390, true], [1280, false]]) {
    await pg.setViewport({ width: w, height: 900, isMobile: mobile, hasTouch: mobile });
    for (const spec of specs) {
      const [kind, path] = spec.split("|");
      const tag = w + " " + kind + " " + path;
      const sel = { describe: "[data-tool-describe]", suggest: "[data-suggest]", repair: "[data-oa-repair]" }[kind];
      await pg.goto(url + path);
      if (kind === "suggest") {
        await pg.evaluate(() => { const s = document.querySelector("[data-suggest-source]"); s.value = "https://github.com/acme/thing"; s.dispatchEvent(new Event("input", { bubbles: true })); });
      }
      if (kind === "repair") await pg.evaluate(() => { const r = document.querySelector("[data-oa-out]"); r.hidden = false; r.querySelector("[data-oa-repair-row]").hidden = false; });
      await pg.evaluate(s => document.querySelector(s).scrollIntoView({ block: "center" }), sel);
      await wait(100);
      const l0 = await line(sel);
      ok(l0 && !l0.inRow && l0.below && l0.text === "" && !l0.hidden && l0.h >= 18, tag + ": an empty status line keeps its room on its own line under the row " + JSON.stringify(l0));
      const base = await boxes();
      ok(base.length > 0, tag + ": buttons measured");
      const step = async (name, check) => {
        const l = await line(sel);
        ok(check(l), tag + ": " + name + ": status " + JSON.stringify(l));
        const d = same(base, await boxes());
        ok(!d, tag + ": " + name + " moved a button or field: " + d);
      };
      if (kind === "repair") {
        await pg.evaluate(s => siSay(document.querySelector(s), "Asking SI", false), sel);
        await step("appears", l => l.text === "Asking SI");
      } else {
        held = null;
        holdPath = kind === "describe" ? path.replace(/\/tools$/, "/tools/suggest") : "/admin/upstreams/suggest";
        await pg.click(sel);
        for (let i = 0; i < 50 && !held; i++) await wait(50);
        ok(held, tag + ": the helper request was sent");
        await wait(50);
        await step("appears", l => l.text !== "" && !l.bad);
      }
      await pg.evaluate(s => siSay(document.querySelector(s), "Asking the model \u00b7 2.5k chars", false), sel);
      await step("changes stage", l => l.text === "Asking the model \u00b7 2.5k chars");
      if (held) {
        await held.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ error: LONG }) });
        held = null; holdPath = "";
        for (let i = 0; i < 50; i++) { const l = await line(sel); if (l && l.bad) break; await wait(50); }
      } else {
        await pg.evaluate((s, t) => siSay(document.querySelector(s), t, true), sel, LONG);
      }
      await step("shows an error", l => l.bad && l.text === LONG && l.title === LONG);
      if (process.env.SKGATE_SHOTS) await pg.screenshot({ path: process.env.SKGATE_SHOTS + "/si-" + w + "-" + kind + "-" + path.replace(/\W+/g, "_") + ".png" });
      await pg.evaluate(s => siSay(document.querySelector(s), "", false), sel);
      await step("clears", l => l.text === "" && !l.bad);
      console.log("ok   " + tag);
    }
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
