// The prefix field lowercases as you type, drops anything else, and flashes its hint red for a second.
// Usage: node prefix.js <path to app.js>
const {JSDOM} = require("jsdom");
const fs = require("fs");
const js = fs.readFileSync(process.argv[2], "utf8");
let fails = 0;
const ok = (c, m) => { if (!c) { fails++; console.log("FAIL", m); } else console.log("ok  ", m); };
const wait = ms => new Promise(r => setTimeout(r, ms));
const html = `<!doctype html><body><form>
<select name="preset" data-preset>
<option value="openai" data-set-prefix="openai" data-set-base="https://api.openai.com/v1">OpenAI</option>
<option value="custom" data-set-prefix="custom" data-set-base="">Custom endpoint</option>
</select>
<input name="prefix" value="openai" maxlength="16" data-prefix>
<p class="muted" data-prefix-hint>Lowercase letters and numbers</p>
<input name="base" value="https://api.openai.com/v1">
</form></body>`;
(async () => {
  const dom = new JSDOM(html, { runScripts: "outside-only", pretendToBeVisual: true, url: "http://localhost/admin" });
  const w = dom.window, d = w.document;
  w.eval(js);
  const field = d.querySelector("[data-prefix]"), hint = d.querySelector("[data-prefix-hint]");
  const type = (v) => { field.value = v; field.dispatchEvent(new w.Event("input", { bubbles: true })); };
  type("A");
  ok(field.value === "a" && !hint.classList.contains("bad"), "uppercase becomes lowercase with no flash: " + field.value);
  type("ab-c!");
  ok(field.value === "abc" && hint.classList.contains("bad"), "other characters are dropped and the hint turns red: " + field.value + " " + hint.className);
  ok(!d.querySelector(".toast"), "no toast");
  hint.classList.remove("bad");
  type("X-Y!");
  ok(field.value === "xy" && hint.classList.contains("bad"), "a paste is lowercased, stripped, and flashes once: " + field.value);
  type("a".repeat(20));
  ok(field.value.length === 16 && hint.classList.contains("bad"), "pasted length stops at 16: " + field.value.length);
  await wait(1100);
  ok(!hint.classList.contains("bad"), "the hint returns to muted");
  d.querySelector("[data-preset]").value = "custom";
  d.querySelector("[data-preset]").dispatchEvent(new w.Event("change", { bubbles: true }));
  ok(field.value === "custom" && d.querySelector("[name=base]").value === "", "choosing a preset fills the prefix: " + field.value);
  if (fails) process.exit(1);
  console.log("ALL OK");
})().catch(e => { console.log(e.stack || String(e)); process.exit(1); });
