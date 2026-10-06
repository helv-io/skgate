// One-click actions save in place: no navigation, the scroll stays where it was, the page shows the new state, and
// the server has it. A switch that fails flips back and says why. Phone and desktop widths.
// Usage: node inplace.js <base> <chrome> <puppeteer-core> <cookies> <provider base URL>
const [url, chrome, pp, cookiesJSON, provBase] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) { bad.push(m); console.log("FAIL", m); } else console.log("ok  ", m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  pg.on("dialog", d => d.accept());
  await pg.setCookie(...JSON.parse(cookiesJSON));
  let navs = 0; // full loads only: a fragment change (a dialog opening) is not a navigation
  pg.on("request", r => { if (r.isNavigationRequest() && r.frame() === pg.mainFrame()) navs++; });
  if (process.env.INPLACE_DEBUG) {
    pg.on("console", m => console.log("console:", m.text()));
    pg.on("response", r => { if (r.request().method() === "POST") console.log("POST", r.url(), r.status()); });
  }
  let failNext = null; // path whose next POST is answered with a refusal
  await pg.setRequestInterception(true);
  pg.on("request", r => {
    if (failNext && r.method() === "POST" && new URL(r.url()).pathname === failNext) {
      failNext = null;
      r.respond({ status: 200, contentType: "application/json", body: JSON.stringify({ toast: { k: "bad", m: "the database refused the change" } }) });
      return;
    }
    r.continue();
  });
  const open = async (path, w) => {
    await pg.setViewport({ width: w, height: 560, isMobile: w < 720, hasTouch: w < 720 });
    await pg.goto(url + path);
    await pg.evaluate(() => { window.__stay = 1; });
    navs = 0;
  };
  const scrollTo = sel => pg.evaluate(s => {
    const el = document.querySelector(s);
    const r = el.getBoundingClientRect();
    window.scrollTo(0, Math.max(0, window.scrollY + r.top - 200));
    return window.scrollY;
  }, sel);
  const settled = async () => {
    for (let i = 0; i < 100; i++) {
      if (!(await pg.evaluate(() => !!document.querySelector("form[data-saving]")))) break;
      await wait(50);
    }
    await wait(250);
  };
  const stayed = async (tag, y) => {
    const s = await pg.evaluate(() => ({ stay: window.__stay === 1, y: window.scrollY, max: document.documentElement.scrollHeight - window.innerHeight }));
    ok(s.stay && navs === 0, tag + ": no navigation (navs " + navs + ")");
    // the scroll stays; only a page that got shorter (a row gone) can hold it lower, at its new end
    const want = Math.min(y, Math.max(0, s.max));
    ok(Math.abs(s.y - want) <= 1, tag + ": scroll kept " + y + " -> " + s.y + (want < y ? " (page end " + want + ")" : ""));
  };
  const confirm = async () => {
    await pg.waitForSelector("[data-modal][open] [data-modal-ok]", { visible: true });
    await pg.click("[data-modal][open] [data-modal-ok]");
  };
  // open the row's menu when the actions sit in one (a phone); on a wide screen they are in the row
  const menu = row => pg.evaluate(r => {
    const m = document.querySelector(r + " [data-menu-button]");
    if (m && m.offsetParent && getComputedStyle(m).display !== "none") m.click();
  }, row);
  const server = path => pg.evaluate(p => fetch(p, { cache: "no-store" }).then(r => r.text()), path);
  const toasts = () => pg.evaluate(() => [...document.querySelectorAll(".toast")].map(t => t.textContent.trim()));

  for (const w of [390, 1280]) {
    // Upstreams: Enabled and In /mcp switch in place; a refused switch flips back with the reason
    await open("/admin/upstreams", w);
    const en = "#up-u20 form[action$='/toggle'] input[value=enabled] ~ button";
    const inc = "#up-u20 form[action$='/toggle'] input[value=include] ~ button";
    let y = await scrollTo("#up-u20");
    ok(y > 300, w + ": the list is scrolled down " + y);
    const before = await pg.$eval(en, b => b.getAttribute("aria-checked"));
    await pg.click(en);
    await settled();
    await stayed(w + " enabled switch", y);
    let st = await pg.$eval(en, b => ({ c: b.getAttribute("aria-checked"), t: b.textContent }));
    ok(st.c !== before && st.t === (st.c === "true" ? "Enabled" : "Disabled"), w + ": the row shows the new state " + JSON.stringify(st));
    let html = await server("/admin/upstreams");
    ok(new RegExp('id="up-u20"[\\s\\S]*?value="enabled"><button class="act toggle ' + (st.c === "true" ? "on" : "off")).test(html), w + ": enabled was saved");
    const ib = await pg.$eval(inc, b => b.getAttribute("aria-checked"));
    await pg.click(inc);
    await settled();
    await stayed(w + " in /mcp switch", y);
    st = await pg.$eval(inc, b => b.getAttribute("aria-checked"));
    html = await server("/admin/upstreams");
    ok(st !== ib && new RegExp('id="up-u20"[\\s\\S]*?value="include"><button class="act toggle ' + (st === "true" ? "on" : "off")).test(html), w + ": in /mcp shows and was saved " + st);
    failNext = "/admin/upstreams/u20/toggle";
    const was = await pg.$eval(en, b => b.getAttribute("aria-checked"));
    await pg.click(en);
    await settled();
    await stayed(w + " refused switch", y);
    st = await pg.$eval(en, b => ({ c: b.getAttribute("aria-checked"), title: b.title }));
    ok(st.c === was && st.title === "the database refused the change", w + ": a refused switch flips back with the reason as its tooltip " + JSON.stringify(st));
    ok((await toasts()).includes("the database refused the change"), w + ": and as a toast");
    // Delete from the row menu, after the confirmation
    const victim = w === 390 ? "u21" : "u22";
    y = await scrollTo("#up-" + victim);
    await menu("#up-" + victim);
    await pg.click("#up-" + victim + " form[action$='/delete'] button");
    await confirm();
    await settled();
    await stayed(w + " delete upstream", y);
    ok(!(await pg.$("#up-" + victim)), w + ": the deleted row is gone");
    ok(!(await server("/admin/upstreams")).includes('id="up-' + victim + '"'), w + ": the delete was saved");

    // Process page: Start, then Stop (confirmed), then Clear logs, in place
    await open("/admin/upstreams/proc/logs", w);
    y = await pg.evaluate(() => window.scrollY);
    await pg.click("form[action$='/process'] input[value=start] ~ button");
    await settled();
    await stayed(w + " start", y);
    for (let i = 0; i < 40; i++) { if (await pg.$("form[action$='/process'] input[value=stop] ~ button")) break; await wait(150); await pg.evaluate(() => window.skgateRefresh()); }
    ok(!!(await pg.$("form[action$='/process'] input[value=stop] ~ button")), w + ": the actions follow the new state");
    await pg.click("form[action$='/process'] input[value=stop] ~ button");
    await confirm();
    await settled();
    await stayed(w + " stop", y);
    ok(!!(await pg.$("form[action$='/process'] input[value=start] ~ button")) && !/>running</.test(await server("/admin/upstreams/proc/logs")), w + ": stopped, and Start is back");

    // Keys: Save in the dialog closes it in place; Revoke (confirmed) marks the row
    await open("/admin/keys", w);
    const krow = "#key-rows tr:nth-child(8)"; // mid-list, so the page is long enough under the scroll after a row goes
    y = await scrollTo(krow);
    const kd = await pg.$eval(krow + " [data-dialog-open]", e => e.getAttribute("data-dialog-open"));
    const kbtn = '#key-rows [data-dialog-open="' + kd + '"]';
    await pg.click(kbtn);
    await pg.waitForSelector("[data-modal][open] input[name=label]", { visible: true });
    y = await pg.evaluate(() => window.scrollY);
    const label = "renamed " + w;
    await pg.$eval("[data-modal][open] input[name=label]", (e, v) => { e.value = v; e.dispatchEvent(new Event("input", { bubbles: true })); }, label);
    await pg.click("[data-modal][open] form[action='/admin/keys/update'] button.btn");
    await settled();
    await stayed(w + " key save", y);
    ok(!(await pg.$("[data-modal][open]")), w + ": the dialog closed after the save");
    ok((await pg.$eval(krow + " td.primary", e => e.textContent.trim())) === label && (await server("/admin/keys")).includes(label), w + ": the new name shows and was saved");
    await pg.click(kbtn);
    await pg.waitForSelector("[data-modal][open] form[action='/admin/keys/revoke'] button", { visible: true });
    await pg.click("[data-modal][open] form[action='/admin/keys/revoke'] button");
    await confirm();
    await settled();
    await stayed(w + " key revoke", y);
    // a revoked key that was never used is deleted at once: its row and its dialog go
    ok(!(await pg.$(kbtn)) && !(await pg.$("template" + kd)), w + ": the revoked key's row and dialog are gone");
    ok(!(await server("/admin/keys")).includes('data-dialog-open="' + kd + '"'), w + ": the revoke was saved");

    // Clients: Delete a row, then Delete unused, both confirmed
    await open("/admin/clients", w);
    const row = "#client-rows tr:nth-child(" + (w === 390 ? 8 : 9) + ")"; // mid-list: a row from the end would shorten the page under the scroll
    y = await scrollTo(row);
    const n0 = await pg.$$eval("#client-rows tr", r => r.length);
    const cid = await pg.$eval(row + " form[action='/admin/clients/delete'] input[name=id]", e => e.value);
    await menu(row);
    await pg.click(row + " form[action='/admin/clients/delete'] button");
    await confirm();
    await settled();
    await stayed(w + " client delete", y);
    ok((await pg.$$eval("#client-rows tr", r => r.length)) === n0 - 1 && !(await server("/admin/clients")).includes('value="' + cid + '"'), w + ": the client is gone and stays gone");
    if (w === 390) {
      y = await scrollTo("#clients-unused");
      await pg.click("#clients-unused button");
      await confirm();
      await settled();
      await stayed(w + " delete unused", y);
      ok((await pg.$eval("#clients-unused button", e => e.disabled)) && !(await server("/admin/clients")).includes("stale-"), w + ": unused clients are gone and the button is off");
    }

    // Status: Add provider in its dialog, then Remove provider (confirmed)
    await open("/admin", w);
    y = await pg.evaluate(() => window.scrollY);
    await pg.click('[data-dialog-open="#add-provider"]');
    await pg.waitForSelector("[data-modal][open] form[action='/admin/providers/add']", { visible: true });
    y = await pg.evaluate(() => window.scrollY);
    await pg.select("[data-modal][open] select[name=preset]", "openai");
    await pg.$eval("[data-modal][open] input[name=base]", (e, v) => { e.value = v; }, provBase);
    await pg.$eval("[data-modal][open] input[name=key]", e => { e.value = "sk-test-1234"; });
    await pg.click("[data-modal][open] form[action='/admin/providers/add'] button.btn");
    await settled();
    await stayed(w + " add provider", y);
    ok(!(await pg.$("[data-modal][open]")) && !!(await pg.$("#other-providers #openai")), w + ": the dialog closed and the card is there");
    await pg.click('[data-dialog-open="#provider-openai"]');
    await pg.waitForSelector("[data-modal][open] form[action$='/remove'] button", { visible: true });
    y = await pg.evaluate(() => window.scrollY);
    await pg.click("[data-modal][open] form[action$='/remove'] button");
    await confirm();
    await settled();
    await stayed(w + " remove provider", y);
    ok(!(await pg.$("#openai")) && !(await server("/admin")).includes('id="openai"'), w + ": the card is gone and the provider removed");
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
