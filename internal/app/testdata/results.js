// The actions that used to load a page save in place: a secret or an outcome shown once opens as a dialog over the
// page, a Save draws its part again, the Grok card follows its sign-in. Each: no navigation, the scroll stays, no
// toast for a good answer, the server has the new state. A secret is in the page only while its dialog is open.
// Phone and desktop widths.
// Usage: node results.js <base> <chrome> <puppeteer-core> <cookies> <issuer URL> <spec host URL>
const [url, chrome, pp, cookiesJSON, issuer, specHost] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) { bad.push(m); console.log("FAIL", m); } else console.log("ok  ", m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  pg.on("dialog", d => d.accept());
  await pg.setCookie(...JSON.parse(cookiesJSON));
  let navs = 0; // full loads only: a fragment change (a dialog opening) is not a navigation
  pg.on("request", r => { if (r.isNavigationRequest() && r.frame() === pg.mainFrame()) navs++; });
  if (process.env.INPLACE_DEBUG) {
    pg.on("console", m => console.log("console:", m.text()));
    pg.on("response", r => { if (r.request().method() === "POST") console.log("POST", r.url(), r.status()); });
  }
  const fresh = async () => { await pg.evaluate(() => { window.__stay = 1; }); navs = 0; };
  const open = async (path, w) => {
    await pg.setViewport({ width: w, height: 560, isMobile: w < 720, hasTouch: w < 720 });
    await pg.goto(url + path);
    await fresh();
  };
  const scrollTo = sel => pg.evaluate(s => {
    const el = document.querySelector(s);
    const r = el.getBoundingClientRect();
    window.scrollTo(0, Math.max(0, window.scrollY + r.top - 200));
    return window.scrollY;
  }, sel);
  const settled = async () => {
    for (let i = 0; i < 200; i++) {
      if (!(await pg.evaluate(() => !!document.querySelector("form[data-saving]")))) break;
      await wait(50);
    }
    await wait(300);
  };
  const stayed = async (tag, y) => {
    const s = await pg.evaluate(() => ({ stay: window.__stay === 1, y: window.scrollY, max: document.documentElement.scrollHeight - window.innerHeight }));
    ok(s.stay && navs === 0, tag + ": no navigation (navs " + navs + ")");
    // the scroll stays; only a page that got shorter (the sign-in panel gone) can hold it lower, at its new end
    const want = Math.min(y, Math.max(0, s.max));
    ok(Math.abs(s.y - want) <= 1, tag + ": scroll kept " + y + " -> " + s.y + (want < y ? " (page end " + want + ")" : ""));
  };
  const confirm = async () => {
    await pg.waitForSelector("[data-modal][open] [data-modal-ok]", { visible: true });
    await pg.click("[data-modal][open] [data-modal-ok]");
  };
  const server = path => pg.evaluate(p => fetch(p, { cache: "no-store" }).then(r => r.text()), path);
  const toasts = () => pg.evaluate(() => [...document.querySelectorAll(".toast")].map(t => t.textContent.trim()));
  const quiet = async tag => { const t = await toasts(); ok(t.length === 0, tag + ": no toast " + JSON.stringify(t)); };
  const dismiss = async () => {
    await pg.evaluate(() => document.querySelectorAll(".toast").forEach(t => t.click()));
    for (let i = 0; i < 40 && (await toasts()).length; i++) await wait(50);
  };
  const setVal = (sel, v) => pg.$eval(sel, (e, v) => { e.value = v; e.dispatchEvent(new Event("input", { bubbles: true })); e.dispatchEvent(new Event("change", { bubbles: true })); }, v);
  // clicks sel and returns its box (and the boxes of more) just before and right after the click, while it posts
  const clickMeasured = (sel, more) => pg.evaluate((s, ss) => {
    const box = () => [s].concat(ss).map(q => {
      const e = document.querySelector(q);
      if (!e) return q + ":none";
      const r = e.getBoundingClientRect();
      return [r.left, r.top, r.width, r.height].map(Math.round).join(",");
    }).join(" ");
    const before = box();
    document.querySelector(s).click();
    return { before, during: box() };
  }, sel, more || []);
  // the one-time dialog: its copy boxes, the address, and every copy box of the page with that text
  const shown = id => pg.evaluate(id => {
    const d = document.querySelector("[data-modal][open]");
    const body = d && d.querySelector("[data-modal-body]");
    return {
      open: !!d, hash: location.hash, title: d ? d.querySelector("[data-modal-title]").textContent : "",
      text: body ? body.textContent : "",
      copies: body ? [...body.querySelectorAll(".copybox")].map(c => c.getAttribute("data-copy-text")) : [],
      tpl: !!document.getElementById(id),
    };
  }, id);
  const everywhere = secret => pg.evaluate(s => ({
    boxes: [...document.querySelectorAll("[data-copy-text]")].filter(e => e.getAttribute("data-copy-text") === s).map(e => !!e.closest("[data-modal]")),
    html: document.documentElement.outerHTML.split(s).length - 1,
  }), secret);
  const waitDialog = async sel => {
    await pg.waitForSelector("[data-modal][open] [data-modal-body] " + sel, { visible: true, timeout: 15000 });
    await wait(150);
  };
  const closeDialog = async () => {
    await pg.click("[data-modal][open] [data-modal-close]");
    for (let i = 0; i < 40 && (await pg.$("[data-modal][open]")); i++) await wait(50);
    await wait(300); // the close goes back in history: let it land
  };
  // the secret is gone with its dialog: not in the page, not in its address, not on the server's page
  const gone = async (tag, id, secret, path) => {
    const s = await shown(id);
    ok(!s.open && s.hash === "" && !s.tpl, tag + ": the dialog closed with its address and template " + JSON.stringify({ open: s.open, hash: s.hash, tpl: s.tpl }));
    const e = await everywhere(secret);
    ok(e.html === 0, tag + ": the secret is no longer in the page (" + e.html + ")");
    ok(!(await server(path)).includes(secret), tag + ": the server's page never has the secret");
  };

  for (const w of [390, 1280]) {
    // Keys: Create key shows the new key once, in a dialog over the list
    await open("/admin/keys", w);
    const label = "fresh " + w;
    await setVal("#key-create input[name=label]", label);
    let y = await scrollTo("#key-create button.btn");
    let m = await clickMeasured("#key-create button.btn", ["#key-rows"]);
    ok(m.before === m.during, w + ": Create key keeps its size while it posts: " + m.before + " -> " + m.during);
    await waitDialog(".copybox");
    let s = await shown("new-key");
    const secret = s.copies[0] || "";
    ok(s.hash === "#new-key" && s.text.includes("New key, shown once") && s.copies.length === 1 && secret.length > 20, w + ": the new key shows once in a dialog " + JSON.stringify({ hash: s.hash, n: s.copies.length, text: s.text.slice(0, 40) }));
    let e = await everywhere(secret);
    ok(e.boxes.length === 1 && e.boxes[0], w + ": the key is only in the dialog " + JSON.stringify(e.boxes));
    await stayed(w + " create key", y);
    await quiet(w + " create key");
    ok(await pg.evaluate(l => [...document.querySelectorAll("#key-rows td.primary")].some(td => td.textContent.trim() === l), label), w + ": the list behind it has the new key");
    ok((await pg.$eval("#key-create input[name=label]", i => i.value)) === "", w + ": the form is empty again");
    ok((await server("/admin/keys")).includes(label), w + ": the key was saved");
    await closeDialog();
    await stayed(w + " close the key dialog", y);
    await gone(w + " new key", "new-key", secret, "/admin/keys");
    await quiet(w + " close the key dialog");

    // Regenerate in the key's dialog: the new secret shows once; the row shows its last four characters
    const kbtn = await pg.evaluate(l => {
      const tr = [...document.querySelectorAll("#key-rows tr")].find(r => r.querySelector("td.primary").textContent.trim() === l);
      return '#key-rows [data-dialog-open="' + tr.querySelector("[data-dialog-open]").getAttribute("data-dialog-open") + '"]';
    }, label);
    y = await scrollTo(kbtn);
    await pg.click(kbtn);
    await pg.waitForSelector("[data-modal][open] form[action='/admin/keys/regenerate'] button", { visible: true });
    await pg.click("[data-modal][open] form[action='/admin/keys/regenerate'] button");
    await confirm();
    await waitDialog(".copybox");
    s = await shown("new-key");
    const secret2 = s.copies[0] || "";
    ok(s.hash === "#new-key" && s.text.includes("New key, shown once") && secret2 && secret2 !== secret, w + ": Regenerate shows the new key once in a dialog");
    await stayed(w + " regenerate", y);
    await quiet(w + " regenerate");
    const masked = await pg.$eval(kbtn, b => b.closest("tr").querySelector("code").textContent);
    ok(masked.endsWith(secret2.slice(-4)), w + ": the row shows the new key's end " + masked);
    await closeDialog();
    await stayed(w + " close the regenerated key", y);
    await gone(w + " regenerated key", "new-key", secret2, "/admin/keys");
    ok(!(await pg.$("[data-modal][open]")), w + ": nothing reopens after the close");

    // a refused create keeps what was typed, with the reason as a toast
    y = await scrollTo("#key-create button.btn");
    await pg.$eval("#key-create details", d => { d.open = true; });
    await setVal("#key-create input[name=label]", "kept");
    await setVal("#key-create input[name=rate]", "2000000000");
    await pg.click("#key-create button.btn");
    await settled();
    await stayed(w + " refused create", y);
    const tt = await toasts();
    ok(tt.length === 1 && tt[0].includes("rate limit"), w + ": a refused create says why " + JSON.stringify(tt));
    ok((await pg.$eval("#key-create input[name=label]", i => i.value)) === "kept" && !(await pg.$("[data-modal][open]")), w + ": and keeps the form, with no dialog");
    await dismiss();
    await setVal("#key-create input[name=label]", "");
    await setVal("#key-create input[name=rate]", "");

    // Clients: Create shows the ID and the secret once
    await open("/admin/clients", w);
    const cname = "app " + w;
    await setVal("#client-create input[name=name]", cname);
    await setVal("#client-create textarea[name=redirects]", "https://app.example/cb");
    y = await scrollTo("#client-create button.btn");
    m = await clickMeasured("#client-create button.btn");
    ok(m.before === m.during, w + ": Create keeps its size while it posts");
    await waitDialog(".copybox");
    s = await shown("new-client");
    const [cid, csecret] = s.copies;
    ok(s.hash === "#new-client" && s.text.includes("Client created, secret shown once") && s.copies.length === 2 && /^skc-/.test(cid || "") && (csecret || "").length >= 40, w + ": the client ID and secret show once in a dialog");
    e = await everywhere(csecret);
    ok(e.boxes.length === 1 && e.boxes[0], w + ": the secret is only in the dialog");
    await stayed(w + " create client", y);
    await quiet(w + " create client");
    ok(await pg.evaluate(n => [...document.querySelectorAll("#client-rows td.primary")].some(td => td.textContent.trim() === n), cname), w + ": the list behind it has the client");
    ok((await pg.$eval("#client-create input[name=name]", i => i.value)) === "", w + ": the form is empty again");
    ok((await server("/admin/clients")).includes(cname), w + ": the client was saved");
    await closeDialog();
    await stayed(w + " close the client dialog", y);
    await gone(w + " new client", "new-client", csecret, "/admin/clients");

    // OpenAPI tools: Save draws the list again in place, keeps the open groups, and the server has it
    await open("/admin/upstreams/big/tools", w);
    const row = k => '[data-tool][data-key="' + k + '"]';
    await pg.$eval('details[data-verbgroup="POST"]', d => { d.open = true; });
    const tname = "renamed_" + w;
    await setVal(row("GET /items25") + " input[name=name]", tname);
    await pg.$eval(row("GET /items24") + " input[data-tool-on]", c => { if (c.checked) c.click(); });
    await pg.$eval(row("POST /notes") + " input[data-tool-on]", c => { if (!c.checked) c.click(); });
    const count = await pg.$eval("[data-toolcount-n]", n => n.textContent);
    const save = "form[data-toolpick] > .row button.btn";
    y = await scrollTo(save);
    ok(y > 300, w + ": the tools page is scrolled down " + y);
    m = await clickMeasured(save, ["[data-toolcount]"]);
    ok(m.before === m.during, w + ": Save keeps its size while it posts");
    await settled();
    await stayed(w + " tools save", y);
    await quiet(w + " tools save");
    let st = await pg.evaluate((r25, r24, rp) => {
      const n = document.querySelector(r25 + " input[name=name]"), c = document.querySelector(r24 + " input[data-tool-on]"), p = document.querySelector(rp + " input[data-tool-on]");
      return { name: n.value, saved: n.value === n.defaultValue, off: !c.checked && !c.defaultChecked, post: p.checked && p.defaultChecked,
        open: document.querySelector('details[data-verbgroup="POST"]').open, n: document.querySelector("[data-toolcount-n]").textContent };
    }, row("GET /items25"), row("GET /items24"), row("POST /notes"));
    ok(st.name === tname && st.saved && st.off && st.post, w + ": the list shows what was saved " + JSON.stringify(st));
    ok(st.open && st.n === count, w + ": the open group stays open and the counter holds " + JSON.stringify(st));
    ok(!!(await pg.$("template#try-" + tname)), w + ": the renamed tool's tester is there");
    let html = await server("/admin/upstreams/big/tools");
    ok(html.includes('value="' + tname + '"') && html.includes('value="GET /items24" data-tool-on aria-label') && /value="POST \/notes" data-tool-on checked/.test(html), w + ": the selection and the name were saved");

    // the unsaved-changes guard still asks before a link leaves an edited list
    await setVal(row("GET /items3") + " input[name=desc]", "edited " + w);
    await pg.click(".actions a[href$='/edit']");
    await pg.waitForSelector("[data-modal][open] [data-modal-ok]", { visible: true });
    ok((await pg.$eval("[data-modal][open] [data-modal-title]", t => t.textContent)) === "Unsaved changes", w + ": a link asks first while an edit is unsaved");
    await pg.click("[data-modal][open] [data-modal-ok]"); // Stay
    await wait(200);
    ok(navs === 0 && (await pg.$eval(row("GET /items3") + " input[name=desc]", i => i.value)) === "edited " + w, w + ": Stay keeps the page and the edit");

    // Update: the review is a dialog; Confirm applies in place and keeps the unsaved edit
    await fetch(specHost + "/flip");
    const facts = await pg.$eval("#oa-facts", p => p.textContent);
    y = await scrollTo("#oa-update");
    await pg.click("#oa-update button");
    await waitDialog("form[action$='/spec/apply'] button");
    s = await shown("spec-review");
    ok(s.hash === "#spec-review" && s.text.includes("Nothing is replaced until you confirm"), w + ": Update shows the review in a dialog");
    await stayed(w + " update review", y);
    await quiet(w + " update review");
    ok((await server("/admin/upstreams/big/tools")).includes(facts.includes("31 operations") ? "/items29" : "/extra"), w + ": the review stored nothing");
    await pg.click("[data-modal][open] form[action$='/spec/apply'] button");
    await settled();
    await wait(300);
    await stayed(w + " update confirm", y);
    await quiet(w + " update confirm");
    const facts2 = await pg.$eval("#oa-facts", p => p.textContent);
    ok(!(await pg.$("[data-modal][open]")) && !(await pg.$("template#spec-review")) && (await pg.evaluate(() => location.hash)) === "", w + ": the review closed");
    ok(facts2 !== facts && /3[12] operations/.test(facts2), w + ": the page shows the new description: " + facts + " -> " + facts2);
    html = await server("/admin/upstreams/big/tools");
    ok(facts2.includes("32 operations") ? html.includes("/extra2") : !html.includes("/extra2"), w + ": the update was saved");
    ok((await pg.$eval(row("GET /items3") + " input[name=desc]", i => i.value)) === "edited " + w, w + ": an unsaved edit survives the update");
    await pg.click("#oa-update button");
    await settled();
    ok(JSON.stringify(await toasts()) === JSON.stringify(["No changes"]) && !(await pg.$("[data-modal][open]")), w + ": an update with nothing new says so, with no dialog");
    await dismiss();
    await pg.$eval(row("GET /items3") + " input[name=desc]", i => { i.value = i.defaultValue; });

    // Import: the outcome shows in a dialog; the text stays and counts as saved
    await open("/admin/upstreams/import", w);
    await pg.waitForSelector("textarea[name=json]");
    const doc = JSON.stringify({ mcpServers: { ["imp" + w]: { url: "https://h.example.com/mcp" } } });
    await setVal("textarea[name=json]", doc);
    y = await scrollTo("form[action='/admin/upstreams/import'] button.btn");
    await pg.click("form[action='/admin/upstreams/import'] button.btn");
    await waitDialog("table");
    s = await shown("import-result");
    ok(s.hash === "#import-result" && s.text.includes("imp" + w) && s.text.includes("created"), w + ": the import outcome shows in a dialog");
    await stayed(w + " import", y);
    await quiet(w + " import");
    await closeDialog();
    ok(!(await pg.$("template#import-result")) && (await pg.evaluate(() => location.hash)) === "", w + ": the outcome went with its dialog");
    ok((await pg.$eval("textarea[name=json]", t => t.value === t.defaultValue && t.value.includes("imp"))), w + ": the text stays and is not an unsaved edit");
    ok((await server("/admin/upstreams")).includes('id="up-imp' + w + '"'), w + ": the upstream was saved");

    // Grok: Cancel the device sign-in in place; at the wide screen the sign-in completes in place
    await open("/admin", w);
    if (await pg.$("#grok form[action$='/device/start'] button")) {
      await Promise.all([pg.waitForNavigation(), pg.click("#grok form[action$='/device/start'] button")]); // starting stays a load (its window)
      await fresh();
    }
    ok(!!(await pg.$("#grok [data-poll]")), w + ": the sign-in panel is up");
    y = await scrollTo("#grok form[action$='/device/cancel'] button");
    await pg.click("#grok form[action$='/device/cancel'] button");
    await settled();
    await stayed(w + " cancel sign-in", y);
    await quiet(w + " cancel sign-in");
    ok(!(await pg.$("#grok [data-poll]")) && !!(await pg.$("#grok form[action$='/device/start'] button")), w + ": the card offers Sign in again");
    ok(!(await server("/admin")).includes("data-poll"), w + ": the sign-in was cancelled");
    await wait(3500); // the poller saw the panel go and stopped: nothing reloads
    ok(navs === 0, w + ": no reload after the cancel (navs " + navs + ")");
    if (w === 1280) {
      await Promise.all([pg.waitForNavigation(), pg.click("#grok form[action$='/device/start'] button")]);
      await fresh();
      await fetch(issuer + "/grant");
      await pg.waitForSelector('#grok [data-dialog-open="#provider-grok"]', { timeout: 15000 }).catch(() => {});
      ok(!!(await pg.$('#grok [data-dialog-open="#provider-grok"]')) && !(await pg.$("#grok [data-poll]")), w + ": the card shows the account once the sign-in completes");
      ok(navs === 0 && (await pg.evaluate(() => window.__stay === 1)), w + ": the sign-in completed without a reload (navs " + navs + ")");
      ok(!!(await pg.$("template#provider-grok")), w + ": its Details dialog is in the page");
    }
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
