// The code box (syntax colours, bracket match, error marker, auto-indent) on the MCP import page and the OpenAPI paste
// box, in a real browser, under the real Content-Security-Policy.
// Usage: node codebox.js <base url> <chrome> <puppeteer-core dir> <cookies json>
const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  const csp = [];
  pg.on("console", m => { if (/Content Security Policy|violates/i.test(m.text())) csp.push(m.text()); });
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 390, height: 900 });
  const frame = () => new Promise(r => setTimeout(r, 120));
  const info = sel => pg.evaluate(sel => {
    const ta = document.querySelector(sel), box = ta.closest(".codebox"), pre = box && box.querySelector("pre");
    const cs = getComputedStyle(ta), ps = pre && getComputedStyle(pre);
    return {
      boxed: !!box, name: ta.name, value: ta.value, caret: ta.selectionStart,
      color: cs.webkitTextFillColor, same: pre ? ["fontFamily", "fontSize", "lineHeight", "tabSize", "paddingLeft", "paddingTop", "borderLeftWidth", "whiteSpace"].every(k => cs[k] === ps[k]) : false,
      html: pre ? pre.innerHTML : "", err: pre ? [...pre.querySelectorAll(".t-e")].map(e => e.textContent) : [],
      match: pre ? [...pre.querySelectorAll(".t-m")].map(e => e.textContent) : [],
      keys: pre ? [...pre.querySelectorAll(".t-k")].map(e => e.textContent) : [], strs: pre ? [...pre.querySelectorAll(".t-s")].map(e => e.textContent) : [],
      nums: pre ? [...pre.querySelectorAll(".t-n")].map(e => e.textContent) : [], tables: pre ? [...pre.querySelectorAll(".t-t")].map(e => e.textContent) : [],
      pt: pre ? pre.textContent : "", sx: pre ? pre.scrollLeft : -1, sy: pre ? pre.scrollTop : -1, tx: ta.scrollLeft, ty: ta.scrollTop,
      page: document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      status: (ta.closest(".field").querySelector("[data-code-status],[data-json-status]") || {}).textContent,
    };
  }, sel);
  const set = (sel, v) => pg.$eval(sel, (e, v) => { e.focus(); e.value = v; e.dispatchEvent(new Event("input", { bubbles: true })); }, v);

  // ---- MCP import: JSON ----
  await pg.goto(url + "/admin/upstreams/import");
  const J = "textarea[name=json]";
  let s = await info(J);
  ok(s.boxed && s.name === "json", "import box is wrapped and keeps its name " + JSON.stringify(s));
  ok(s.same, "the coloured copy and the textarea share font, padding and border");
  ok(s.color === "rgba(0, 0, 0, 0)", "textarea text is transparent over the colours: " + s.color);
  await pg.click(J);
  await pg.keyboard.type('{"mcpServers": {"a": {"url": "http://x/mcp", "n": 12, "t": true}');
  await frame(); s = await info(J);
  ok(s.keys.includes('"mcpServers"') && s.keys.includes('"a"') && s.strs.includes('"http://x/mcp"') && s.nums.includes("12"), "JSON colours: " + JSON.stringify(s.keys) + JSON.stringify(s.strs));
  ok(s.err.length > 0, "an unclosed brace is marked: " + JSON.stringify(s.err));
  ok(/^Not valid JSON/.test(s.status), "data-json-check keeps its own line: " + s.status);
  await pg.keyboard.type("}}");
  await frame(); s = await info(J);
  ok(s.err.length === 0 && /^Valid JSON/.test(s.status), "closed: no marker, still valid " + JSON.stringify(s.err) + s.status);
  ok(s.pt.replace(/\s+$/, "") === s.value, "the copy shows exactly the text");
  // mistakes
  await set(J, '{"a": 1,}'); await frame(); s = await info(J);
  ok(s.err.join("") === "}", "a trailing comma marks the brace after it: " + JSON.stringify(s.err));
  await set(J, '{"a": "open}'); await frame(); s = await info(J);
  ok(s.err.length > 0, "an unclosed string is marked");
  // auto-indent: Enter between braces opens a body and puts the closer below
  await set(J, "");
  await pg.keyboard.type("{");
  await pg.keyboard.type("}");
  await pg.keyboard.press("ArrowLeft");
  await pg.keyboard.press("Enter");
  s = await info(J);
  ok(s.value === "{\n  \n}" && s.caret === 4, "Enter between braces: " + JSON.stringify(s.value) + " caret " + s.caret);
  await pg.keyboard.type('"k": [');
  await pg.keyboard.press("Enter");
  s = await info(J);
  ok(s.value === '{\n  "k": [\n    \n}', "Enter after [ indents one more level: " + JSON.stringify(s.value));
  await pg.keyboard.press("Enter");
  s = await info(J);
  ok(s.value === '{\n  "k": [\n    \n    \n}', "Enter keeps the indent: " + JSON.stringify(s.value));
  // bracket match
  await set(J, '{"a": [1, {"b": 2}]}'); await pg.$eval(J, e => e.setSelectionRange(6, 6)); await frame(); s = await info(J);
  ok(s.match.join("") === "[]", "caret beside [ marks it and its ]: " + JSON.stringify(s.match));
  await pg.$eval(J, e => e.setSelectionRange(1, 1)); await frame(); s = await info(J);
  ok(s.match.join("") === "{}", "caret after the first { marks the last }: " + JSON.stringify(s.match));
  await pg.$eval(J, e => e.setSelectionRange(3, 3)); await frame(); s = await info(J);
  ok(s.match.length === 0, "no marker inside a string");
  // scroll stays in step, also with a long line
  await set(J, Array.from({ length: 80 }, (_, i) => '{"line' + i + '": "' + "x".repeat(120) + '"}').join("\n"));
  await pg.$eval(J, e => { e.scrollTop = 300; e.scrollLeft = 150; e.dispatchEvent(new Event("scroll")); });
  await frame(); s = await info(J);
  ok(s.sy === s.ty && s.sx === s.tx && s.ty > 0 && s.tx > 0, "the copy scrolls with the textarea " + [s.sx, s.tx, s.sy, s.ty]);
  ok(s.page, "a long line scrolls inside the box, not the page");
  // very long text: colours off, text still shown
  await pg.$eval(J, e => { e.value = '{"a": "' + "y".repeat(250000) + '"}'; e.dispatchEvent(new Event("input", { bubbles: true })); });
  await frame(); const big = await pg.evaluate(() => ({ plain: document.querySelector(".codebox").classList.contains("plain"), color: getComputedStyle(document.querySelector("textarea[name=json]")).webkitTextFillColor }));
  ok(big.plain && big.color !== "rgba(0, 0, 0, 0)", "very long text: plain, readable " + JSON.stringify(big));
  // the form still sends the textarea
  await set(J, '{"mcpServers": {"cb": {"url": "http://127.0.0.1:1/mcp"}}}');
  await Promise.all([pg.waitForNavigation(), pg.click("form[action='/admin/upstreams/import'] button.btn")]);
  ok(await pg.evaluate(() => document.body.textContent.includes("cb")), "Import sends the textarea value");

  // ---- OpenAPI paste box: YAML, TOML, JSON ----
  await pg.goto(url + "/admin/upstreams/new");
  await pg.select("select[name=kind]", "openapi");
  const Y = "textarea[name=oa_spec_text]";
  s = await info(Y);
  ok(s.boxed && s.name === "oa_spec_text", "OpenAPI box is wrapped " + JSON.stringify(s.boxed));
  await pg.click(Y);
  await pg.keyboard.type("openapi: 3.0.3");
  await pg.keyboard.press("Enter");
  await pg.keyboard.type("paths:");
  await pg.keyboard.press("Enter");
  s = await info(Y);
  ok(s.value === "openapi: 3.0.3\npaths:\n  ", "YAML: Enter after a key with a colon indents: " + JSON.stringify(s.value));
  await pg.keyboard.type("/a:");
  await pg.keyboard.press("Enter");
  await pg.keyboard.type("get: {operationId: a}  # note");
  await frame(); s = await info(Y);
  ok(s.value.endsWith("/a:\n    get: {operationId: a}  # note"), "indent kept and deepened: " + JSON.stringify(s.value));
  ok(s.keys.includes("openapi") && s.keys.includes("paths") && s.nums.includes("3.0.3") === false, "YAML keys coloured: " + JSON.stringify(s.keys) + JSON.stringify(s.nums));
  ok(/Reads as YAML/.test(s.status) && s.err.length === 0, "YAML reads, no error: " + s.status);
  await set(Y, "openapi: 3.0.3\ninfo:\n\ttitle: X\n"); await frame(); s = await info(Y);
  ok(s.err.length > 0 && /line 3: indent with spaces/.test(s.status), "a tab indent is marked with its line: " + s.status);
  await set(Y, "a: b: c\n"); await frame(); s = await info(Y);
  ok(/line 1: a second colon/.test(s.status), "two colons in a plain value: " + s.status);
  await set(Y, "list: [1, 2\nother: 3\n"); await frame(); s = await info(Y);
  ok(s.err.length > 0, "an unclosed flow list is marked: " + s.status);
  await set(Y, 'openapi = "3.0.3"\n\n[info]\ntitle = "X"\nversion = 2\ntags = ["a", "b"]\n'); await frame(); s = await info(Y);
  ok(/Reads as TOML/.test(s.status) && s.err.length === 0 && s.tables.includes("info") && s.keys.includes("title") && s.nums.includes("2"), "TOML: " + s.status + JSON.stringify(s.tables) + JSON.stringify(s.err));
  await set(Y, 'a = \nb = nope\n[x\n'); await frame(); s = await info(Y);
  ok(s.err.length > 0 && /^TOML/.test(s.status), "TOML mistakes marked: " + s.status);
  await set(Y, '{"openapi": "3.0.3", "paths": {}}'); await frame(); s = await info(Y);
  ok(/Reads as JSON/.test(s.status) && s.err.length === 0, "JSON guessed from the first character: " + s.status);
  await set(Y, ""); await frame(); s = await info(Y);
  ok(s.status === "" && s.err.length === 0, "empty: quiet " + JSON.stringify(s.status));
  ok(s.page, "no sideways page scroll on a phone");

  if (process.env.SKGATE_SHOTS) {
    const shot = async (name, text, sel, w) => {
      await pg.setViewport({ width: w, height: 800 });
      await pg.goto(url + (sel === Y ? "/admin/upstreams/new" : "/admin/upstreams/import"));
      if (sel === Y) await pg.select("select[name=kind]", "openapi");
      await set(sel, text); await pg.$eval(sel, e => { e.setSelectionRange(1, 1); }); await frame();
      await (await pg.$(sel + "")).evaluate(e => e.scrollIntoView({ block: "center" }));
      await pg.screenshot({ path: process.env.SKGATE_SHOTS + "/codebox-" + name + ".png" });
    };
    await shot("json-390", '{"mcpServers": {"a": {"command": "npx", "args": ["-y", "pkg"], "n": 1.5, "ok": true, "x": null,}}}', J, 390);
    await shot("yaml-1100", "openapi: 3.0.3\n# a comment\ninfo:\n  title: \"Pets\"\n  version: 1\npaths:\n  /pets:\n    get: {operationId: list, tags: [a, b]}\n    post:\n      description: |\n        Adds a pet.\n      deprecated: false\n", Y, 1100);
    await shot("toml-390", 'openapi = "3.0.3"\n# comment\n[info]\ntitle = "Pets"\nversion = 2\ntags = ["a", "b"]\n[[servers]]\nurl = nope\n', Y, 390);
  }
  ok(csp.length === 0, "no Content-Security-Policy violation: " + csp.join(" | "));
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
