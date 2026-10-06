// The MCP helper model dialog opened from an upstream's tools page and its edit page: changing the model, the
// reasoning and the timeout each stays changed in the dialog, survives reopening it, and is saved (the page read
// again shows it). Usage: node helperedit.js <base> <chrome> <puppeteer-core> <cookies> <path>...
const [url, chrome, pp, cookiesJSON, ...paths] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) { bad.push(m); console.log("FAIL", m); } else console.log("ok  ", m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 1280, height: 900 });
  const body = "[data-modal] [data-modal-body]";
  const read = () => pg.evaluate(s => {
    const bd = document.querySelector(s);
    const f = bd && bd.querySelector('form[action$="/model"]');
    if (!f) return null;
    return { model: f.querySelector('[name=model]').value, effort: f.querySelector('[name=effort]').value, timeout: f.querySelector('[name=timeout]').value };
  }, body);
  const idle = async () => { for (let i = 0; i < 50; i++) { if (!(await pg.evaluate(() => !!document.querySelector("form[data-saving]")))) break; await wait(100); } await wait(300); };
  // the dialog reloads the model list when it opens (deferred, then posted): pick only once that has answered and
  // the dialog was drawn again from it, as a person would see the list first
  const open = async () => {
    await pg.evaluate(() => {
      window.__reload = null;
      if (window.__watch) return;
      window.__watch = true;
      document.addEventListener("submit", e => { if (e.target.matches('form[action$="/models/reload"]')) window.__reload = e.target; }, true);
    });
    await pg.click('[data-dialog-open="#helper-model"]');
    await pg.waitForSelector(body + ' form[action$="/model"]', { visible: true });
    await pg.waitForFunction(() => window.__reload && !window.__reload._saving, { timeout: 10000 });
    await idle();
  };
  const close = async () => { await pg.click("[data-modal] [data-modal-close]"); await wait(150); };
  const setSel = async (name, v) => { await pg.select(body + ` [name=${name}]`, v); await idle(); };
  const setNum = async (v) => {
    const sel = body + " [name=timeout]";
    await pg.focus(sel);
    await pg.evaluate(s => document.querySelector(s).select(), sel);
    await pg.keyboard.press("Backspace");
    await pg.type(sel, v);
    await pg.keyboard.press("Tab");
    await idle();
  };
  let n = 0;
  for (const path of paths) {
    const steps = [
      ["model", n % 2 ? "helper-2" : "helper-1"],
      ["effort", n % 2 ? "low" : "high"],
      ["timeout", String(400 + n)],
      ["model", n % 2 ? "helper-1" : "helper-2"],
    ];
    n++;
    await pg.goto(url + path);
    await open();
    let want = await read();
    ok(want, path + ": the dialog shows the helper form " + JSON.stringify(want));
    for (const [k, v] of steps) {
      if (k === "timeout") await setNum(v); else await setSel(k, v);
      want[k] = v;
      const got = await read();
      ok(JSON.stringify(got) === JSON.stringify(want), path + `: after ${k}=${v} the dialog keeps every value: ` + JSON.stringify(got) + " want " + JSON.stringify(want));
    }
    await close();
    await open();
    let got = await read();
    ok(JSON.stringify(got) === JSON.stringify(want), path + ": reopened, the dialog shows what was chosen: " + JSON.stringify(got));
    const label = await pg.evaluate(() => { const o = document.querySelector('[data-dialog-open="#helper-model"]'); return o && o.textContent.trim(); });
    ok(label === "MCP helper model: " + want.model, path + ": the opener names the model: " + label);
    await close();
    await pg.goto(url + path);
    await open();
    got = await read();
    ok(JSON.stringify(got) === JSON.stringify(want), path + ": after a reload, it was saved: " + JSON.stringify(got));
    await close();
    console.log("SAVED " + path + " " + JSON.stringify(want));
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
