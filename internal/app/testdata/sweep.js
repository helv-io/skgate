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
  let bad = 0, checks = 0;
  for (const p of pages) {
    const solo = ["consent", "signed-out", "auth-error", "not-configured"].includes(p.name);
    for (const [width, height] of widths.flatMap(w => (solo ? [[w, 900], [w, 360]] : [[w, 900]]))) {
      const pg = await b.newPage();
      await pg.setViewport({ width, height });
      await pg.goto("file://" + dir + "/" + p.name + ".html");
      let res = await pg.evaluate(measure);
      checks++;
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
        }
      }
      await pg.close();
    }
  }
  await b.close();
  console.log(bad ? "FAILED " + bad + " of " + checks : "ALL OK " + checks + " checks"); process.exit(bad ? 1 : 0);
})();
