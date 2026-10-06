const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const wait = ms => new Promise(r => setTimeout(r, ms));
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setViewport({ width: 1000, height: 900 });
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.goto(url + "/admin#aliases-grok");
  await pg.waitForSelector("dialog[open] [data-section=aliases]");
  await pg.evaluate(() => { window.__same = 1; document.body.style.minHeight = "2400px"; window.scrollTo(0, 480); });
  const dlg = "dialog[open] ";
  await pg.type(dlg + "input[type=text][name=name]", "fast");
  await pg.click(dlg + "form[action$='/aliases/put'] button.btn");
  await pg.waitForFunction(() => /fast/.test(document.querySelector("dialog [data-section=aliases] tbody").textContent)
    && /fast/.test(document.querySelector("#alias-overview").textContent)
    && /1 alias/.test(document.querySelector("#grok").textContent), { timeout: 5000 });
  const st = await pg.evaluate(() => ({
    same: window.__same,
    name: document.querySelector("dialog input[type=text][name=name]").value,
    dirty: document.querySelector("dialog [data-section=aliases]").hasAttribute("data-dirty"),
    toast: (document.querySelector(".toast") || {}).textContent,
    card: document.querySelector("#grok").textContent,
    scroll: window.scrollY,
  }));
  ok(st.same === 1, "the page was not reloaded");
  ok(st.name === "" && !st.dirty, "the saved section is clean: " + JSON.stringify(st));
  ok(st.toast === "alias saved", "toast: " + st.toast);
  ok(/1 alias/.test(st.card), "the card behind shows the alias: " + st.card);
  ok(st.scroll > 400, "the page keeps its scroll position: " + st.scroll);
  await pg.click(dlg + "[data-modal-close]");
  await wait(200);
  ok(!(await pg.evaluate(() => document.querySelector("dialog").open)), "a clean dialog closes at once");
  await pg.click("[data-dialog-open='#aliases-grok']");
  await wait(150);
  let q = await pg.evaluate(() => /fast/.test(document.querySelector("dialog [data-section=aliases]").textContent));
  ok(q, "reopened with the saved alias");
  await pg.click(dlg + "[data-modal-close]");
  await wait(150);
  await pg.click("[data-dialog-open='#helper-model']");
  await pg.waitForFunction(() => document.querySelector("dialog[open] select[name=model]") && !document.querySelector("[data-saving]"), { timeout: 5000 });
  await pg.select("dialog[open] select[name=model]", "grok-mini");
  await pg.waitForFunction(() => /grok-mini/.test(document.querySelector("#grok").textContent), { timeout: 5000 });
  await pg.evaluate(() => {
    const el = document.querySelector("dialog[open] input[name=timeout]");
    el.value = "240";
    el.dispatchEvent(new Event("change", { bubbles: true }));
  });
  await pg.waitForFunction(() => Array.prototype.some.call(document.querySelectorAll(".toast"), n => /timeout 240/.test(n.textContent)), { timeout: 5000 });
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent }));
  ok(q.open && q.title === "MCP helper model", "the helper dialog stays open: " + JSON.stringify(q));
  await pg.click(dlg + "[data-modal-close]");
  await wait(200);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent }));
  ok(!q.open && q.title !== "Discard changes", "a saved helper closes without asking: " + JSON.stringify(q));
  await pg.click("[data-dialog-open='#helper-model']");
  await pg.waitForFunction(() => {
    const el = document.querySelector("dialog[open] input[name=timeout]");
    return el && el.value === "240" && !document.querySelector("[data-saving]");
  }, { timeout: 5000 });
  await pg.click(dlg + "[data-modal-close]");
  await wait(200);
  await pg.click("#alias-overview a.name");
  await pg.waitForSelector("dialog[open] tr[data-current]");
  q = await pg.evaluate(() => ({
    title: document.querySelector("[data-modal-title]").textContent,
    row: document.querySelector("dialog[open] tr[data-current]").textContent,
  }));
  ok(q.title === "Model aliases" && /fast/.test(q.row), "the alias name opens its row: " + JSON.stringify(q));
  await pg.click(dlg + "[data-modal-close]");
  await wait(200);
  // Toasts sit in front of the page and leave after a click. Clear them so Delete can be clicked.
  await pg.evaluate(() => document.querySelectorAll(".toast").forEach(t => t.click()));
  await wait(400);
  await pg.click("#alias-overview button.danger");
  await pg.waitForFunction(() => !/fast/.test(document.querySelector("#alias-overview").textContent)
    && /no aliases/.test(document.querySelector("#grok").textContent), { timeout: 5000 });
  ok(!(await pg.evaluate(() => document.querySelector("dialog").open)), "delete does not ask");
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
