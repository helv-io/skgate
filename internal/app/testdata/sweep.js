// No horizontal scrollbar, at any width, on any admin page; screenshots at phone and desktop widths (optional).
// Usage: node sweep.js <pagesDir> <chrome> <puppeteer-core dir> [shotsDir]
// <pagesDir>/pages.json lists {name, dialog}: dialog=true also opens the first Details dialog and checks again.
const [dir, chrome, pp, shots] = process.argv.slice(2);
const puppeteer = require(pp);
const fs = require("fs");
const widths = [320, 360, 390, 768, 1024, 1280, 1920];
const pages = JSON.parse(fs.readFileSync(dir + "/pages.json", "utf8"));

// Offenders: elements whose box reaches past the viewport (not scrollable boxes of their own).
const measure = () => {
  const vw = document.documentElement.clientWidth;
  const out = [];
  if (document.documentElement.scrollWidth > vw) out.push("document scrollWidth " + document.documentElement.scrollWidth + " > " + vw);
  if (document.body.scrollWidth > vw) out.push("body scrollWidth " + document.body.scrollWidth + " > " + vw);
  const dlg = document.querySelector("dialog[open]");
  if (dlg) {
    if (dlg.scrollWidth > dlg.clientWidth) out.push("dialog scrollWidth " + dlg.scrollWidth + " > " + dlg.clientWidth);
    const r = dlg.getBoundingClientRect();
    if (r.left < 0 || r.right > vw + 0.5) out.push("dialog box " + Math.round(r.left) + ".." + Math.round(r.right) + " outside " + vw);
  }
  const card = document.querySelector("main.solo .card");
  if (card) {
    const r = card.getBoundingClientRect(), vh = window.innerHeight;
    if (Math.abs((r.left + r.right) / 2 - vw / 2) > 1) out.push("card not centered horizontally: center " + ((r.left + r.right) / 2) + " vs " + vw / 2);
    if (document.documentElement.scrollHeight <= vh) {
      if (Math.abs((r.top + r.bottom) / 2 - vh / 2) > 1.5) out.push("card not centered vertically: center " + ((r.top + r.bottom) / 2) + " vs " + vh / 2);
    } else if (r.top < 0) out.push("card clipped at the top: " + r.top);
    if (r.left < 0 || r.right > vw) out.push("card outside the viewport");
  }
  const ch = [...document.querySelectorAll(".choices button")];
  window.__choices = ch.map(e => { const c = getComputedStyle(e); return [e.textContent.trim(), c.backgroundColor, c.borderTopColor, c.color, c.fontSize, c.borderTopWidth, c.minHeight].join("|"); });
  ch.forEach(e => { if (e.getBoundingClientRect().height < 48) out.push(e.textContent.trim() + " button is shorter than 48px"); });
  // the version link is the yellow warning colour when an update is known, the dim one otherwise, and stays on screen
  const ver = document.querySelector("header a.ver");
  if (ver) {
    const r = ver.getBoundingClientRect(), col = getComputedStyle(ver).color;
    if (r.right > vw + 0.5 || r.left < 0) out.push("version link outside the screen");
    if (ver.classList.contains("new") !== (col === "rgb(230, 192, 123)")) out.push("version link colour " + col + " does not match its update state");
    if (ver.target !== "_blank" || !/noopener/.test(ver.rel) || !/noreferrer/.test(ver.rel)) out.push("version link must open in a new tab with rel noopener noreferrer");
  }
  // folded sections (the create form's Limits) are opened so what is inside is measured too
  document.querySelectorAll("details:not([open])").forEach(d => { d.open = true; });
  // the expiration presets are finger-sized on phones and the field and its preview fit the screen
  [...document.querySelectorAll(".quick .act")].filter(e => e.offsetParent !== null).forEach(e => {
    const r = e.getBoundingClientRect();
    if (vw <= 720 && r.height < 44) out.push("expiry preset " + e.textContent.trim() + " is " + Math.round(r.height) + "px tall, under 44px");
    if (r.right > vw + 0.5) out.push("expiry preset " + e.textContent.trim() + " reaches past the screen");
  });
  [...document.querySelectorAll("[data-expiry-input]")].filter(e => e.offsetParent !== null).forEach(e => {
    const r = e.getBoundingClientRect();
    if (r.width < 160) out.push("expiry field only " + Math.round(r.width) + "px wide");
  });
  // keys that may be sent as ?key= are tinted (row on desktop, card on phones); the others are not, and every cell's
  // text keeps its contrast on the tinted background
  const rows = [...document.querySelectorAll("tr.geturl")];
  const plain = [...document.querySelectorAll(".table:not(.kv) tbody tr:not(.geturl)")].filter(r => r.querySelector("td.primary"));
  const rgba = s => { const m = s.match(/[\d.]+/g).map(Number); return { r: m[0], g: m[1], b: m[2], a: m.length > 3 ? m[3] : 1 }; };
  const under = el => { // what the element really sits on: backgrounds of itself and its ancestors composed
    const layers = [];
    for (let e = el; e; e = e.parentElement) { const c = rgba(getComputedStyle(e).backgroundColor); if (c.a > 0) layers.push(c); if (c.a === 1) break; }
    let base = layers.pop() || { r: 14, g: 15, b: 17, a: 1 };
    while (layers.length) { const t = layers.pop(); base = { r: t.r * t.a + base.r * (1 - t.a), g: t.g * t.a + base.g * (1 - t.a), b: t.b * t.a + base.b * (1 - t.a), a: 1 }; }
    return base;
  };
  // a tinted card on phones paints its gradient as background-image; fold that layer in
  const tinted = el => { const bi = getComputedStyle(el).backgroundImage; return bi && bi !== "none" ? rgba(bi.match(/rgba?\([^)]*\)/)[0]) : null; };
  const lum = c => { const f = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b); };
  const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const rowBg = tr => {
    let bg = under(tr.querySelector("td"));
    const tint = tinted(tr);
    if (tint) bg = { r: tint.r * tint.a + bg.r * (1 - tint.a), g: tint.g * tint.a + bg.g * (1 - tint.a), b: tint.b * tint.a + bg.b * (1 - tint.a), a: 1 };
    return bg;
  };
  const plainRef = plain[0] ? rowBg(plain[0]) : null;
  rows.forEach(tr => {
    const bg = rowBg(tr);
    if (plainRef && Math.abs(bg.r - plainRef.r) + Math.abs(bg.g - plainRef.g) + Math.abs(bg.b - plainRef.b) < 4) out.push("tinted key row is not visibly tinted against a plain one");
    tr.querySelectorAll("td, td *").forEach(e => {
      if (![...e.childNodes].some(n => n.nodeType === 3 && n.textContent.trim())) return;
      const col = rgba(getComputedStyle(e).color);
      if (ratio(col, bg) < 4.5) out.push("text on a tinted key row only has contrast " + ratio(col, bg).toFixed(2) + " (" + e.tagName + ")");
    });
  });
  if (plain[0] && plainRef) { const t = tinted(plain[0]); if (t && t.r === 230) out.push("a plain key row carries the tint"); }
  window.__tinted = rows.length;
  if (out.length) {
    const bad = [];
    for (const el of document.querySelectorAll("body *")) {
      const r = el.getBoundingClientRect();
      if (r.width && r.right > vw + 0.5) bad.push((el.tagName + "." + (el.className && el.className.baseVal === undefined ? el.className : "")).slice(0, 40) + " right=" + Math.round(r.right));
      if (bad.length >= 6) break;
    }
    out.push("offenders: " + bad.join(", "));
  }
  return out;
};

(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  // headless Chrome reports no hover; a second browser that reports a mouse checks the hover behavior of the sign-out pill
  const bm = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new", "--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4"] });
  let bad = 0, checks = 0, ref = null;
  for (const p of pages) {
    const solo = ["consent", "signed-out", "auth-error", "not-configured"].includes(p.name);
    for (const [width, height] of widths.flatMap(w => (solo ? [[w, 900], [w, 360]] : [[w, 900]]))) {
      const pg = await b.newPage();
      await pg.setViewport({ width, height });
      await pg.goto("file://" + dir + "/" + p.name + ".html");
      let res = await pg.evaluate(measure);
      checks++;
      if (p.name === "keys") { checks++; if (!(await pg.evaluate(() => window.__tinted))) { bad++; console.log("FAIL", p.name, width, "no tinted ?key= row on the keys page"); } }
      const sig = await pg.evaluate(() => window.__choices || []);
      if (sig.length) {
        // Approve (primary) and Deny look the same at every width, and Approve is the filled one
        ref = ref || sig;
        if (JSON.stringify(sig) !== JSON.stringify(ref)) { bad++; console.log("FAIL", p.name, width + "x" + height, "buttons differ from the first width", JSON.stringify(sig), JSON.stringify(ref)); }
        const [deny, approve] = sig.map(x => x.split("|"));
        if (deny[0] !== "Deny" || approve[0] !== "Approve" || approve.slice(1, 4).join() === deny.slice(1, 4).join()) { bad++; console.log("FAIL", p.name, "Approve must be the filled primary", JSON.stringify(sig)); }
      }
      if (res.length) { bad++; console.log("FAIL", p.name, width + "x" + height, JSON.stringify(res)); }
      // the signed-in pill is the Sign out button: hovering turns it red and shows "Sign out" without any size change
      const sw0 = await pg.$("button.pill.swap");
      if (sw0) {
        checks++;
        // touch (no hover): the pill is a finger-sized target and shows no Sign out label
        const touch = await pg.evaluate(() => { const b = document.querySelector("button.pill.swap"); return { h: b.getBoundingClientRect().height, now: getComputedStyle(b.querySelector(".now")).visibility, cs: getComputedStyle(b).color }; });
        if (touch.h < 44 || touch.now !== "hidden") { bad++; console.log("FAIL", p.name, width, "sign-out pill without hover:", JSON.stringify(touch)); }
        const mp = await bm.newPage();
        await mp.setViewport({ width, height });
        await mp.goto("file://" + dir + "/" + p.name + ".html");
        const sw = await mp.$("button.pill.swap");
        const state = () => mp.evaluate(() => { const b = document.querySelector("button.pill.swap"), r = b.getBoundingClientRect(), v = e => getComputedStyle(b.querySelector(e)).visibility;
          return { w: Math.round(r.width * 10) / 10, h: Math.round(r.height * 10) / 10, color: getComputedStyle(b).color, was: v(".was"), now: v(".now"), right: r.right, vw: document.documentElement.clientWidth, sw: document.documentElement.scrollWidth,
            fits: b.querySelector(".swaps").getBoundingClientRect().width >= Math.max(...[".was", ".now"].map(e => b.querySelector(e).scrollWidth)) }; });
        const before = await state();
        await sw.hover(); await new Promise(r => setTimeout(r, 80));
        const after = await state();
        await mp.mouse.move(1, 1);
        const why = [];
        if (before.w !== after.w || before.h !== after.h) why.push("the pill changes size on hover " + before.w + "x" + before.h + " -> " + after.w + "x" + after.h);
        if (before.was !== "visible" || before.now !== "hidden") why.push("resting labels wrong");
        if (after.was !== "hidden" || after.now !== "visible") why.push("hover must show Sign out ");
        if (after.color !== "rgb(255, 138, 128)" || before.color === after.color) why.push("hover color " + after.color);
        if (!before.fits || before.right > before.vw + 0.5 || before.sw > before.vw) why.push("pill does not fit");
        if (why.length) { bad++; console.log("FAIL", p.name, width, "sign-out pill:", why.join("; ")); }
        await mp.close();
      }
      if (shots && solo) await pg.screenshot({ path: shots + "/" + p.name + "-" + width + "x" + height + ".png" });
      else if (shots && (width === 390 || width === 1280)) await pg.screenshot({ path: shots + "/" + p.name + "-" + width + ".png", fullPage: true });
      if (p.dialog) {
        const opener = await pg.$("[data-dialog-open]");
        if (opener) {
          await opener.evaluate(e => e.click());
          await new Promise(r => setTimeout(r, 150));
          res = await pg.evaluate(measure);
          checks++;
          if (res.length) { bad++; console.log("FAIL", p.name, "dialog", width, JSON.stringify(res)); }
          // a toast fired while a dialog is open is drawn in front of it
          const front = await pg.evaluate(() => {
            window.skgateToast("ok", "front check");
            const t = document.querySelector(".toasts .toast:last-child");
            if (!t) return "no toast";
            const r = t.getBoundingClientRect();
            const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
            return hit && t.contains(hit) ? "" : "toast is behind " + (hit ? hit.tagName + "." + hit.className : "nothing");
          });
          checks++;
          if (front) { bad++; console.log("FAIL", p.name, "dialog", width, front); }
          else {
            // ... and it still takes a click (a modal makes everything outside it inert) and goes away
            const box = await pg.$(".toasts .toast:last-child");
            await box.click(); await new Promise(r => setTimeout(r, 450));
            checks++;
            if (await pg.evaluate(() => document.querySelectorAll(".toasts .toast").length)) { bad++; console.log("FAIL", p.name, "dialog", width, "a toast over a dialog cannot be dismissed by a click"); }
          }
          if (shots && (width === 390 || width === 1280)) await pg.screenshot({ path: shots + "/" + p.name + "-dialog-" + width + ".png" });
          // the fragment follows the dialog: set on open, gone after Escape, backdrop or the close button, and a
          // reload then shows no dialog; Back closes it
          const open = () => pg.evaluate(() => document.querySelector("[data-modal]").open);
          const hash = () => pg.evaluate(() => location.hash);
          const settle = () => new Promise(r => setTimeout(r, 120));
          const reopen = async () => { await pg.evaluate(() => document.querySelector("[data-dialog-open]").click()); await settle(); };
          checks++;
          if (!(await open()) || !/^#.+/.test(await hash())) { bad++; console.log("FAIL", p.name, width, "opening sets the fragment", await hash()); }
          await pg.keyboard.press("Escape"); await settle();
          checks++;
          if ((await open()) || (await hash()) !== "") { bad++; console.log("FAIL", p.name, width, "Escape leaves the fragment", await hash()); }
          await reopen();
          // the backdrop (outside the dialog box): an information-only dialog closes by it, one with a form ignores it
          const info = await pg.evaluate(() => document.getElementById(document.querySelector("[data-dialog-open]").getAttribute("data-dialog-open").slice(1)).hasAttribute("data-informational"));
          await pg.mouse.click(2, 2); await settle();
          checks++;
          if (info && ((await open()) || (await hash()) !== "")) { bad++; console.log("FAIL", p.name, width, "backdrop click must close an informational dialog", await hash(), await open()); }
          if (!info && (!(await open()) || (await hash()) === "")) { bad++; console.log("FAIL", p.name, width, "backdrop click must not close a dialog with a form", await hash(), await open()); }
          if (!info) { await pg.keyboard.press("Escape"); await settle(); checks++; if (await open()) { bad++; console.log("FAIL", p.name, width, "Escape must still close it"); } }
          await reopen();
          await pg.evaluate(() => document.querySelector("[data-modal-close]").click()); await settle();
          await pg.reload({ waitUntil: "load" }); await settle();
          checks++;
          if ((await open()) || (await hash()) !== "") { bad++; console.log("FAIL", p.name, width, "a reload after closing reopens the dialog", await hash()); }
          await reopen();
          await pg.goBack(); await settle();
          checks++;
          if ((await open()) || (await hash()) !== "") { bad++; console.log("FAIL", p.name, width, "Back leaves the dialog open", await hash()); }
        }
      }
      await pg.close();
    }
  }
  await b.close();
  await bm.close();
  console.log(bad ? "FAILED " + bad + " of " + checks : "ALL OK " + checks + " checks"); process.exit(bad ? 1 : 0);
})();
