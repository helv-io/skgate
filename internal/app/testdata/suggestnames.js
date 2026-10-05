// Suggest names and selection: one click fills every name and ticks the core set the helper chose.
// Usage: node suggestnames.js <base url> <chrome> <puppeteer-core dir> <cookies json> <alias>
const [url, chrome, pp, cookiesJSON, alias] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 1280, height: 900 });
  await pg.goto(url + "/admin/upstreams/" + alias + "/tools");

  const label = await pg.evaluate(() => {
    const b = document.querySelector("[data-tool-describe]");
    return { text: b ? b.textContent : "", dialog: !!document.getElementById("suggest-names"), disabled: b && b.disabled };
  });
  ok(label.text === "Suggest names and selection" && !label.dialog && !label.disabled, "one-click button: " + JSON.stringify(label));

  await pg.click("[data-tool-describe]");
  await pg.waitForFunction(() => /Filled \d+ names?, \d+ on/.test(document.body.textContent), { timeout: 15000 });
  const after = await pg.evaluate(() => {
    const rows = [...document.querySelectorAll("[data-tool]")].map(row => ({
      key: row.dataset.key,
      name: (row.querySelector("[data-tool-name]") || {}).value,
      on: !!(row.querySelector("[data-tool-on]") || {}).checked,
    }));
    const btn = document.querySelector("[data-tool-describe]");
    return { rows, disabled: btn.disabled, n: Number(document.querySelector("[data-toolcount-n]").textContent) };
  });
  const get = after.rows.find(r => r.key === "GET /notes");
  ok(get && get.name === "list_my_notes" && get.on, "GET named and on: " + JSON.stringify(get));
  ok(after.rows.filter(r => r.on).length === 1 && after.n === 1, "only the core set is on: " + JSON.stringify(after));
  ok(!after.disabled, "button is back after the answer");

  await pg.setViewport({ width: 360, height: 740, isMobile: true, hasTouch: true });
  await pg.goto(url + "/admin/upstreams/" + alias + "/tools");
  const phone = await pg.evaluate(() => {
    const b = document.querySelector("[data-tool-describe]");
    const r = b && b.getBoundingClientRect();
    return { shown: !!b && b.offsetParent !== null, inView: r && r.left >= 0 && r.right <= innerWidth + 1, text: b && b.textContent };
  });
  ok(phone.shown && phone.inView && phone.text === "Suggest names and selection", "button fits at 360 px: " + JSON.stringify(phone));

  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
