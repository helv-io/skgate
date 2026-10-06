// The code editor (CodeMirror 6, static/codemirror.js) on the MCP import page and the OpenAPI paste box, in a real
// browser, under the real Content-Security-Policy: colours, error underline, bracket match, auto-indent, the textarea
// kept as the form field, 16px on touch screens, and no CSP violation.
// Usage: node codebox.js <base url> <chrome> <puppeteer-core dir> <cookies json>
const [url, chrome, pp, cookiesJSON] = process.argv.slice(2);
const puppeteer = require(pp);
const bad = [];
const ok = (c, m) => { if (!c) bad.push(m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  const pg = await b.newPage();
  pg.on("dialog", d => d.accept()); // a reload after an unsaved edit is the tab-close warning; the check is leaving
  const csp = [];
  pg.on("console", m => { if (/Content Security Policy|violates|Refused/i.test(m.text())) csp.push(m.text()); });
  pg.on("pageerror", e => csp.push("page error: " + e.message));
  await pg.setCookie(...JSON.parse(cookiesJSON));
  await pg.setViewport({ width: 390, height: 900 });
  const info = sel => pg.evaluate(sel => {
    const ta = document.querySelector(sel), host = document.querySelector(".codehost"), sh = host && host.shadowRoot, ed = sh && sh.querySelector(".cm-editor");
    const color = text => { const s = [...(sh ? sh.querySelectorAll(".cm-line span") : [])].find(e => e.textContent === text); return s ? getComputedStyle(s).color : null; };
    return {
      mounted: !!ed, value: ta.value, name: ta.name, hidden: ta.classList.contains("code-src"),
      editorText: ed ? [...ed.querySelectorAll(".cm-line")].map(l => l.textContent).join("\n") : "",
      err: ed ? [...ed.querySelectorAll(".cm-lintRange-error")].map(e => e.textContent) : [], 
      match: ed ? [...ed.querySelectorAll(".cm-matchingBracket")].map(e => e.textContent) : [],
      key: color('"mcpServers"') || color("openapi") || color("title"), str: color('"http://x/mcp"') || color('"Pets"') || color('"3.0.3"'), num: color("12") || color("2"),
      border: ed ? getComputedStyle(ed).borderTopColor : "", size: ed ? getComputedStyle(ed.querySelector(".cm-content")).fontSize : "",
      page: document.documentElement.scrollWidth <= document.documentElement.clientWidth,
      status: (ta.closest(".field").querySelector("[data-code-status],[data-json-status]") || {}).textContent,
      shadowStyles: sh ? sh.querySelectorAll("style").length : 0, sheets: sh ? sh.adoptedStyleSheets.length : 0, styles: document.querySelectorAll("style").length,
      active: document.activeElement === host,
    };
  }, sel);
  const set = (sel, v) => pg.$eval(sel, (e, v) => { e.value = v; e.dispatchEvent(new Event("input", { bubbles: true })); }, v);
  const settle = () => wait(700); // the error check waits 250 ms after the last change

  // ---- MCP import: JSON ----
  await pg.goto(url + "/admin/upstreams/import");
  await wait(300);
  const J = "textarea[name=json]";
  let s = await info(J);
  ok(s.mounted && s.hidden && s.name === "json", "import box is an editor and the textarea stays the field " + JSON.stringify(s));
  ok(s.sheets > 0 && s.styles === 0 && s.shadowStyles === 0, "styles come in as constructed stylesheets, not <style> elements (shadow root sheets / style elements): " + s.sheets + "/" + s.styles);
  ok(s.size === "13px" || s.size === "16px", "editor font size " + s.size);
  await pg.click(".codehost");
  await pg.keyboard.type('{"mcpServers": {"a": {"url": "http://x/mcp", "n": 12}');
  await settle(); s = await info(J);
  ok(s.value === s.editorText && s.value.startsWith('{"mcpServers"'), "typing reaches the textarea: " + JSON.stringify(s.value));
  ok(s.key === "rgb(127, 184, 255)" && s.str === "rgb(143, 214, 148)" && s.num === "rgb(230, 192, 123)", "JSON colours: " + [s.key, s.str, s.num]);
  ok(/^Not valid JSON/.test(s.status), "data-json-check keeps its own line: " + s.status);
  ok(s.border === "rgb(255, 138, 128)", "the editor shows the invalid state: " + s.border);
  ok(s.err.length > 0, "the error is underlined: " + JSON.stringify(s.err));
  await pg.keyboard.type("}}");
  await settle(); s = await info(J);
  ok(s.err.length === 0 && /^Valid JSON/.test(s.status) && s.border !== "rgb(255, 138, 128)", "closed: no underline, valid " + JSON.stringify(s.err) + s.status + s.border);
  // auto-indent between braces
  await set(J, ""); await settle();
  await pg.click(".codehost");
  await pg.keyboard.type("{}");
  await pg.keyboard.press("ArrowLeft");
  await pg.keyboard.press("Enter");
  s = await info(J);
  ok(s.value === "{\n  \n}", "Enter between braces: " + JSON.stringify(s.value));
  await pg.keyboard.type('"k": [');
  await pg.keyboard.press("Enter");
  s = await info(J);
  ok(s.value.startsWith('{\n  "k": [\n    '), "Enter after [ indents deeper: " + JSON.stringify(s.value));
  // bracket match (caret just after the first {)
  await set(J, '{"a": [1, {"b": 2}]}'); await wait(200);
  await pg.click(".codehost"); await pg.keyboard.down("Control"); await pg.keyboard.press("Home"); await pg.keyboard.up("Control");
  await pg.keyboard.press("ArrowRight"); await wait(200); s = await info(J);
  ok(s.match.join("") === "{}", "caret after { marks the { and its }: " + JSON.stringify(s.match));
  // big text stays editable
  await pg.$eval(J, e => { e.value = '{"a": "' + "y".repeat(250000) + '"}'; e.dispatchEvent(new Event("input", { bubbles: true })); });
  await wait(500); s = await info(J);
  ok(s.mounted && s.value.length > 250000, "very long text is kept");
  ok(s.page, "no sideways page scroll on a phone");
  // the form still sends the textarea
  await set(J, '{"mcpServers": {"cb": {"url": "http://127.0.0.1:1/mcp"}}}'); await wait(300);
  await pg.click("form[action='/admin/upstreams/import'] button.btn");
  await pg.waitForSelector("[data-modal][open] [data-modal-body] table", { visible: true }); // the outcome, in a dialog over the page
  ok(await pg.evaluate(() => /cb/.test(document.querySelector("[data-modal-body]").textContent) && /created/.test(document.querySelector("[data-modal-body]").textContent)), "Import sends the textarea value");

  // ---- OpenAPI paste box: YAML, TOML, JSON ----
  await pg.goto(url + "/admin/upstreams/new");
  await pg.select("select[name=kind]", "openapi");
  await wait(400);
  const Y = "textarea[name=oa_spec_text]";
  s = await info(Y);
  ok(s.mounted && s.hidden && s.name === "oa_spec_text", "OpenAPI box is an editor " + JSON.stringify(s.mounted));
  await pg.click(".codehost");
  await pg.keyboard.type("openapi: 3.0.3");
  await pg.keyboard.press("Enter");
  await pg.keyboard.type("paths:");
  await pg.keyboard.press("Enter");
  await pg.keyboard.type("/a:");
  await pg.keyboard.press("Enter");
  await pg.keyboard.type("get: {operationId: a}");
  await settle(); s = await info(Y);
  ok(s.value === "openapi: 3.0.3\npaths:\n  /a:\n    get: {operationId: a}", "YAML auto-indent: " + JSON.stringify(s.value));
  ok(/Reads as YAML/.test(s.status) && s.err.length === 0, "YAML reads: " + s.status + JSON.stringify(s.err));
  ok(s.key === "rgb(127, 184, 255)", "YAML key colour " + s.key);
  await set(Y, "list: [1, 2\nother: 3\n"); await settle(); s = await info(Y);
  ok(s.err.length > 0 && /^YAML/.test(s.status), "YAML mistake is marked: " + s.status);
  await set(Y, 'openapi = "3.0.3"\n\n[info]\ntitle = "Pets"\nversion = 2\ntags = ["a", "b"]\n'); await settle(); s = await info(Y);
  ok(/Reads as TOML/.test(s.status) && s.err.length === 0, "TOML reads: " + s.status);
  ok(s.str === "rgb(143, 214, 148)" && s.num === "rgb(230, 192, 123)", "TOML colours " + [s.str, s.num]);
  await set(Y, 'a = \nb = nope\n[x\n'); await settle(); s = await info(Y);
  ok(s.err.length > 0 && /^TOML · line \d+/.test(s.status), "TOML mistake is marked with its line: " + s.status);
  await set(Y, '{"openapi": "3.0.3", "paths": {}}'); await settle(); s = await info(Y);
  ok(/Reads as JSON/.test(s.status) && s.err.length === 0, "JSON guessed: " + s.status);
  await set(Y, '{"openapi": "3.0.3", "paths": {},}'); await settle(); s = await info(Y);
  ok(s.err.length > 0 && /^JSON · line 1/.test(s.status), "JSON mistake: " + s.status);
  await set(Y, ""); await settle(); s = await info(Y);
  ok(s.status === "" && s.err.length === 0, "empty: quiet " + JSON.stringify(s.status));

  // ---- touch screen: 16px ----
  await pg.emulate({ viewport: { width: 390, height: 800, isMobile: true, hasTouch: true, deviceScaleFactor: 2 }, userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Mobile/15E148" });
  await pg.goto(url + "/admin/upstreams/import"); await wait(400);
  s = await info(J);
  ok(s.size === "16px", "16px text on a touch screen: " + s.size);
  ok(s.page, "no sideways page scroll on a touch screen");

  if (process.env.SKGATE_SHOTS) {
    await pg.setViewport({ width: 390, height: 800 });
    await set(J, '{"mcpServers": {"a": {"command": "npx", "args": ["-y", "pkg"], "n": 1.5, "ok": true, "x": null,}}}'); await settle();
    await pg.screenshot({ path: process.env.SKGATE_SHOTS + "/cm-json-390.png" });
    await pg.setViewport({ width: 1100, height: 800 });
    await pg.goto(url + "/admin/upstreams/new"); await pg.select("select[name=kind]", "openapi"); await wait(300);
    await set(Y, "openapi: 3.0.3\n# a comment\ninfo:\n  title: \"Pets\"\n  version: 1\npaths:\n  /pets:\n    get: {operationId: list, tags: [a, b]}\n"); await settle();
    await pg.screenshot({ path: process.env.SKGATE_SHOTS + "/cm-yaml-1100.png" });
    await set(Y, 'openapi = "3.0.3"\n# comment\n[info]\ntitle = "Pets"\nversion = 2\n[[servers]]\nurl = nope\n'); await settle();
    await pg.screenshot({ path: process.env.SKGATE_SHOTS + "/cm-toml-1100.png" });
  }
  ok(csp.length === 0, "no Content-Security-Policy violation or page error: " + csp.join(" | "));
  await b.close();
  console.log(bad.length ? "FAIL\n" + bad.join("\n") : "ALL OK");
  process.exit(bad.length ? 1 : 0);
})().catch(e => { console.error(e); process.exit(2); });
