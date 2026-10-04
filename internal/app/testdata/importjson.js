const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const wait = ms => new Promise(r => setTimeout(r, ms));
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setViewport({ width: 390, height: 900 });
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.goto(url + "/admin/upstreams/import");
  const st = () => pg.evaluate(() => { const a = document.querySelector("textarea[name=json]"), p = document.querySelector("[data-json-status]"); return { invalid: a.getAttribute("aria-invalid"), valid: a.validity.valid, formValid: a.form.checkValidity(), text: p.textContent, bad: p.classList.contains("bad"), border: getComputedStyle((document.querySelector(".codehost") || {}).shadowRoot ? document.querySelector(".codehost").shadowRoot.querySelector(".cm-editor") : a).borderTopColor }; });
  const typeIn = async t => { await pg.click(".codehost"); await pg.keyboard.press("End"); await pg.keyboard.type(t); await wait(700); }; // the editor is what people type into
  let s = await st();
  ok(!s.invalid && !s.valid && /Paste or type/.test(s.text) && !s.bad, "empty: quietly blocked " + JSON.stringify(s));
  await typeIn('{"mcpServers": {"fixed": {"url": "http://127.0.0.1:1/mcp"},');
  s = await st();
  ok(s.invalid === "true" && !s.formValid && s.bad && /^Not valid JSON: /.test(s.text) && s.border === "rgb(255, 138, 128)", "broken JSON while typing: marked, explained, blocked " + JSON.stringify(s));
  await pg.click("form[action='/admin/upstreams/import'] button.btn");
  await wait(500);
  ok(pg.url().endsWith("/admin/upstreams/import"), "Import does nothing while the JSON is broken: " + pg.url());
  await typeIn(' "other": {"url": "http://127.0.0.1:2/mcp"}}}');
  s = await st();
  ok(!s.invalid && s.formValid && !s.bad && s.text === "Valid JSON · 2 servers", "fixed: valid and counted " + JSON.stringify(s));
  // one server only: drop the second entry
  await pg.$eval("textarea[name=json]", e => { e.value = '{"mcpServers": {"fixed": {"url": "http://127.0.0.1:1/mcp"}}}'; e.dispatchEvent(new Event("input", { bubbles: true })); });
  s = await st();
  ok(s.text === "Valid JSON · 1 server" && s.formValid, "singular " + JSON.stringify(s));
  await Promise.all([pg.waitForNavigation(), pg.click("form[action='/admin/upstreams/import'] button.btn")]);
  ok(await pg.evaluate(() => document.body.textContent.includes("fixed")), "the fixed JSON was imported");
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
