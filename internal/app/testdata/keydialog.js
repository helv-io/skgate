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
  // layout: the rate label sits above its field; the Danger zone (regenerate, revoke) is framed below Save, above Details
  const lay = await pg.evaluate(() => {
    const body = document.querySelector("[data-modal-body]"), top = e => e.getBoundingClientRect().top, bottom = e => e.getBoundingClientRect().bottom;
    const rate = body.querySelector("input[name=rate]"), lab = rate.closest("label"), save = body.querySelector("form[action$='/keys/update'] button.btn");
    const range = document.createRange(); range.selectNodeContents(lab.firstChild);
    const zone = body.querySelector(".danger-zone"), det = [...body.querySelectorAll("h4")].find(h => h.textContent.trim() === "Details");
    return { labelAboveField: range.getBoundingClientRect().bottom <= top(rate) + 1, zone: !!zone, head: zone && zone.querySelector("h4").textContent.trim(),
      belowSave: zone && top(zone) > bottom(save), aboveDetails: zone && det && bottom(zone) <= top(det),
      both: zone && [...zone.querySelectorAll("button")].map(x => x.textContent.trim()).join(","), border: zone && getComputedStyle(zone).borderTopColor,
      outside: zone && body.querySelector("form[action$='/keys/update']").contains(zone) };
  });
  ok(lay.labelAboveField, "the rate label is above its field " + JSON.stringify(lay));
  // item 9: unreadable expiry text in the dialog's own form marks that field aria-invalid too, and clearing lifts it
  await pg.type("[data-modal-body] [data-expiry-input]", "banana");
  await wait(600);
  let inv = await pg.evaluate(() => { const i = document.querySelector("[data-modal-body] [data-expiry-input]"); return [i.getAttribute("aria-invalid"), i.form.checkValidity()]; });
  ok(inv[0] === "true" && inv[1] === false, "an unreadable expiry in the dialog is aria-invalid and blocks Save " + inv);
  await pg.$eval("[data-modal-body] [data-expiry-input]", e => { e.value = ""; e.dispatchEvent(new Event("input", { bubbles: true })); });
  await wait(600);
  inv = await pg.evaluate(() => { const i = document.querySelector("[data-modal-body] [data-expiry-input]"); return [i.getAttribute("aria-invalid"), i.form.checkValidity()]; });
  ok(inv[0] === null && inv[1] === true, "clearing it lifts the mark " + inv);
  ok(lay.zone && lay.head === "Danger zone" && lay.belowSave && lay.aboveDetails && !lay.outside && lay.both === "Regenerate,Revoke", "the Danger zone is below Save and above Details with both actions " + JSON.stringify(lay));
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
