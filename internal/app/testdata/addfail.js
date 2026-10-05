// The add form in a real browser: the button waits for what the type needs, a failed add (the server refuses or needs
// a key) leaves the page and every field as they were with a short error, and the fixed form then saves.
// Usage: node addfail.js <base url> <chrome> <puppeteer-core dir> <cookies json> <api url> <spec json>
const [url, chrome, pp, cookiesJSON, api, spec] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 1280, height: 900 });
  await pg.goto(url + "/admin/upstreams/new");
  const gate = () => pg.evaluate(() => {
    const btn = document.querySelector("[data-gate-submit] button"), why = document.querySelector("[data-gate-reason]");
    return { disabled: btn.disabled, title: btn.title, hint: why.hidden ? "" : why.textContent, label: btn.textContent };
  });

  let g = await gate();
  ok(g.disabled && g.label === "Add" && g.title === "Enter an alias" && g.hint === "Enter an alias", "the button waits for an alias, with the reason as tooltip and hint: " + JSON.stringify(g));
  await pg.select("[data-kind-select]", "openapi");
  await pg.type('input[name="alias"]', "failcase");
  g = await gate();
  ok(g.disabled && g.hint === "Enter an address", "the button waits for the API: " + JSON.stringify(g));
  ok(!/[()]/.test(g.hint), "the reason has no parenthetical");

  // a pasted description counts as the API
  await pg.$eval('textarea[name="oa_spec_text"]', (e, v) => { e.value = v; e.dispatchEvent(new Event("input", { bubbles: true })); }, spec);
  g = await gate();
  ok(!g.disabled && g.hint === "" && g.title === "", "the button is on once the API is given: " + JSON.stringify(g));
  await pg.type('input[name="oa_auth_value"]', "WRONGKEY");
  await pg.evaluate(() => { document.querySelector('[data-kind="openapi"] details').open = true; });
  await pg.type('input[name="oa_auth_as"]', "bearer");

  const fields = () => pg.evaluate(() => ({
    path: location.pathname, kind: document.querySelector("[data-kind-select]").value,
    alias: document.querySelector('input[name="alias"]').value, spec: document.querySelector('textarea[name="oa_spec_text"]').value,
    key: document.querySelector('input[name="oa_auth_value"]').value, as: document.querySelector('input[name="oa_auth_as"]').value,
    keyType: document.querySelector('input[name="oa_auth_value"]').type, open: document.querySelector('[data-kind="openapi"] details').open,
  }));
  const before = await fields();

  // the server refuses the key: the form stays exactly as it was
  await pg.click("[data-gate-submit] button");
  await pg.waitForSelector(".toast.bad", { timeout: 20000 });
  const toast = await pg.evaluate(() => document.querySelector(".toast.bad").textContent);
  ok(/refused the key/.test(toast) && !/[()]/.test(toast) && toast.length < 80, "a short plain error: " + toast);
  const after = await fields();
  ok(JSON.stringify(before) === JSON.stringify(after) && after.path === "/admin/upstreams/new" && after.key === "WRONGKEY" && after.keyType === "password" && after.spec === spec,
    "a failed add keeps every field: " + JSON.stringify(before) + " / " + JSON.stringify(after));
  g = await gate();
  ok(!g.disabled, "the button is back after a failure: " + JSON.stringify(g));
  ok(await pg.evaluate(() => !document.body.textContent.includes("WRONGKEY")), "the key is not echoed into the page");

  // Skip check is a button beside Add from the start, with one line that says what it does; no checkbox, no old hint
  const sk = await pg.evaluate(() => {
    const b = document.querySelector("button[data-oa-skip-btn]");
    const r = b && b.getBoundingClientRect(), a = document.querySelector("[data-gate-submit] button").getBoundingClientRect();
    return { shown: !!b && b.offsetParent !== null, text: b ? b.textContent : "", cls: b ? b.className : "", line: /Skip check adds it without calling the server\./.test(document.body.textContent),
      old: /first GET/.test(document.body.textContent) || !!document.querySelector('input[type="checkbox"][name="oa_skip_check"]'), side: !!r && Math.abs(r.top - a.top) < 40 || (r && r.top >= a.top) };
  });
  ok(sk.shown && sk.text === "Skip check" && sk.cls === "act" && sk.line && !sk.old, "Skip check button: " + JSON.stringify(sk));

  // no key at all: the server needs one, and the fields stay
  await pg.$eval('input[name="oa_auth_value"]', e => { e.value = ""; });
  await pg.evaluate(() => document.querySelectorAll(".toast").forEach(t => t.remove()));
  const hit = await pg.evaluate(() => {
    const b = document.querySelector("[data-gate-submit] button"), r = b.getBoundingClientRect();
    const t = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return { disabled: b.disabled, onButton: t === b || b.contains(t), over: t ? t.tagName + "." + t.className : "none", rect: [r.left, r.top, r.width, r.height], vh: innerHeight };
  });
  await pg.click("[data-gate-submit] button");
  try {
    await pg.waitForFunction(() => /needs a key/.test(document.body.textContent), { timeout: 20000 });
  } catch (e) {
    console.log("no key wait failed: " + pg.url() + " " + JSON.stringify(hit) + " toasts: " + await pg.evaluate(() => Array.from(document.querySelectorAll(".toast")).map(t => t.textContent).join("|")));
    throw e;
  }
  const nokey = await fields();
  ok(nokey.path === "/admin/upstreams/new" && nokey.alias === "failcase" && nokey.spec === spec && nokey.as === "bearer" && nokey.key === "" && nokey.open === true,
    "a missing key keeps every field: " + JSON.stringify(nokey));
  ok(await pg.evaluate(() => document.querySelector('input[name="oa_skip_check"]').value === ""), "nothing is skipped by a plain Add");

  // fix the key and add again: the form saves and goes on to the tools
  await pg.type('input[name="oa_auth_value"]', "GOODKEY");
  await Promise.all([pg.waitForNavigation({ timeout: 20000 }), pg.click("[data-gate-submit] button")]);
  ok(new URL(pg.url()).pathname === "/admin/upstreams/failcase/tools", "a good add goes on to the tools: " + pg.url());
  // Skip check saves a server that refuses: a second add, no key
  await pg.goto(url + "/admin/upstreams/new");
  await pg.select("[data-kind-select]", "openapi");
  await pg.type('input[name="alias"]', "skipcase");
  await pg.$eval('textarea[name="oa_spec_text"]', (e, v) => { e.value = v; e.dispatchEvent(new Event("input", { bubbles: true })); }, spec);
  // Skip check up front: the server is never called, the upstream is added
  await Promise.all([pg.waitForNavigation({ timeout: 20000 }), pg.click("button[data-oa-skip-btn]")]);
  ok(new URL(pg.url()).pathname === "/admin/upstreams/skipcase/tools", "Skip check adds without the key: " + pg.url());
  // phone width: Skip check and its line fit the screen and are tappable
  await pg.setViewport({ width: 360, height: 740, isMobile: true, hasTouch: true });
  await pg.goto(url + "/admin/upstreams/new");
  await pg.select("[data-kind-select]", "openapi");
  const mob = await pg.evaluate(() => {
    const b = document.querySelector("button[data-oa-skip-btn]"), r = b.getBoundingClientRect(), p = document.querySelector("p[data-oa-skip-btn]").getBoundingClientRect();
    return { w: document.documentElement.scrollWidth, vw: window.innerWidth, right: Math.round(r.right), h: Math.round(r.height), pright: Math.round(p.right), shown: b.offsetParent !== null };
  });
  ok(mob.shown && mob.w <= mob.vw && mob.right <= mob.vw && mob.pright <= mob.vw && mob.h >= 32, "Skip check on a phone: " + JSON.stringify(mob));
  await b.close();
  if (bad.length) { console.log(bad.join("\n")); process.exit(1); }
  console.log("ALL OK");
})().catch(e => { console.log(e.stack || String(e)); process.exit(1); });
