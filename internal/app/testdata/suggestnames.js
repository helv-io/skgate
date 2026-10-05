// Suggest names on the tools page: the choice dialog, Selected disabled when none are on, All, and a filled name.
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

  // open the choice: Selected is off with none ticked, All is on
  await pg.click("[data-tool-describe]");
  await pg.waitForSelector("[data-modal][open] [data-suggest-selected], [data-modal].open [data-suggest-selected], dialog[open] [data-suggest-selected]", { timeout: 5000 }).catch(() => {});
  const open = await pg.evaluate(() => {
    const body = document.querySelector("[data-modal-body]");
    const sel = body && body.querySelector("[data-suggest-selected]");
    const all = body && body.querySelector("[data-suggest-all]");
    const title = document.querySelector("[data-modal-title]");
    const text = body ? body.textContent : "";
    return {
      title: title ? title.textContent : "",
      sel: sel ? { text: sel.textContent, disabled: sel.disabled } : null,
      all: all ? { text: all.textContent, disabled: all.disabled } : null,
      cancel: !!(body && body.querySelector("[data-suggest-cancel]")),
      body: /Names and one-line descriptions/.test(text) && /Review them, then Save/.test(text),
      waitHidden: body && body.querySelector("[data-suggest-wait]") && body.querySelector("[data-suggest-wait]").hidden,
    };
  });
  ok(open.title === "Suggest names" && open.body && open.cancel, "modal copy: " + JSON.stringify(open));
  ok(open.sel && open.sel.text === "Selected" && open.sel.disabled, "Selected is off with none on: " + JSON.stringify(open.sel));
  ok(open.all && open.all.text === "All" && !open.all.disabled, "All is on: " + JSON.stringify(open.all));

  // cancel closes
  await pg.click("[data-suggest-cancel]");
  await pg.waitForFunction(() => !document.querySelector("[data-modal]").open, { timeout: 5000 });

  // switch one tool on, open again: Selected 1
  await pg.click("[data-tool-on]");
  await pg.click("[data-tool-describe]");
  await pg.waitForFunction(() => {
    const s = document.querySelector("[data-modal-body] [data-suggest-selected]");
    return s && s.textContent === "Selected 1" && !s.disabled;
  }, { timeout: 5000 });

  // Selected runs and fills
  await pg.click("[data-suggest-selected]");
  await pg.waitForFunction(() => /Names filled for 1 tool/.test(document.body.textContent), { timeout: 15000 });
  const filled = await pg.evaluate(() => {
    const row = document.querySelector("[data-tool] input[data-tool-on]:checked")?.closest("[data-tool]");
    return row ? { name: row.querySelector("[data-tool-name]").value, desc: row.querySelector("[data-tool-desc]").value, open: document.querySelector("[data-modal]").open } : null;
  });
  ok(filled && filled.name === "list_my_notes" && /note/i.test(filled.desc) && !filled.open, "Selected fills and closes: " + JSON.stringify(filled));

  // phone width: All still usable
  await pg.setViewport({ width: 360, height: 740, isMobile: true, hasTouch: true });
  await pg.goto(url + "/admin/upstreams/" + alias + "/tools");
  await pg.click("[data-tool-describe]");
  const phone = await pg.evaluate(() => {
    const all = document.querySelector("[data-modal-body] [data-suggest-all]");
    const r = all && all.getBoundingClientRect();
    return { shown: !!all && all.offsetParent !== null, w: r && r.width, inView: r && r.left >= 0 && r.right <= innerWidth + 1 };
  });
  ok(phone.shown && phone.inView, "All fits at 360 px: " + JSON.stringify(phone));

  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
