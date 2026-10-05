// The tool tester in a real browser: Test opens a form made from the input schema (required fields marked in words,
// defaults filled, helper text), Run shows the answer, Edit inputs goes back with the values kept, Edit as JSON shows
// the raw arguments, and it all fits a phone.
// Usage: node trytool.js <base url> <chrome> <puppeteer-core dir> <cookies json>
const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 1280, height: 900 });
  await pg.goto(url + "/admin/upstreams/tt/tools");
  await pg.click('[data-dialog-open="#try-getItem"]');
  await pg.waitForSelector("dialog[open] [data-try-form] label", { timeout: 10000 });
  const form = await pg.evaluate(() => {
    const labels = Array.from(document.querySelectorAll("dialog[open] [data-try-form] .field")).map(f => ({
      text: f.querySelector("label").textContent.trim(), help: (f.querySelector("p.muted") || {}).textContent || "",
      tag: f.querySelector("input,select,textarea").tagName + ":" + (f.querySelector("input,select,textarea").type || ""),
      value: f.querySelector("input[type=checkbox]") ? f.querySelector("input").checked : f.querySelector("input,select,textarea").value }));
    return { labels, note: /Run calls the upstream for real\./.test(document.querySelector("dialog[open]").textContent), run: !document.querySelector("dialog[open] [data-try-run]").disabled };
  });
  const by = n => form.labels.find(l => l.text.startsWith(n));
  ok(form.labels.length === 5 && form.note && form.run, "five fields, the note, Run on: " + JSON.stringify(form));
  ok(by("id") && /required/.test(by("id").text) && by("id").tag === "INPUT:text" && by("id").help === "The item id.", "id is required, text, with helper text: " + JSON.stringify(by("id")));
  ok(by("limit") && by("limit").tag === "INPUT:number" && by("limit").value === "10" && !/required/.test(by("limit").text), "limit is a number with its default: " + JSON.stringify(by("limit")));
  ok(by("verbose") && by("verbose").tag === "INPUT:checkbox" && by("verbose").value === false, "verbose is a checkbox: " + JSON.stringify(by("verbose")));
  ok(by("kind") && by("kind").tag.startsWith("SELECT") && by("kind").value === "", "kind is a select: " + JSON.stringify(by("kind")));
  ok(by("tags") && by("tags").tag.startsWith("TEXTAREA"), "tags is a JSON box: " + JSON.stringify(by("tags")));

  // a missing required field stops before anything is sent
  await pg.click("dialog[open] [data-try-run]");
  const miss = await pg.evaluate(() => ({ err: document.querySelector("dialog[open] [data-try-err]").textContent, result: !document.querySelector("dialog[open] [data-try-result]").hidden }));
  ok(miss.err === "id is required" && !miss.result, "required id: " + JSON.stringify(miss));

  // fill and run
  await pg.type('dialog[open] [data-try-form] .field:nth-child(1) input', "abc");
  await pg.click('dialog[open] [data-try-form] input[type=checkbox]');
  await pg.select("dialog[open] [data-try-form] select", JSON.stringify("b"));
  await pg.click("dialog[open] [data-try-run]");
  await pg.waitForFunction(() => !document.querySelector("dialog[open] [data-try-result]").hidden, { timeout: 15000 });
  const res = await pg.evaluate(() => ({ pill: document.querySelector("dialog[open] [data-try-pill] .pill").textContent, cls: document.querySelector("dialog[open] [data-try-pill] .pill").className,
    out: document.querySelector("dialog[open] [data-try-out]").textContent, edit: document.querySelector("dialog[open] [data-try-edit]").hidden }));
  ok(/^OK · \d+ ms$/.test(res.pill) && res.cls.includes("ok") && res.edit, "the pill: " + JSON.stringify(res));
  let echo = {};
  try { echo = JSON.parse(res.out.slice(res.out.indexOf("\n\n") + 2)); } catch (e) { bad.push("the answer is JSON: " + res.out); }
  ok(echo.id === "abc" && echo.limit === "10" && echo.verbose === "true" && echo.kind === "b" && res.out.includes("\n  "), "the upstream got the form values, shown indented: " + res.out);

  // back to the inputs: values kept; JSON view shows them; editing JSON feeds the form
  await pg.click("dialog[open] [data-try-back]");
  await pg.click("dialog[open] [data-try-mode]");
  const raw = await pg.evaluate(() => ({ text: document.querySelector("dialog[open] [data-try-raw]").value, label: document.querySelector("dialog[open] [data-try-mode]").textContent, form: document.querySelector("dialog[open] [data-try-form]").hidden }));
  let rawObj = {};
  try { rawObj = JSON.parse(raw.text); } catch (e) { bad.push("raw arguments are JSON: " + raw.text); }
  ok(rawObj.id === "abc" && rawObj.limit === 10 && rawObj.verbose === true && rawObj.kind === "b" && raw.label === "Edit as form" && raw.form, "raw JSON: " + JSON.stringify(raw));
  await pg.$eval("dialog[open] [data-try-raw]", e => { e.value = JSON.stringify({ id: "zzz", extra: 1 }); });
  await pg.click("dialog[open] [data-try-mode]");
  ok(await pg.evaluate(() => document.querySelector('dialog[open] [data-try-form] .field input').value === "zzz" && document.querySelector("dialog[open] [data-try-mode]").textContent === "Edit as JSON"), "JSON edits come back into the form");
  await pg.$eval("dialog[open] [data-try-mode]", e => e.click());
  ok(await pg.evaluate(() => JSON.parse(document.querySelector("dialog[open] [data-try-raw]").value).extra === 1), "a key the schema does not list is kept");
  await pg.$eval("dialog[open] [data-try-raw]", e => { e.value = "{nope"; });
  await pg.click("dialog[open] [data-try-mode]");
  ok(await pg.evaluate(() => document.querySelector("dialog[open] [data-try-err]").textContent === "The arguments are not valid JSON"), "bad JSON is said, not sent");

  // Close
  await pg.evaluate(() => { document.querySelector("dialog[open] [data-try-edit]").hidden = true; document.querySelector("dialog[open] [data-try-result]").hidden = false; });
  await pg.click("dialog[open] [data-try-close]");
  ok(await pg.evaluate(() => !document.querySelector("dialog[open]")), "Close closes the dialog");

  // a phone: the form fits and Run is reachable
  await pg.setViewport({ width: 360, height: 740, isMobile: true, hasTouch: true });
  await pg.goto(url + "/admin/upstreams/tt/tools#try-getItem");
  await pg.waitForSelector("dialog[open] [data-try-form] label", { timeout: 10000 });
  const mob = await pg.evaluate(() => {
    const d = document.querySelector("dialog[open]"), r = d.getBoundingClientRect(), run = d.querySelector("[data-try-run]").getBoundingClientRect();
    return { dw: Math.round(r.width), vw: window.innerWidth, scroll: d.scrollWidth <= d.clientWidth + 1, runRight: Math.round(run.right), runH: Math.round(run.height) };
  });
  ok(mob.dw <= mob.vw && mob.scroll && mob.runRight <= mob.vw && mob.runH >= 32, "the tester on a phone: " + JSON.stringify(mob));
  await b.close();
  if (bad.length) { console.log(bad.join("\n")); process.exit(1); }
  console.log("ALL OK");
})().catch(e => { console.log(e.stack || String(e)); process.exit(1); });
