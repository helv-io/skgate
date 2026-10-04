// The OpenAPI tools page and the add form's check/repair, in a real browser.
// Usage: node toolpick.js <base url> <chrome> <puppeteer-core dir> <cookies json> <alias>
const [url, chrome, pp, cookiesJSON, alias] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
const sleep = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 1280, height: 900 });
  await pg.goto(url + "/admin/upstreams/" + alias + "/tools");
  const state = () => pg.evaluate(() => ({
    n: Number(document.querySelector("[data-toolcount-n]").textContent),
    cls: document.querySelector("[data-toolcount]").className,
    callout: document.querySelector("[data-toolcallout]").className,
    word: document.querySelector("[data-toolcount-level]").textContent,
    toasts: [...document.querySelectorAll(".toast")].map(t => t.className + "|" + t.textContent),
    groups: Object.fromEntries([...document.querySelectorAll("[data-verbgroup]")].map(g => [g.dataset.verbgroup, g.querySelector("[data-verb-count]").textContent])),
  }));
  let s = await state();
  ok(s.n === 0 && s.cls === "toolcount ok" && s.callout === "callout ok", "starts at zero and green: " + JSON.stringify(s));
  ok(s.toasts.length === 0, "no toast on load: " + JSON.stringify(s.toasts));

  // switch a whole read verb on: 20 tools is amber, with a toast for the group and one for the level
  await pg.click('[data-verbgroup="GET"] [data-verb-toggle]');
  s = await state();
  ok(s.n === 20 && s.cls === "toolcount warn" && s.callout === "callout warn", "20 tools is amber: " + JSON.stringify(s));
  ok(s.groups.GET === "20 of 20 on", "group count follows: " + JSON.stringify(s.groups));
  ok(s.toasts.some(t => /^toast warn\|20 tools is a lot/.test(t)), "amber toast on crossing: " + JSON.stringify(s.toasts));
  ok(s.toasts.some(t => /Switched on 20 GET tools/.test(t)), "toast for the whole verb: " + JSON.stringify(s.toasts));
  const nonblocking = await pg.evaluate(() => !document.querySelector("[data-toolpick] .toolcount button.btn").disabled);
  ok(nonblocking, "Save stays enabled at amber");

  // a write verb on takes it past 30: red, and a red toast
  await pg.click('[data-verbgroup="DELETE"] summary');
  await pg.click('[data-verbgroup="DELETE"] [data-verb-toggle]');
  s = await state();
  ok(s.n === 40 && s.cls === "toolcount bad" && s.callout === "callout bad" && s.word === "too many", "40 tools is red: " + JSON.stringify(s));
  ok(s.toasts.some(t => /^toast bad\|40 tools is too many/.test(t)), "red toast on crossing: " + JSON.stringify(s.toasts));
  ok(s.toasts.some(t => /^toast warn\|Switched on 20 DELETE tools/.test(t)), "a write verb switched on warns: " + JSON.stringify(s.toasts));
  ok(await pg.evaluate(() => !document.querySelector("[data-toolpick] .toolcount button.btn").disabled), "Save stays enabled at red");

  // toasts last 5 seconds and sit in front
  const front = await pg.evaluate(() => { const t = document.querySelector(".toasts"); return t && (t.matches(":popover-open") || getComputedStyle(t).zIndex === "2147483647"); });
  ok(front, "toasts are in front");
  await sleep(5600);
  s = await state();
  ok(s.toasts.length === 0, "toasts expire after 5 s: " + JSON.stringify(s.toasts));

  // down again: single tools, no toast on the way down
  await pg.click('[data-verbgroup="DELETE"] [data-verb-toggle]');
  s = await state();
  ok(s.n === 20 && s.cls === "toolcount warn" && s.toasts.length === 0, "off again: " + JSON.stringify(s));
  await pg.click('[data-verbgroup="GET"] [data-tool-on]');
  s = await state();
  ok(s.n === 19 && s.groups.GET === "19 of 20 on", "a single tool: " + JSON.stringify(s));
  ok(await pg.evaluate(() => document.querySelector('[data-verbgroup="GET"] [data-verb-toggle]').indeterminate), "the verb switch shows a partial state");

  // the filter narrows rows; a verb switch then acts on what is shown
  await pg.type("[data-toolfilter]", "item7");
  const shown = await pg.evaluate(() => [...document.querySelectorAll("[data-tool]:not([hidden])")].map(r => r.dataset.key));
  ok(shown.length === 2 && shown.every(k => /item7/i.test(k)), "filter shows two rows: " + JSON.stringify(shown));
  ok(await pg.evaluate(() => document.querySelector("[data-toolfilter-count]").textContent) === "2 of 40", "filter count");
  await pg.click('[data-verbgroup="GET"] [data-verb-toggle]');
  s = await state();
  ok(s.n === 19 || s.n === 20, "verb switch on filtered rows: " + s.n);
  await pg.$eval("[data-toolfilter]", e => { e.value = ""; e.dispatchEvent(new Event("input", { bubbles: true })); });
  ok(await pg.evaluate(() => document.querySelectorAll("[data-tool][hidden]").length) === 0, "clearing the filter shows all");

  // the counter stays under the top bar while scrolling
  await pg.evaluate(() => window.scrollTo(0, 1500));
  await sleep(100);
  const pos = await pg.evaluate(() => { const r = document.querySelector("[data-toolcount]").getBoundingClientRect(), h = document.querySelector("header").getBoundingClientRect(); return [r.top, h.bottom]; });
  ok(Math.abs(pos[0] - pos[1]) < 2, "counter sticks under the header: " + pos);

  // the add form: the OpenAPI type shows its own fields; check and repair a broken description
  await pg.goto(url + "/admin/upstreams/new");
  await pg.select("[data-kind-select]", "openapi");
  const vis = await pg.evaluate(() => ({ oa: !document.querySelector('[data-kind="openapi"]').hidden, remote: !document.querySelector('.group[data-kind="remote"]').hidden,
    advanced: document.querySelector('[data-kind="openapi"] details').open }));
  ok(vis.oa && !vis.remote && !vis.advanced, "type switches the groups and Advanced starts closed: " + JSON.stringify(vis));
  ok(await pg.evaluate(() => !document.querySelector('select[name="oa_auth_kind"]') && !!document.querySelector('input[name="oa_auth_value"]') && !!document.querySelector('input[name="oa_auth_as"]')), "one key field and one Advanced line");
  await pg.$eval('textarea[name="oa_spec_text"]', e => { e.value = JSON.stringify({ openapi: "3.0.0", info: { title: "Broken", version: "1" }, paths: { "/a": { get: { summary: "x" } } } }); });
  await pg.click("[data-oa-check]");
  await pg.waitForSelector("[data-oa-out]:not([hidden]) [data-oa-issues] li");
  ok(await pg.evaluate(() => document.querySelector("[data-oa-pill]").textContent) === "1 problem", "the check counts the problem");
  await pg.waitForSelector("[data-oa-repair-row]:not([hidden])");
  await pg.click("[data-oa-repair]");
  await pg.waitForSelector("[data-oa-diff]:not([hidden]) [data-oa-diff-list] li", { timeout: 15000 });
  const dif = await pg.evaluate(() => ({ add: document.querySelector("[data-oa-diff-list] .add").textContent, path: document.querySelector("[data-oa-diff-list] code").textContent,
    pasted: document.querySelector('textarea[name="oa_spec_text"]').value }));
  ok(/getA/.test(dif.add) && dif.path === "/paths/~1a/get/operationId", "the diff shows the proposed change: " + JSON.stringify(dif));
  ok(!/getA/.test(dif.pasted), "nothing is applied before approval");
  await pg.click("[data-oa-apply]");
  ok(await pg.evaluate(() => /"operationId": "getA"/.test(document.querySelector('textarea[name="oa_spec_text"]').value)), "approving puts the fix into the paste box");

  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
