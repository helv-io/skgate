// Entry point of internal/admin/static/codemirror.js. Rebuild with `npm ci && npm run build` in this directory
// (docs/development.md, "Code box"). It turns every <textarea data-code="json|yaml|toml|auto"> into a CodeMirror 6
// editor. The textarea stays the form field: it keeps its name and value (kept in step on every change), still takes
// setCustomValidity from the page's JSON check (an invalid hidden field blocks the form; the status line says why), and
// is all the page shows without script.
// Everything here is CSP-clean: the editor lives in a shadow root so CodeMirror's styles go in through
// adoptedStyleSheets (no <style> element, no style attribute from markup), and no image, font or network request is made.
import { EditorState, Compartment, Prec } from "@codemirror/state";
import { EditorView, keymap, placeholder, highlightSpecialChars } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { HighlightStyle, StreamLanguage, bracketMatching, indentOnInput, syntaxHighlighting, syntaxTree } from "@codemirror/language";
import { linter } from "@codemirror/lint";
import { json, jsonParseLinter } from "@codemirror/lang-json";
import { yaml } from "@codemirror/lang-yaml";
import { toml } from "@codemirror/legacy-modes/mode/toml";
import { tags as t } from "@lezer/highlight";
import { parse as parseToml } from "smol-toml";

const NAME = { json: "JSON", yaml: "YAML", toml: "TOML" };

// "auto": guess the format from the text (JSON starts with { or [, TOML has key = value and no key: value).
function detect(text) {
  const s = text.replace(/^\s+/, "");
  if (!s) return "yaml";
  if (s[0] === "{" || s[0] === "[") return /^\[\[?[A-Za-z0-9_."' .-]+\]\]?\s*(#.*)?(\n|$)/.test(s) ? "toml" : "json";
  if (/^\s*[A-Za-z0-9_."'-]+\s*=\s*\S/m.test(text) && !/^\s*[A-Za-z0-9_"'-]+\s*:(\s|$)/m.test(text)) return "toml";
  return "yaml";
}

function language(fmt) {
  return fmt === "json" ? json() : fmt === "toml" ? StreamLanguage.define(toml) : yaml();
}

// One diagnostic list per format: JSON.parse for JSON, smol-toml for TOML, the syntax tree's error nodes for YAML.
function diagnostics(fmt, view) {
  const doc = view.state.doc, text = doc.toString();
  if (!text.trim()) return [];
  return widen(raw(fmt, view, doc, text), doc);
}

// A mistake that sits at the end of a line or of the text has no character to underline; take the one before it.
function widen(list, doc) {
  const ch = (p) => doc.sliceString(p, p + 1);
  return list.map((d) => {
    let from = Math.min(d.from, doc.length), to = Math.max(d.to, from);
    if (from >= doc.length || ch(from) === "\n") {
      from = Math.min(from, doc.length - 1);
      while (from > 0 && ch(from) === "\n") from--;
      to = from + 1;
    } else if (to === from) to = from + 1;
    return { ...d, from, to: Math.min(to, doc.length) };
  });
}

function raw(fmt, view, doc, text) {
  if (fmt === "json") return jsonParseLinter()(view);
  if (fmt === "toml") {
    try { parseToml(text); return []; } catch (e) {
      const line = doc.line(Math.min(Math.max(e.line || 1, 1), doc.lines));
      const from = Math.min(line.from + Math.max((e.column || 1) - 1, 0), line.to);
      return [{ from: Math.min(from, line.to), to: Math.max(Math.min(from + 1, doc.length), from), severity: "error", message: String(e.message).split("\n")[0] }];
    }
  }
  const out = [];
  syntaxTree(view.state).iterate({ enter(n) { if (n.type.isError && out.length < 20) out.push({ from: n.from, to: Math.max(n.to, Math.min(n.from + 1, doc.length)), severity: "error", message: "Syntax error" }); } });
  return out;
}

const highlight = HighlightStyle.define([
  { tag: [t.propertyName, t.definition(t.propertyName), t.variableName, t.labelName], color: "var(--acc)" },
  { tag: [t.heading, t.namespace], color: "var(--acc)", fontWeight: "600" },
  { tag: [t.string, t.special(t.string)], color: "var(--ok)" },
  { tag: [t.number, t.integer, t.float, t.literal], color: "var(--warn)" },
  { tag: [t.bool, t.null, t.atom, t.keyword, t.meta, t.typeName], color: "var(--kw)" },
  { tag: [t.punctuation, t.separator, t.bracket, t.squareBracket, t.brace, t.operator, t.comment, t.lineComment], color: "var(--dim)" },
  { tag: [t.comment, t.lineComment], fontStyle: "italic" },
  { tag: t.invalid, color: "var(--bad)" },
]);

// Colours and sizes come from the page's CSS variables (app.css), so the editor follows the rest of the UI.
const theme = EditorView.theme({
  "&": { color: "var(--fg)", backgroundColor: "var(--well)", border: "1px solid var(--line)", fontSize: "var(--code-size)", height: "calc(var(--code-rows) * 1.5em + 12px)", minHeight: "120px", resize: "vertical", overflow: "hidden" },
  "&.cm-focused": { outline: "none", borderColor: "var(--acc)" },
  "&.cm-invalid": { borderColor: "var(--bad)" },
  ".cm-scroller": { fontFamily: "var(--mono)", lineHeight: "1.5", overflow: "auto" },
  ".cm-content": { padding: "5px 0", caretColor: "var(--fg)" },
  ".cm-line": { padding: "0 8px" },
  ".cm-cursor, .cm-dropCursor": { borderLeftColor: "var(--fg)" },
  "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, ::selection": { backgroundColor: "rgba(127,184,255,.35) !important" },
  ".cm-placeholder": { color: "var(--dim)" },
  "&.cm-focused .cm-matchingBracket": { backgroundColor: "var(--press)", color: "var(--fg)" },
  "&.cm-focused .cm-nonmatchingBracket": { backgroundColor: "var(--bad-bg)", color: "var(--bad)" },
  // the error underline: CodeMirror draws it with a data: image, which the CSP does not allow
  ".cm-lintRange-error": { backgroundImage: "none !important", textDecoration: "underline wavy var(--bad)", textUnderlineOffset: "3px", backgroundColor: "var(--bad-bg)" },
  ".cm-tooltip": { backgroundColor: "var(--panel)", border: "1px solid var(--line)", color: "var(--fg)", fontFamily: "var(--sans)", fontSize: "13px" },
  ".cm-diagnostic": { padding: "4px 8px", borderLeft: "3px solid var(--bad)" },
}, { dark: true });

// YAML: Enter keeps the indent of the line and adds two spaces after a line ending in a colon (the YAML grammar does
// not know that a key without a value opens a block). Other formats use CodeMirror's own indent.
function yamlEnter(getFmt) {
  return (view) => {
    if (getFmt() !== "yaml") return false;
    const { state } = view, r = state.selection.main, line = state.doc.lineAt(r.from), before = state.doc.sliceString(line.from, r.from);
    const indent = /^ */.exec(before)[0] + (/:\s*$/.test(before.replace(/\s+#.*$/, "")) ? "  " : "");
    view.dispatch(state.update({ changes: { from: r.from, to: r.to, insert: "\n" + indent }, selection: { anchor: r.from + 1 + indent.length }, scrollIntoView: true, userEvent: "input" }));
    return true;
  };
}

function lineOf(doc, pos) { return doc.lineAt(Math.min(pos, doc.length)).number; }

function mount(ta) {
  if (ta.dataset.codeMounted) return;
  ta.dataset.codeMounted = "1";
  const want = ta.getAttribute("data-code"), field = ta.closest(".field"), label = ta.closest("label");
  const status = field ? field.querySelector("[data-code-status]") : null, own = ta.hasAttribute("data-json-check");
  const host = document.createElement("div");
  host.className = "codehost";
  host.style.setProperty("--code-rows", String(ta.rows || 8));
  (label || ta).after(host);
  // CodeMirror adds its rules as a <style> element when it lives in the document, which the CSP refuses; inside a
  // shadow root it uses a constructed stylesheet (adoptedStyleSheets), which the CSP allows. The page's CSS variables
  // still reach in, so the colours and sizes come from app.css.
  const shadow = host.attachShadow({ mode: "open" }), mountAt = document.createElement("div");
  shadow.appendChild(mountAt);
  ta.classList.add("code-src");
  if (status) { if (!status.id) status.id = "code-status-" + Math.random().toString(36).slice(2, 8); ta.setAttribute("aria-describedby", status.id); }

  let fmt = want === "auto" ? detect(ta.value) : want, syncing = false;
  const lang = new Compartment();
  function say(ds) {
    if (!status || own) return;
    const text = view.state.doc.toString(), bad = ds.length > 0;
    status.classList.toggle("bad", bad); status.classList.toggle("muted", !bad);
    if (!text.trim()) status.textContent = "";
    else if (bad) status.textContent = NAME[fmt] + " \u00b7 line " + lineOf(view.state.doc, ds[0].from) + ": " + ds[0].message.replace(/ in JSON at position.*$/, "");
    else status.textContent = want === "auto" ? "Reads as " + NAME[fmt] : "Valid " + NAME[fmt] + " syntax";
  }
  const view = new EditorView({
    parent: mountAt, root: shadow,
    state: EditorState.create({
      doc: ta.value,
      extensions: [
        theme, syntaxHighlighting(highlight), lang.of(language(fmt)),
        history(), Prec.high(keymap.of([{ key: "Enter", run: yamlEnter(() => fmt) }])), keymap.of([...defaultKeymap, ...historyKeymap]), indentOnInput(), bracketMatching(), highlightSpecialChars(),
        linter((v) => { const ds = diagnostics(fmt, v); say(ds); return ds; }, { delay: 250 }),
        ta.placeholder ? placeholder(ta.placeholder) : [],
        EditorView.contentAttributes.of({ spellcheck: "false", autocapitalize: "off", autocorrect: "off", autocomplete: "off", "aria-label": (label ? label.textContent : ta.name).trim().split("\n")[0].slice(0, 80) }),
        EditorView.updateListener.of((u) => {
          if (!u.docChanged) return;
          if (want === "auto") {
            const f = detect(u.state.doc.toString());
            if (f !== fmt) { fmt = f; view.dispatch({ effects: lang.reconfigure(language(fmt)) }); }
          }
          syncing = true; ta.value = u.state.doc.toString(); ta.dispatchEvent(new Event("input", { bubbles: true })); syncing = false;
        }),
      ],
    }),
  });
  // text set by script (the repair button) or by a form reset reaches the editor
  ta.addEventListener("input", () => {
    if (syncing || ta.value === view.state.doc.toString()) return;
    view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: ta.value } });
  });
  if (ta.form) ta.form.addEventListener("reset", () => setTimeout(() => ta.dispatchEvent(new Event("input")), 0));
  // a click on the field's label puts the cursor in the editor (the textarea itself is out of sight)
  ta.addEventListener("focus", () => view.focus());
  if (label) label.addEventListener("click", () => view.focus());
  // the page's JSON check marks the textarea invalid; show it on the editor
  new MutationObserver(() => view.dom.classList.toggle("cm-invalid", ta.getAttribute("aria-invalid") === "true"))
    .observe(ta, { attributes: true, attributeFilter: ["aria-invalid"] });
  view.dom.classList.toggle("cm-invalid", ta.getAttribute("aria-invalid") === "true");
}

// Without constructed stylesheets (an old browser) CodeMirror would have to add a <style> element, which the CSP
// refuses; the plain textarea stays.
if (document.adoptedStyleSheets !== undefined && window.CSSStyleSheet && "replaceSync" in CSSStyleSheet.prototype) {
  document.querySelectorAll("textarea[data-code]").forEach(mount);
}
