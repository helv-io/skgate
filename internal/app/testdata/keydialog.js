// Regenerate and revoke are forms inside a key's dialog. Real browser: Cancel and Escape go back to the dialog,
// Confirm submits the form (a form taken out of the page would not submit), Save in the same dialog saves.
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
  await pg.goto(url + "/admin/keys");
  const state = () => pg.evaluate(() => {
    const d = document.querySelector("[data-modal]");
    const vis = e => !!e && !e.hidden && e.offsetParent !== null;
    return { open: d.open, hash: location.hash, title: d.querySelector("[data-modal-title]").textContent,
      body: vis(d.querySelector("[data-modal-body]")), text: vis(d.querySelector("[data-modal-text]")),
      name: !!d.querySelector("[data-modal-body] input[name=label]") };
  });
  const clickIn = sel => pg.evaluate(s => document.querySelector(s).click(), sel);
  await clickIn("[data-dialog-open]");
  await wait(150);
  let s = await state();
  ok(s.open && s.body && s.name && /^#key-/.test(s.hash), "edit opens the dialog with the form " + JSON.stringify(s));
  const dialogTitle = s.title;
  // Cancel goes back to the dialog
  await clickIn("[data-modal-body] form[action$='/keys/revoke'] button");
  await wait(150);
  s = await state();
  ok(s.open && !s.body && s.text && s.title === "Revoke" && /^#key-/.test(s.hash), "revoke asks first, the dialog is kept " + JSON.stringify(s));
  await clickIn("[data-modal-cancel]");
  s = await state();
  ok(s.open && s.body && s.name && !s.text && s.title === dialogTitle, "Cancel goes back to the dialog " + JSON.stringify(s));
  // Escape goes back too
  await clickIn("[data-modal-body] form[action$='/keys/regenerate'] button");
  await wait(100);
  await pg.keyboard.press("Escape");
  await wait(100);
  s = await state();
  ok(s.open && s.body && s.name && !s.text, "Escape goes back to the dialog " + JSON.stringify(s));
  // Save saves
  await pg.evaluate(() => { document.querySelector("[data-modal-body] input[name=label]").value = "renamed in browser"; });
  await Promise.all([pg.waitForNavigation(), clickIn("[data-modal-body] form[action$='/keys/update'] button.btn")]);
  await wait(150);
  s = await state();
  ok(!s.open && s.hash === "", "Save leaves the dialog closed and the address clean " + JSON.stringify(s));
  ok(await pg.evaluate(() => document.body.textContent.includes("renamed in browser")), "the new name is listed");
  // Confirm submits
  await clickIn("[data-dialog-open]");
  await wait(150);
  await clickIn("[data-modal-body] form[action$='/keys/revoke'] button");
  await wait(100);
  await Promise.all([pg.waitForNavigation(), clickIn("[data-modal-ok]")]);
  await wait(150);
  s = await state();
  ok(!s.open && s.hash === "", "Revoke leaves the dialog closed and the address clean " + JSON.stringify(s));
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
