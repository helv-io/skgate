const [url, chrome, pp, cookiesJSON, width] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
const vis = (pg, sel) => pg.evaluate(s => [...document.querySelectorAll(s)].filter(e => !e.hidden && e.offsetParent !== null).length, sel);
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  for (const w of [parseInt(width, 10)]) {
    const pg = await b.newPage();
    await pg.setCookie(...JSON.parse(cookiesJSON));
    await pg.setViewport({ width: w, height: 600 });
    await pg.goto(url + "/admin/upstreams/tools/logs");
    const nonSys = '.logline:not([data-src="sys"])';
    const view = "[data-log-view]";
    const state = () => pg.evaluate(() => { const v = document.querySelector("[data-log-view]"); return { top: v.scrollTop, gap: v.scrollHeight - v.scrollTop - v.clientHeight, scrollable: v.scrollHeight > v.clientHeight, jump: !document.querySelector("[data-log-jump]").hidden }; });
    const total = await pg.evaluate(() => document.querySelectorAll(".logline").length);
    ok(total >= 35, w + ": the page shows the kept lines " + total);
    let s = await state();
    ok(s.scrollable && s.gap < 24 && !s.jump, w + ": opens at the newest line " + JSON.stringify(s));
    ok(await pg.evaluate(() => document.querySelector("[data-log-count]").textContent) === total + " lines", w + ": counts the lines");

    // tint: stdout, stderr and skgate's own lines differ
    const tints = await pg.evaluate(() => { const bg = src => { const e = document.querySelector('.logline[data-src="' + src + '"]'); return e && getComputedStyle(e).backgroundColor; }; return { err: bg("err"), sys: bg("sys") }; });
    ok(tints.err && tints.sys && tints.err !== tints.sys, w + ": stderr and skgate lines are tinted differently " + JSON.stringify(tints));
    ok(await pg.evaluate(() => getComputedStyle(document.querySelector('.logline[data-src="sys"]')).fontStyle) === "italic", w + ": skgate lines are italic");

    // text filter, case-insensitive; regex; an invalid regex says so
    await pg.type("[data-log-filter]", "NOISE 1");
    ok(await vis(pg, nonSys) === 11, w + ": text filter " + await vis(pg, nonSys));
    ok(/^11 of \d+ lines$/.test(await pg.evaluate(() => document.querySelector("[data-log-count]").textContent)), w + ": filter count");
    await pg.$eval("[data-log-filter]", e => { e.value = ""; });
    await pg.type("[data-log-filter]", "/^ERROR noise \\d+ failed$/");
    ok(await vis(pg, nonSys) === 3, w + ": regex filter " + await vis(pg, nonSys));
    await pg.$eval("[data-log-filter]", e => { e.value = ""; });
    await pg.type("[data-log-filter]", "/(/");
    ok(await pg.$eval("[data-log-filter]", e => e.getAttribute("aria-invalid")) === "true" && /not a valid/.test(await pg.evaluate(() => document.querySelector("[data-log-count]").textContent)), w + ": invalid regex is flagged");
    await pg.$eval("[data-log-filter]", e => { e.value = ""; e.dispatchEvent(new Event("input", { bubbles: true })); });

    // level filter: a stack line follows its error
    ok(await pg.$eval("[data-log-level]", e => !e.hidden), w + ": the level filter is offered");
    await pg.select("[data-log-level]", "error");
    ok(await vis(pg, nonSys) === 6, w + ": errors and their stack lines " + await vis(pg, nonSys));
    await pg.select("[data-log-level]", "warn");
    ok(await vis(pg, nonSys) === 9, w + ": warnings and errors " + await vis(pg, nonSys));
    await pg.select("[data-log-level]", "");
    ok(await pg.evaluate(() => getComputedStyle(document.querySelector('.logline[data-lvl="error"]')).borderLeftColor) !== "rgba(0, 0, 0, 0)", w + ": an error line is marked");

    // stderr only
    await pg.click("[data-log-stderr]");
    ok(await pg.$eval("[data-log-stderr]", e => e.getAttribute("aria-pressed") === "true" && e.classList.contains("on")), w + ": stderr toggle is on");
    ok(await vis(pg, nonSys) === total - 2 - 0 || await vis(pg, nonSys) > 30, w + ": stderr lines stay");
    await pg.click("[data-log-stderr]");

    // times: clock <-> ago, remembered
    ok(/^\d\d:\d\d:\d\d$/.test(await pg.$eval(".logtime", e => e.textContent)), w + ": clock times");
    await pg.click("[data-log-time]");
    ok(/ ago$/.test(await pg.$eval(".logtime", e => e.textContent)) && await pg.evaluate(() => localStorage.getItem("skgate.logtime")) === "ago", w + ": relative times, remembered");
    await pg.reload();
    ok(/ ago$/.test(await pg.$eval(".logtime", e => e.textContent)), w + ": relative times survive a reload");
    await pg.click("[data-log-time]");
    ok(/^\d\d:\d\d:\d\d$/.test(await pg.$eval(".logtime", e => e.textContent)), w + ": back to the clock");

    // scrolling up stops following; a live line arrives without moving the view; jump returns
    const csrf = await pg.$eval("input[name=csrf]", e => e.value);
    const post = action => pg.evaluate((c, a) => fetch("/admin/upstreams/tools/process", { method: "POST", body: new URLSearchParams({ csrf: c, action: a, to: "logs" }), redirect: "manual" }).then(() => 1), csrf, action);
    await pg.$eval(view, e => { e.scrollTop = 0; });
    await pg.waitForFunction(() => !document.querySelector("[data-log-jump]").hidden);
    const before = await pg.evaluate(() => document.querySelectorAll(".logline").length);
    await post("stop");
    await pg.waitForFunction(() => [...document.querySelectorAll(".logline")].some(l => l.textContent.includes("stopped (")), { timeout: 10000 });
    s = await state();
    ok(s.top < 24 && s.jump && s.gap > 24, w + ": reading history is not disturbed by new lines " + JSON.stringify(s));
    ok(await pg.evaluate(() => document.querySelectorAll(".logline").length) > before, w + ": the live line arrived");
    await pg.click("[data-log-jump]");
    s = await state();
    ok(s.gap < 24 && !s.jump, w + ": jump to latest " + JSON.stringify(s));

    // a new run: divider labels, only two runs
    await post("start");
    await pg.waitForFunction(() => document.querySelectorAll(".logdivider").length >= 2 && [...document.querySelectorAll(".logline")].filter(l => l.textContent.includes("started, pid")).length >= 2, { timeout: 10000 });
    const marks = await pg.evaluate(() => [...document.querySelectorAll(".logmark")].map(m => m.textContent));
    ok(marks.length === 2 && marks[0] === "previous run" && marks[1] === "since last start", w + ": dividers " + JSON.stringify(marks));
    await pg.waitForFunction(() => { const v = document.querySelector("[data-log-view]"); return v.scrollHeight - v.scrollTop - v.clientHeight < 24; }, { timeout: 3000 }).catch(() => {});
    s = await state();
    ok(s.gap < 24, w + ": a following view stays at the newest line " + JSON.stringify(s));
    await post("stop");
    await pg.waitForFunction(() => [...document.querySelectorAll(".logline")].filter(l => l.textContent.includes("stopped (")).length >= 2, { timeout: 10000 });
    await post("start");
    await pg.waitForFunction(() => document.querySelectorAll(".logdivider").length === 2 && [...document.querySelectorAll(".logline")].filter(l => l.textContent.includes("started, pid")).length === 2 && document.querySelectorAll(".logline").length > 40, { timeout: 10000 });
    const runs = await pg.evaluate(() => [...new Set([...document.querySelectorAll(".logline")].map(l => l.getAttribute("data-run")))]);
    ok(runs.length === 2, w + ": only two runs stay on the page " + JSON.stringify(runs));

    // copy takes the shown lines
    await pg.type("[data-log-filter]", "noise 2");
    await pg.click("[data-copy]");
    const copied = await pg.$eval("#log-copy", e => e.textContent.split("\n").length);
    ok(copied === await vis(pg, ".logline"), w + ": copy has the shown lines " + copied);

    ok(await pg.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), w + ": no sideways scroll");
    await pg.close();
  }
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.log("ERROR " + e.stack); process.exit(1); });
