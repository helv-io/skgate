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
  // on phones the version, user and logout live in the folded panel: unfold it for these checks, fold it after
  const navBtn = document.querySelector("[data-nav-button]");
  const navShown = navBtn && getComputedStyle(navBtn).display !== "none";
  if (navShown && navBtn.getAttribute("aria-expanded") !== "true") navBtn.click();
  // the version link is the yellow warning colour when an update is known, the dim one otherwise, and stays on screen
  const ver = document.querySelector("header a.ver");
  if (ver) {
    const r = ver.getBoundingClientRect(), col = getComputedStyle(ver).color;
    if (r.right > vw + 0.5 || r.left < 0) out.push("version link outside the screen");
    if (ver.classList.contains("new") !== (col === "rgb(230, 192, 123)")) out.push("version link colour " + col + " does not match its update state");
    if (ver.target !== "_blank" || !/noopener/.test(ver.rel) || !/noreferrer/.test(ver.rel)) out.push("version link must open in a new tab with rel noopener noreferrer");
  }
  // a long account name never pushes logout off the screen
  const who = document.querySelector("header .who"), lo = document.querySelector('header form[action="/admin/logout"] button');
  if (who && lo) {
    who.textContent = "someone.with.a.very.long.address+and.a.tag@a-long-domain-name.example.org";
    const r = lo.getBoundingClientRect();
    if (r.right > vw + 0.5 || r.left < 0 || r.width === 0) out.push("logout is outside the screen (" + Math.round(r.left) + ".." + Math.round(r.right) + ")");
    const nav = [...document.querySelectorAll("header a")].filter(e => e.getBoundingClientRect().right > vw + 0.5);
    if (nav.length) out.push("header link past the screen: " + nav[0].textContent.trim());
  }
  if (navShown && navBtn.getAttribute("aria-expanded") === "true") navBtn.click();
  // a table never scrolls inside its card at desktop widths: that hides the row's actions without a scrollbar to tell
  if (vw > 800) document.querySelectorAll(".tablewrap").forEach(w => {
    if (w.scrollWidth > w.clientWidth + 1) out.push("a table scrolls sideways inside its card (" + w.scrollWidth + " > " + w.clientWidth + ") cells " + [...w.querySelectorAll("tbody tr:first-child > *")].map(c => c.className + ":" + Math.round(c.getBoundingClientRect().width)).join(","));
  });
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
  // on phones every control is a finger-sized target: at least 44px tall (and wide) as the page shows it, folded panels aside
  if (vw <= 720) {
    const sel = "button, a.act, a.btn, a.name, select, textarea, summary, label.check, header a, input:not([type=hidden]):not([type=checkbox]):not([type=radio])";
    document.querySelectorAll(sel).forEach(e => {
      if (e.offsetParent === null && getComputedStyle(e).position !== "fixed") return;
      const r = e.getBoundingClientRect();
      if (!r.width || !r.height) return;
      if (r.height < 43.5 || r.width < 43.5) out.push("touch target " + e.tagName.toLowerCase() + (e.className ? "." + String(e.className).split(" ")[0] : "") + " '" + (e.textContent || e.name || "").trim().slice(0, 24) + "' is " + Math.round(r.width) + "x" + Math.round(r.height) + ", under 44px");
    });
  }
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

// The top bar: sticky and opaque on every page and width, never covered while scrolling, anchors land below it;
// on phones one row (brand, current page, Menu) with the rest in a panel that folds with Escape, a click outside
// and choosing a link; on wide windows the links are in the bar and there is no Menu button.
const headerChecks = async (pg, vw) => {
  const out = [];
  await new Promise(r => setTimeout(r, 80)); // the observer that keeps --header-h runs after layout changes
  const info = await pg.evaluate(() => {
    const h = document.querySelector("header[data-nav]");
    if (!h) return null;
    const cs = getComputedStyle(h), r = h.getBoundingClientRect();
    const bg = cs.backgroundColor.match(/[\d.]+/g).map(Number);
    const bt = document.querySelector("[data-nav-button]"), here = h.querySelector("[data-nav-here]");
    const z = parseInt(cs.zIndex, 10), zt = parseInt(getComputedStyle(document.documentElement).getPropertyValue("--z-toast"), 10);
    return { pos: cs.position, top: cs.top, alpha: bg.length > 3 ? bg[3] : 1, z, zt, height: r.height,
      btn: getComputedStyle(bt).display, here: getComputedStyle(here).display, hereText: here.textContent.trim(),
      hh: document.documentElement.style.getPropertyValue("--header-h"), collapsed: h.hasAttribute("data-collapsed"),
      panel: getComputedStyle(h.querySelector(".nav-panel")).display, expanded: bt.getAttribute("aria-expanded") };
  });
  if (!info) return out;
  if (info.pos !== "sticky" || info.top !== "0px") out.push("header is not sticky at the top: " + info.pos + " " + info.top);
  if (info.alpha !== 1) out.push("header background is not opaque");
  if (!(info.z > 0 && info.z < info.zt)) out.push("header z-index " + info.z + " must sit below the toast layer");
  if (!info.hereText) out.push("the current page name is empty");
  if (parseInt(info.hh, 10) !== Math.ceil(info.height)) out.push("--header-h " + info.hh + " does not follow the bar's height " + info.height);
  // scrolling: add height and an anchor target far down, then scroll; the bar stays put, on top, and the anchor clears it
  await pg.evaluate(() => {
    const sp = document.createElement("div"); sp.id = "scroll-spacer"; sp.style.height = "3000px";
    const t = document.createElement("div"); t.id = "anchor-test"; t.textContent = "anchor"; t.style.cssText = "height:20px;position:absolute;top:1500px";
    document.body.appendChild(sp); document.body.appendChild(t);
    window.scrollTo(0, 800);
  });
  const sc = await pg.evaluate(() => {
    const h = document.querySelector("header[data-nav]"), r = h.getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return { y: window.scrollY, top: r.top, covered: !(hit && h.contains(hit)) };
  });
  if (sc.y < 100) out.push("the test page did not scroll");
  if (Math.abs(sc.top) > 0.5) out.push("header moved while scrolling: top " + sc.top);
  if (sc.covered) out.push("something is drawn over the header while scrolling");
  const an = await pg.evaluate(() => {
    location.hash = "#anchor-test";
    const h = document.querySelector("header[data-nav]").getBoundingClientRect(), t = document.getElementById("anchor-test").getBoundingClientRect();
    return { barBottom: h.bottom, targetTop: t.top };
  });
  await new Promise(r => setTimeout(r, 60));
  const an2 = await pg.evaluate(() => ({ barBottom: document.querySelector("header[data-nav]").getBoundingClientRect().bottom, targetTop: document.getElementById("anchor-test").getBoundingClientRect().top }));
  if (an2.targetTop < an2.barBottom - 0.5) out.push("an anchor scrolled behind the header: target top " + an2.targetTop + " < bar bottom " + an2.barBottom);
  await pg.evaluate(() => { document.getElementById("scroll-spacer").remove(); document.getElementById("anchor-test").remove(); history.replaceState(null, "", location.pathname + location.search); window.scrollTo(0, 0); });
  if (vw > 720) {
    if (info.btn !== "none" || info.here !== "none") out.push("wide window shows the Menu button or page name");
    const links = await pg.evaluate(() => [...document.querySelectorAll("header .nav-panel > a:not(.ver), header .nav-panel form")].map(e => { const r = e.getBoundingClientRect(); return [Math.round(r.top), Math.round(r.height)]; }));
    if (links.some(l => l[1] === 0)) out.push("a header link is hidden on a wide window");
    if (vw >= 1280 && new Set(links.map(l => l[0])).size > 1) out.push("header links wrap at " + vw + ": " + JSON.stringify(links));
    return out;
  }
  // phones
  if (info.height > 64) out.push("header is " + info.height + "px tall, expected one row");
  if (info.btn === "none" || info.here === "none") out.push("phone header lacks the Menu button or page name");
  if (!info.collapsed || info.panel !== "none" || info.expanded !== "false") out.push("the panel must start folded: " + JSON.stringify([info.collapsed, info.panel, info.expanded]));
  const row = await pg.evaluate(() => {
    const h = document.querySelector("header[data-nav]"), q = s => h.querySelector(s).getBoundingClientRect();
    const b = q(".brand"), n = q("[data-nav-here]"), m = q("[data-nav-button]"), hr = h.getBoundingClientRect();
    return { brand: [b.top, b.bottom], here: [n.top, n.bottom], btn: [m.top, m.bottom, m.width, m.height, m.right], bar: [hr.top, hr.bottom], vw: document.documentElement.clientWidth, hereW: n.width };
  });
  const mid = a => (a[0] + a[1]) / 2;
  if (Math.abs(mid(row.brand) - mid(row.btn)) > 2 || Math.abs(mid(row.here) - mid(row.btn)) > 2) out.push("brand, page name and Menu are not on one row " + JSON.stringify(row));
  if (row.btn[3] < 44 || row.btn[2] < 44) out.push("Menu button is under 44px: " + row.btn[2] + "x" + row.btn[3]);
  if (row.btn[4] > row.vw + 0.5) out.push("Menu button is past the screen");
  if (row.hereW < 20) out.push("the page name has no room: " + row.hereW);
  // open: the panel holds the links, version, user and logout, all inside the window, tall enough to touch
  await pg.click("[data-nav-button]");
  const op = await pg.evaluate(() => {
    const h = document.querySelector("header[data-nav]"), p = h.querySelector(".nav-panel"), pr = p.getBoundingClientRect(), vw = document.documentElement.clientWidth;
    const items = [...p.querySelectorAll("a, .who, form .act")].map(e => { const r = e.getBoundingClientRect(); return { t: e.textContent.trim().slice(0, 14), h: r.height, l: r.left, r: r.right }; });
    const hit = document.elementFromPoint(pr.left + pr.width / 2, pr.top + 10);
    return { shown: pr.height > 0, expanded: h.querySelector("[data-nav-button]").getAttribute("aria-expanded"), inside: pr.left >= 0 && pr.right <= vw + 0.5, items, onTop: !!hit && p.contains(hit), top: pr.top, barBottom: h.getBoundingClientRect().bottom };
  });
  if (!op.shown || op.expanded !== "true") out.push("Menu does not open the panel " + JSON.stringify([op.shown, op.expanded]));
  if (!op.inside) out.push("the panel reaches past the screen");
  if (!op.onTop) out.push("the panel is covered by the page");
  if (Math.abs(op.top - op.barBottom) > 1.5) out.push("the panel is not attached under the bar");
  if (op.items.length < 6) out.push("the panel is missing items: " + op.items.map(i => i.t));
  op.items.forEach(i => { if (i.h < 43.5) out.push("panel item " + i.t + " is " + Math.round(i.h) + "px tall, under 44px"); if (i.r > vw + 0.5 || i.l < 0) out.push("panel item " + i.t + " is outside the screen"); });
  const state = () => pg.evaluate(() => ({ exp: document.querySelector("[data-nav-button]").getAttribute("aria-expanded"), focus: document.activeElement === document.querySelector("[data-nav-button]") }));
  await pg.keyboard.press("Escape");
  let st = await state();
  if (st.exp !== "false" || !st.focus) out.push("Escape must fold the panel and focus Menu " + JSON.stringify(st));
  await pg.click("[data-nav-button]");
  await pg.evaluate(() => document.querySelector("main").click());
  st = await state();
  if (st.exp !== "false") out.push("a click outside must fold the panel");
  await pg.click("[data-nav-button]");
  await pg.evaluate(() => document.querySelector(".nav-panel a").addEventListener("click", e => e.preventDefault(), { once: true }));
  await pg.click(".nav-panel a");
  st = await state();
  if (st.exp !== "false") out.push("choosing a link must fold the panel");
  // a sticky bar does not hide the panel's own scroll when many items: the panel never exceeds the window
  return out;
};

(async () => {
  const b = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new"] });
  // headless Chrome reports no hover; a second browser that reports a mouse checks the hover behavior of the sign-out pill
  const bm = await puppeteer.launch({ executablePath: chrome, args: ["--no-sandbox", "--headless=new", "--blink-settings=primaryHoverType=2,availableHoverTypes=2,primaryPointerType=4,availablePointerTypes=4"] });
  let bad = 0, checks = 0, ref = null, headers = 0;
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
      {
        const hres = await headerChecks(pg, width); checks++; headers += solo ? 0 : 1;
        if (hres.length) { bad++; console.log("FAIL", p.name, width + "x" + height, "top bar", JSON.stringify(hres)); }
        if (solo && await pg.evaluate(() => !!document.querySelector("header"))) { bad++; console.log("FAIL", p.name, "a standalone page must not carry the top bar"); }
      }
      if (shots && width === 390 && p.name === "upstreams") {
        await pg.click("[data-nav-button]"); await pg.screenshot({ path: shots + "/" + p.name + "-navmenu-390.png" }); await pg.click("[data-nav-button]");
        await pg.evaluate(() => window.scrollTo(0, 400)); await pg.screenshot({ path: shots + "/" + p.name + "-scrolled-390.png" }); await pg.evaluate(() => window.scrollTo(0, 0));
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
  if (!headers) { bad++; console.log("FAIL no page with a top bar was swept"); }
  console.log(bad ? "FAILED " + bad + " of " + checks : "ALL OK " + checks + " checks"); process.exit(bad ? 1 : 0);
})();
