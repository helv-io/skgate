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
  let bad = 0, checks = 0, ref = null;
  for (const p of pages) {
    const solo = ["consent", "signed-out", "auth-error", "not-configured"].includes(p.name);
    for (const [width, height] of widths.flatMap(w => (solo ? [[w, 900], [w, 360]] : [[w, 900]]))) {
      const pg = await b.newPage();
      await pg.setViewport({ width, height });
      await pg.goto("file://" + dir + "/" + p.name + ".html");
      let res = await pg.evaluate(measure);
      checks++;
      const sig = await pg.evaluate(() => window.__choices || []);
      if (sig.length) {
        // Approve (primary) and Deny look the same at every width, and Approve is the filled one
        ref = ref || sig;
        if (JSON.stringify(sig) !== JSON.stringify(ref)) { bad++; console.log("FAIL", p.name, width + "x" + height, "buttons differ from the first width", JSON.stringify(sig), JSON.stringify(ref)); }
        const [deny, approve] = sig.map(x => x.split("|"));
        if (deny[0] !== "Deny" || approve[0] !== "Approve" || approve.slice(1, 4).join() === deny.slice(1, 4).join()) { bad++; console.log("FAIL", p.name, "Approve must be the filled primary", JSON.stringify(sig)); }
      }
      if (res.length) { bad++; console.log("FAIL", p.name, width + "x" + height, JSON.stringify(res)); }
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
  console.log(bad ? "FAILED " + bad + " of " + checks : "ALL OK " + checks + " checks"); process.exit(bad ? 1 : 0);
})();
