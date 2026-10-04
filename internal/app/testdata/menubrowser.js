const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const wait = ms => new Promise(r => setTimeout(r, ms));
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  const state = () => pg.evaluate(() => {
    const m = document.querySelector("#menu-alpha"), r = m.getBoundingClientRect(), a = document.activeElement;
    const first = m.querySelector("[role=menuitem]"), fr = first.getBoundingClientRect();
    const hit = m.hidden ? null : document.elementFromPoint(fr.left + fr.width / 2, fr.top + fr.height / 2);
    return { hidden: m.hidden, inside: r.left >= 0 && r.top >= 0 && r.right <= innerWidth && r.bottom <= innerHeight, hit: hit === first || (hit && first.contains(hit)),
      focus: a.id || a.getAttribute("data-menu-button") !== null && "button" || a.textContent.trim(), expanded: m.parentNode.querySelector("[data-menu-button]").getAttribute("aria-expanded") };
  });
  const btn = "tr:has(a[href='#upstream-alpha']) [data-menu-button]";
  const item = t => `#menu-alpha [role=menuitem]`;
  // desktop
  await pg.setViewport({ width: 1280, height: 800 });
  await pg.goto(url + "/admin/upstreams");
  await pg.click(btn);
  let s = await state();
  ok(!s.hidden && s.inside && s.hit && s.expanded === "true", "opens, inside the window, not clipped by the table: " + JSON.stringify(s));
  await pg.keyboard.press("ArrowDown");
  await pg.keyboard.press("Escape");
  s = await state();
  ok(s.hidden && s.focus === "button" && s.expanded === "false", "Escape closes and the focus is back on the button: " + JSON.stringify(s));
  await pg.click(btn);
  await pg.mouse.click(5, 5);
  ok((await state()).hidden, "a click outside closes it");
  // Delete asks first; Cancel returns to the button
  await pg.click(btn);
  const del = await pg.$("#menu-alpha form[action$='/delete'] button");
  await del.click();
  await wait(150);
  let q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent, menu: document.querySelector("#menu-alpha").hidden }));
  ok(q.open && q.title === "Delete" && q.menu, "Delete opens the confirmation and closes the menu: " + JSON.stringify(q));
  await pg.click("[data-modal-cancel]");
  await wait(150);
  s = await state();
  ok(s.focus === "button", "Cancel returns the focus to the menu button: " + JSON.stringify(s));
  // Details opens the dialog
  await pg.click(btn);
  await pg.click("#menu-alpha [data-dialog-open]");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, title: document.querySelector("[data-modal-title]").textContent, hash: location.hash, acts: [...document.querySelectorAll("dialog [data-modal-body] .actions > *")].map(e => e.textContent.trim()) }));
  ok(q.open && q.title === "Upstream alpha" && q.hash === "#upstream-alpha", "Details opens the dialog: " + JSON.stringify(q));
  ok(q.acts.join() === "copy URL,test,detect,edit,delete" || q.acts.join() === "test,detect,edit,delete" , "the dialog has the row's actions: " + q.acts);
  await pg.keyboard.press("Escape");
  await wait(150);
  // the name opens the same dialog
  await pg.click("a[href='#upstream-alpha']");
  await wait(150);
  q = await pg.evaluate(() => ({ open: document.querySelector("dialog").open, hash: location.hash, n: history.length }));
  ok(q.open && q.hash === "#upstream-alpha", "the name opens the details: " + JSON.stringify(q));
  await pg.keyboard.press("Escape");
  await wait(150);
  // phone: a tap opens it and a tap on Edit goes to the edit page
  await pg.setViewport({ width: 390, height: 800, hasTouch: true, isMobile: true });
  await pg.goto(url + "/admin/upstreams");
  const cards = await pg.evaluate(() => [...document.querySelectorAll("main .table:not(.kv) tbody tr")].map(r => Math.round(r.getBoundingClientRect().height)));
  ok(cards.length === 2 && cards.every(h => h <= 270), "a phone card stays short (two lines of 44px controls at most, one row of actions): " + cards);
  await (await pg.$(btn)).tap(); // scrolls it into view first
  await wait(150);
  s = await state();
  ok(!s.hidden && s.inside && s.hit, "a tap opens the menu on a phone: " + JSON.stringify(s));
  const eb = await (await pg.$("#menu-alpha a[href$='/edit']")).boundingBox();
  ok(eb.height >= 44, "menu items are tap-sized: " + eb.height);
  await Promise.all([pg.waitForNavigation(), (await pg.$("#menu-alpha a[href$='/edit']")).tap()]);
  ok(pg.url().endsWith("/admin/upstreams/alpha/edit"), "a tap on Edit goes to the edit page: " + pg.url());
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
