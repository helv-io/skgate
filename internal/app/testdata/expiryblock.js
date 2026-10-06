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
  const form = "form[action='/admin/keys/create']";
  ok(await pg.evaluate(f => !document.querySelector(f + " details").open, form), "Limits starts folded");
  await pg.type(form + " input[name=label]", "blocked then fixed");
  await pg.evaluate(f => { document.querySelector(f + " details").open = true; }, form);
  await pg.type(form + " [data-expiry-input]", "banana");
  await wait(500);
  const st = () => pg.evaluate(f => { const i = document.querySelector(f + " [data-expiry-input]"); return { invalid: i.getAttribute("aria-invalid"), border: getComputedStyle(i).borderTopColor, open: document.querySelector(f + " details").open, valid: i.validity.valid }; }, form);
  let s = await st();
  ok(s.invalid === "true" && !s.valid && s.border === "rgb(255, 138, 128)", "unreadable text: aria-invalid, red border, invalid " + JSON.stringify(s));
  // fold the section, then submit: nothing is sent and the section opens again
  await pg.evaluate(f => { document.querySelector(f + " details").open = false; }, form);
  await pg.click(form + " button.btn");
  await wait(600);
  ok(pg.url().endsWith("/admin/keys"), "no navigation after a blocked submit: " + pg.url());
  s = await st();
  ok(s.open, "the folded section opens to show the field");
  // clear it and create
  await pg.evaluate(f => { const i = document.querySelector(f + " [data-expiry-input]"); i.value = ""; i.dispatchEvent(new Event("input", { bubbles: true })); }, form);
  await wait(500);
  s = await st();
  ok(!s.invalid && s.valid, "cleared: valid again " + JSON.stringify(s));
  await pg.click(form + " button.btn");
  await pg.waitForSelector("[data-modal][open] [data-modal-body] .copybox", { visible: true });
  ok(pg.url().endsWith("/admin/keys#new-key"), "created, the key shows in a dialog over the page: " + pg.url());
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
