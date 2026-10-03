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
  await pg.goto(url + "/admin#provider-grok");
  await pg.waitForSelector("dialog[open] [data-section=aliases]");
  await pg.evaluate(() => { window.__same = 1; });
  const dlg = "dialog[open] ";
  // an edit in one section, a save in another
  await pg.type(dlg + "input[name=base]", "x");
  await pg.type(dlg + "form[action$='/aliases/put'] input[name=name]", "fast");
  await pg.click(dlg + "form[action$='/aliases/put'] button");
  await pg.waitForFunction(() => /fast/.test(document.querySelector("dialog [data-section=aliases] tbody").textContent), { timeout: 5000 });
  const st = await pg.evaluate(() => ({
    same: window.__same, base: document.querySelector("dialog input[name=base]").value,
    dirty: document.querySelector("dialog [data-section=upstream]").hasAttribute("data-dirty"),
    chip: !document.querySelector("dialog [data-section=upstream] [data-dirty-chip]").hidden,
    other: document.querySelector("dialog [data-section=aliases]").hasAttribute("data-dirty"),
    toast: (document.querySelector(".toast") || {}).textContent, card: document.querySelector("#grok").textContent,
    target: document.querySelector("dialog form[action$='/aliases/put'] select").value,
  }));
  ok(st.same === 1, "the page was not reloaded");
  ok(st.base.startsWith("x") && st.dirty && st.chip, "the edit in the Upstream URLs section survived the alias save: " + JSON.stringify(st));
  ok(!st.other, "the saved section is clean");
  ok(st.toast === "alias saved", "toast: " + st.toast);
  ok(/1/.test(st.card), "the card behind shows the alias");
  ok(st.target === "grok-mini" || st.target === "grok-4.7-reasoning" || st.target === "plain", "target is a model: " + st.target);
  // closing asks; Escape and Close both
  await pg.click(dlg + "[data-modal-close]");
  await wait(150);
  let q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent, text: document.querySelector("[data-modal-text]").textContent, url: location.hash }));
  ok(q.open && q.title === "Discard changes" && /Upstream URLs/.test(q.text) && q.url === "#provider-grok", "Close asks: " + JSON.stringify(q));
  await pg.keyboard.press("Escape");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, base: document.querySelector("dialog input[name=base]").value, shown: !document.querySelector("[data-modal-body]").hidden }));
  ok(q.open && q.shown && q.base.startsWith("x"), "Escape on the question returns to the edits: " + JSON.stringify(q));
  await pg.keyboard.press("Escape");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent }));
  ok(q.open && q.title === "Discard changes", "Escape on the content asks too: " + JSON.stringify(q));
  await pg.click("[data-modal-ok]");
  await wait(200);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, hash: location.hash }));
  ok(!q.open && q.hash === "", "Discard closes and clears the address: " + JSON.stringify(q));
  // the saved alias is there when the dialog is opened again, and a clean dialog closes without asking
  await pg.click("[data-dialog-open='#provider-grok']");
  await wait(150);
  q = await pg.evaluate(() => ({ alias: /fast/.test(document.querySelector("dialog [data-section=aliases]").textContent), base: document.querySelector("dialog input[name=base]").value }));
  ok(q.alias && !q.base.startsWith("x"), "reopened with the saved alias and without the discarded edit: " + JSON.stringify(q));
  await pg.click(dlg + "[data-modal-close]");
  await wait(150);
  ok(!(await pg.evaluate(() => document.querySelector("dialog").open)), "a clean dialog closes at once");
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
