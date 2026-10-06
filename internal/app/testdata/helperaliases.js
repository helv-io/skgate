// The MCP helper model picker in a real browser, wherever it is rendered: no blank entry (the first is "none", and
// the shown entry always has text), the provider's aliases listed by name in an Aliases group, and picking an alias
// saves it and keeps it selected after a reload. Usage: node helperaliases.js <base> <chrome> <puppeteer-core> <cookies> <alias> <path#dialog>...
const [url, chrome, pp, cookiesJSON, alias, ...targets] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) { bad.push(m); console.log("FAIL", m); } else console.log("ok  ", m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 1280, height: 900 });
  const sel = 'dialog[open] [data-modal-body] select[name="model"]';
  const idle = async () => { for (let i = 0; i < 50; i++) { if (!(await pg.evaluate(() => !!document.querySelector("form[data-saving]")))) break; await wait(100); } await wait(300); };
  const read = () => pg.evaluate(s => {
    const el = document.querySelector(s);
    if (!el) return null;
    return {
      value: el.value,
      shown: el.selectedOptions[0] ? el.selectedOptions[0].textContent : "",
      options: [...el.options].map(o => ({ v: o.value, t: o.textContent, g: o.parentElement.tagName === "OPTGROUP" ? o.parentElement.label : "" })),
    };
  }, sel);
  const open = async (path, hash) => {
    await pg.goto(url + path + hash);
    await pg.waitForSelector(sel, { visible: true });
    await idle();
  };
  for (const t of targets) {
    const [path, h] = t.split("#");
    const hash = "#" + h;
    await open(path, hash);
    let s = await read();
    ok(s && s.options.length > 1, t + ": the picker has entries");
    ok(s.options[0].v === "" && s.options[0].t === "none", t + ": the first entry is none: " + JSON.stringify(s.options[0]));
    ok(s.options.every(o => o.t.trim() !== ""), t + ": no blank entry: " + JSON.stringify(s.options.filter(o => !o.t.trim())));
    ok(s.options.filter(o => o.v === "").length === 1, t + ": one empty value only");
    ok(s.shown.trim() !== "", t + ": the shown entry has text: " + JSON.stringify(s.shown));
    const a = s.options.find(o => o.v === alias);
    ok(a && a.t === alias && a.g === "Aliases", t + ": the alias is listed by name in Aliases: " + JSON.stringify(a));
    ok(!s.options.some(o => /\(alias of|\(/.test(o.t)), t + ": no parenthetical labels");
    await pg.select(sel, alias);
    await idle();
    s = await read();
    ok(s.value === alias && s.shown === alias, t + ": picking the alias keeps it shown: " + JSON.stringify([s.value, s.shown]));
    await open(path, hash);
    s = await read();
    ok(s.value === alias && s.shown === alias, t + ": after a reload the alias is still the helper: " + JSON.stringify([s.value, s.shown]));
    ok(s.options[0].t === "none", t + ": after a reload nothing sits above none");
    await pg.select(sel, "");
    await idle();
    await open(path, hash);
    s = await read();
    ok(s.value === "" && s.shown === "none", t + ": cleared, the picker shows none: " + JSON.stringify([s.value, s.shown]));
  }
  await b.close();
  if (bad.length) { console.log(bad.join("\n")); process.exit(1); }
  console.log("ALL OK");
})().catch(e => { console.error(e); process.exit(1); });
