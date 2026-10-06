// The update pill on a phone (touch, iPhone user agent): a tap posts the update to the row's process route, the row
// refreshes in place, and a failed update shows its reason as a toast as well as on the red pill.
// Usage: node update_tap.js <base> <chrome> <puppeteer-core> <cookies> <phase: ok|fail>
const [url, chrome, pp, cookiesJSON, phase] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setUserAgent("Mozilla/5.0 (iPhone; CPU iPhone OS 18_7 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/27.0.1 Mobile/15E148 Safari/604.1");
  await pg.setViewport({ width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 2 });
  const posts = [];
  pg.on("request", r => { if (r.method() === "POST") posts.push(new URL(r.url()).pathname + "?" + (r.postData() || "")); });
  await pg.goto(url + "/admin/upstreams");
  const sel = "#up-repo form[data-update] button";
  const before = await pg.evaluate(s => {
    const btn = document.querySelector(s);
    if (!btn) return null;
    btn.scrollIntoView({ block: "center" });
    const r = btn.getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return { tag: btn.tagName, type: btn.type, hit: !!hit && (hit === btn || btn.contains(hit)), h: r.height };
  }, sel);
  ok(before && before.tag === "BUTTON" && before.type === "submit", "the pill is a real button: " + JSON.stringify(before));
  ok(before && before.hit, "a tap on the pill lands on the pill: " + JSON.stringify(before));
  await pg.tap(sel);
  await wait(300);
  ok(posts.length === 1 && posts[0].startsWith("/admin/upstreams/repo/process?") && posts[0].includes("action=update"), "one tap posts one update to the row's process route: " + JSON.stringify(posts));
  let end = Date.now() + 20000, state;
  for (;;) {
    state = await pg.evaluate(() => {
      const b = document.querySelector("#up-repo form[data-update] button");
      return { pill: b ? b.className : "", updating: !!(b && b.hasAttribute("data-updating")), title: b ? b.title : "",
        toasts: [...document.querySelectorAll(".toast")].map(t => t.textContent.trim()) };
    });
    if (!state.updating && (phase === "ok" ? state.pill === "" : state.pill.includes("bad"))) break;
    if (Date.now() > end) break;
    await wait(200);
  }
  if (phase === "ok") {
    ok(state.pill === "", "after the update the row refreshes in place and the pill is gone: " + JSON.stringify(state));
    ok(!state.toasts.some(t => /fail|error|reach/i.test(t)), "a good update shows no error: " + JSON.stringify(state));
  } else {
    ok(state.pill.includes("bad") && state.title !== "", "a failed update turns the pill red with the reason: " + JSON.stringify(state));
    ok(state.toasts.some(t => t === state.title), "a failed update shows its reason as a toast: " + JSON.stringify(state));
  }
  ok(posts.length === 1, "polling only reads: " + JSON.stringify(posts));
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
