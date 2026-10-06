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
  await pg.evaluate(() => { window.__same = 1; });
  const dlg = "dialog[open] ";
  await pg.type(dlg + "input[type=text][name=name]", "fast");
  await pg.click(dlg + "form[action$='/aliases/put'] button.btn");
  await pg.waitForFunction(() => /fast/.test(document.querySelector("dialog [data-section=aliases] tbody").textContent), { timeout: 5000 });
  const st = await pg.evaluate(() => ({
    same: window.__same,
    name: document.querySelector("dialog input[type=text][name=name]").value,
    dirty: document.querySelector("dialog [data-section=aliases]").hasAttribute("data-dirty"),
    toast: (document.querySelector(".toast") || {}).textContent,
    card: document.querySelector("#grok").textContent,
  }));
  ok(st.same === 1, "the page was not reloaded");
  ok(st.name === "" && !st.dirty, "the saved section is clean: " + JSON.stringify(st));
  ok(st.toast === "alias saved", "toast: " + st.toast);
  ok(/1 alias/.test(st.card), "the card behind shows the alias: " + st.card);
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
  await pg.waitForSelector("dialog[open] input[name=timeout]");
  // A number input does not select its text on a triple click (that turned 120 into 120240).
  // Clear it, then type the new value so the keystrokes are what the page sees.
  await pg.evaluate(() => { document.querySelector("dialog[open] input[name=timeout]").value = ""; });
  await pg.type(dlg + "input[name=timeout]", "240");
  await pg.click(dlg + "[data-modal-close]");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent, text: document.querySelector("[data-modal-text]").textContent, url: location.hash }));
  ok(q.open && q.title === "Discard changes" && /MCP helper model/.test(q.text) && q.url === "#helper-model", "Close asks: " + JSON.stringify(q));
  await pg.keyboard.press("Escape");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, timeout: document.querySelector("dialog input[name=timeout]").value, shown: !document.querySelector("[data-modal-body]").hidden }));
  ok(q.open && q.shown && q.timeout === "240", "Escape on the question returns to the edit: " + JSON.stringify(q));
  await pg.keyboard.press("Escape");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent }));
  ok(q.open && q.title === "Discard changes", "Escape on the content asks too: " + JSON.stringify(q));
  await pg.click("[data-modal-ok]");
  await wait(200);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, hash: location.hash }));
  ok(!q.open && q.hash === "", "Discard closes and clears the address: " + JSON.stringify(q));
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
