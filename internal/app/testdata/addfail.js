// The add form in a real browser: the button waits for what the type needs, a failed add (the server refuses the
// key) leaves the page and every field as they were with a short error, and the fixed form then saves.
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
  ok(g.disabled && g.hint === "Enter the address of the API", "the button waits for the API: " + JSON.stringify(g));
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

  // fix the key and add again: the form saves and goes on to the tools
  await pg.$eval('input[name="oa_auth_value"]', e => { e.value = "GOODKEY"; });
  await Promise.all([pg.waitForNavigation({ timeout: 20000 }), pg.click("[data-gate-submit] button")]);
  ok(new URL(pg.url()).pathname === "/admin/upstreams/failcase/tools", "a good add goes on to the tools: " + pg.url());
  await b.close();
  if (bad.length) { console.log(bad.join("\n")); process.exit(1); }
  console.log("ALL OK");
})().catch(e => { console.log(e.stack || String(e)); process.exit(1); });
