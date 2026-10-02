// Polls the device sign-in state ([data-poll] holds the URL) and reloads the status page when it changes.
(function () {
  var el = document.querySelector("[data-poll]");
  if (!el) return;
  var url = el.getAttribute("data-poll");
  var t = setInterval(function () {
    fetch(url, { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state !== "pending") { clearInterval(t); location.href = "/admin"; }
      })
      .catch(function () {});
  }, 3000);
})();

// Toasts: expire after 5 seconds, click to dismiss.
(function () {
  function drop(el) {
    el.classList.add("gone");
    setTimeout(function () { if (el.parentNode) el.parentNode.removeChild(el); }, 250);
  }
  Array.prototype.forEach.call(document.querySelectorAll(".toast"), function (el) {
    el.addEventListener("click", function () { drop(el); });
    setTimeout(function () { drop(el); }, 5000);
  });
})();

// Confirmation: a form with data-confirm="text" opens the shared modal (<dialog data-modal>) on submit
// instead of submitting. Title and confirm label are the submit button's label; a button with class
// "danger" makes the confirm button destructive. Escape, Cancel and a click on the backdrop close it.
(function () {
  var dlg = document.querySelector("[data-modal]");
  if (!dlg) return;
  var titleEl = dlg.querySelector("[data-modal-title]");
  var textEl = dlg.querySelector("[data-modal-text]");
  var okBtn = dlg.querySelector("[data-modal-ok]");
  var cancelBtn = dlg.querySelector("[data-modal-cancel]");
  var closeBtn = dlg.querySelector("[data-modal-close]");
  var bodyEl = dlg.querySelector("[data-modal-body]");
  var actionsEl = dlg.querySelector("[data-modal-actions]");
  var pending = null; // { form, submitter, opener }
  var opener = null; // the button that opened content

  // mode switches the one dialog between a confirmation and content copied from a template.
  function mode(content) {
    dlg.classList.toggle("wide", content);
    bodyEl.hidden = closeBtn.hidden = !content;
    textEl.hidden = actionsEl.hidden = content;
    if (!content) bodyEl.textContent = "";
  }
  function close() {
    if (dlg.close && dlg.open) dlg.close(); else dlg.removeAttribute("open");
    mode(false);
  }
  function restoreFocus() {
    var p = pending, o = opener;
    pending = null; opener = null;
    var f = (p && p.opener) || o;
    if (f && f.focus) f.focus();
    mode(false);
  }
  function openContent(sel, from) {
    var tpl = null;
    try { tpl = document.querySelector(sel); } catch (err) {}
    if (!tpl || !tpl.hasAttribute("data-dialog-content")) return;
    mode(true);
    titleEl.textContent = tpl.getAttribute("data-title") || "";
    bodyEl.appendChild(tpl.content.cloneNode(true));
    opener = from || null;
    if (dlg.showModal) { if (!dlg.open) dlg.showModal(); } else dlg.setAttribute("open", "");
  }
  document.addEventListener("click", function (e) {
    var o = e.target && e.target.closest ? e.target.closest("[data-dialog-open]") : null;
    if (o) openContent(o.getAttribute("data-dialog-open"), o);
  });
  closeBtn.addEventListener("click", close);
  if (location.hash.length > 1) openContent(location.hash, null);
  function label(btn) { return ((btn && btn.textContent) || "").trim(); }

  document.addEventListener("submit", function (e) {
    var form = e.target;
    var text = form && form.getAttribute && form.getAttribute("data-confirm");
    if (!text) return;
    if (form._confirmed) { form._confirmed = false; return; }
    e.preventDefault();
    var btn = e.submitter || form.querySelector("button:not([type=button])");
    var word = label(btn);
    var danger = !!(btn && btn.classList && btn.classList.contains("danger"));
    mode(false);
    pending = { form: form, submitter: btn, opener: btn || document.activeElement };
    titleEl.textContent = form.getAttribute("data-confirm-title") || (word ? word.charAt(0).toUpperCase() + word.slice(1) : "Confirm");
    textEl.textContent = text;
    okBtn.textContent = form.getAttribute("data-confirm-ok") || (word ? word.charAt(0).toUpperCase() + word.slice(1) : "Confirm");
    okBtn.classList.toggle("confirm-danger", danger);
    if (dlg.showModal) dlg.showModal(); else dlg.setAttribute("open", "");
    (danger ? cancelBtn : okBtn).focus(); // a destructive action is never the default key press
  });

  okBtn.addEventListener("click", function () {
    var p = pending;
    if (!p) return close();
    close();
    p.form._confirmed = true;
    if (p.form.requestSubmit) {
      try { p.form.requestSubmit(p.submitter && p.submitter.form === p.form ? p.submitter : undefined); } catch (err) { p.form.requestSubmit(); }
    } else {
      p.form.submit();
    }
    pending = null;
  });
  cancelBtn.addEventListener("click", close);
  // A click on the backdrop lands on the dialog element itself; clicks inside land on its children.
  dlg.addEventListener("click", function (e) { if (e.target === dlg) close(); });
  // Escape fires cancel (native) and then close; both end up here.
  dlg.addEventListener("cancel", function () { /* native close follows */ });
  dlg.addEventListener("close", restoreFocus);
  dlg.addEventListener("keydown", function (e) {
    if (e.key === "Escape") { e.preventDefault(); close(); restoreFocus(); }
  });
})();

// Copy buttons: data-copy="#id" copies that element's text. Clipboard API, with an execCommand fallback.
document.addEventListener("click", function (e) {
  var b = e.target && e.target.closest ? e.target.closest("[data-copy]") : null;
  if (!b) return;
  var src = document.querySelector(b.getAttribute("data-copy"));
  if (!src) return;
  var text = src.textContent.trim();
  function done(ok) {
    var old = b.getAttribute("data-label") || b.textContent;
    b.setAttribute("data-label", old);
    b.textContent = ok ? "Copied" : "Copy failed";
    setTimeout(function () { b.textContent = old; }, 1500);
  }
  function fallback() {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.className = "offscreen";
    document.body.appendChild(ta);
    ta.select();
    var ok = false;
    try { ok = document.execCommand("copy"); } catch (err) {}
    document.body.removeChild(ta);
    done(ok);
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(function () { done(true); }, fallback);
  } else {
    fallback();
  }
});

// Presets: choosing an option of a [data-preset] select copies its data-set-<field> attributes into the
// form fields of that name.
document.addEventListener("change", function (e) {
  var sel = e.target && e.target.closest ? e.target.closest("[data-preset]") : null;
  if (!sel || !sel.form) return;
  var opt = sel.options[sel.selectedIndex];
  Array.prototype.forEach.call(opt.attributes, function (a) {
    if (a.name.indexOf("data-set-") !== 0) return;
    var f = sel.form.elements[a.name.slice(9)];
    if (f) f.value = a.value;
  });
});

// Pick: a [data-pick] select whose empty option ("Custom") reveals the [data-pick-custom] input next to it.
(function () {
  function apply(sel) {
    var inp = sel.parentNode.querySelector("[data-pick-custom]");
    if (!inp) return;
    inp.hidden = sel.value !== "";
    if (!inp.hidden && document.activeElement === sel) inp.focus();
  }
  document.addEventListener("change", function (e) {
    var sel = e.target && e.target.closest ? e.target.closest("[data-pick]") : null;
    if (sel) apply(sel);
  });
  Array.prototype.forEach.call(document.querySelectorAll("[data-pick]"), apply);
})();

// Upstream form: [data-kind-select] (a select, or a hidden input on the edit page) picks which
// [data-kind] groups are visible. Without JavaScript every group stays visible.
(function () {
  var sel = document.querySelector("[data-kind-select]");
  if (!sel) return;
  function apply() {
    Array.prototype.forEach.call(document.querySelectorAll("[data-kind]"), function (el) {
      el.hidden = el.getAttribute("data-kind").split(" ").indexOf(sel.value) < 0;
    });
  }
  sel.addEventListener("change", apply);
  apply();
})();

// [data-when-select] shows the [data-when="a b"] fields that apply to its value. Without
// JavaScript every field stays visible.
(function () {
  var sel = document.querySelector("[data-when-select]");
  if (!sel) return;
  function apply() {
    Array.prototype.forEach.call(document.querySelectorAll("[data-when]"), function (el) {
      el.hidden = el.getAttribute("data-when").split(" ").indexOf(sel.value) < 0;
    });
  }
  sel.addEventListener("change", apply);
  apply();
})();

// Pairs (env vars, headers): [data-pairs] holds [data-pairs-rows], a <template data-pairs-template>
// row and an [data-pairs-add] button. Add appends a row; [data-pairs-remove] deletes its own row.
// The first row has no remove button, and blank rows are ignored by the server.
document.addEventListener("click", function (e) {
  var t = e.target && e.target.closest ? e.target : null;
  if (!t) return;
  var add = t.closest("[data-pairs-add]");
  if (add) {
    var box = add.closest("[data-pairs]");
    var tpl = box.querySelector("[data-pairs-template]");
    var rows = box.querySelector("[data-pairs-rows]");
    rows.appendChild(tpl.content.cloneNode(true));
    var inputs = rows.querySelectorAll("input");
    if (inputs.length >= 2) inputs[inputs.length - 2].focus();
    return;
  }
  var rm = t.closest("[data-pairs-remove]");
  if (rm) {
    var row = rm.closest(".pair");
    if (row && row.parentNode) row.parentNode.removeChild(row);
  }
});


// Suggest configuration: [data-suggest="url"] posts the source and token of its form and fills the manual fields
// with the validated answer, for review. Nothing is saved. The fields stay usable whatever the outcome.
(function () {
  function rows(form, name) {
    var first = form.querySelector('[name="' + name + '"]');
    return first ? first.closest("[data-pairs]") : null;
  }
  // fill replaces the rows of a dynamic list: items are strings (single) or [name, value] (pairs).
  function fill(box, items, pairs) {
    if (!box) return;
    var list = box.querySelector("[data-pairs-rows]");
    var tpl = box.querySelector("[data-pairs-template]");
    while (list.children.length > 1) list.removeChild(list.lastChild);
    var need = Math.max(items.length, 1);
    for (var i = 1; i < need; i++) list.appendChild(tpl.content.cloneNode(true));
    Array.prototype.forEach.call(list.children, function (row, i) {
      var inputs = row.querySelectorAll("input");
      var it = items[i];
      inputs[0].value = it === undefined ? "" : pairs ? it[0] : it;
      if (pairs) inputs[1].value = it === undefined ? "" : it[1];
    });
  }
  function set(form, name, v) { var f = form.elements[name]; if (f) f.value = v; }
  function apply(form, r) {
    if (form.elements.alias && form.elements.alias.type !== "hidden" && !form.elements.alias.value) set(form, "alias", r.alias);
    var pick = form.elements.command_pick;
    if (pick) {
      var known = Array.prototype.some.call(pick.options, function (o) { return o.value === r.command; });
      pick.value = known ? r.command : "";
      pick.dispatchEvent(new Event("change", { bubbles: true }));
      if (!known) set(form, "command", r.command);
    }
    fill(rows(form, "args"), r.args || [], false);
    fill(rows(form, "env_name"), (r.env || []).map(function (e) { return [e.name, e.value]; }), true);
    set(form, "install", r.install || "");
    set(form, "startup_secs", r.startup_secs || "");
    if (r.kind === "git") { set(form, "source", r.git_url || ""); set(form, "git_ref", r.git_ref || ""); }
    var d = form.querySelector("[data-manual]");
    if (d) d.open = true;
  }
  function show(form, cls, label, lines) {
    var out = form.querySelector("[data-suggest-out]");
    var pill = out.querySelector("[data-suggest-pill]");
    var ul = out.querySelector("[data-suggest-list]");
    pill.className = "pill " + cls;
    pill.textContent = label;
    ul.textContent = "";
    lines.forEach(function (l) { var li = document.createElement("li"); li.textContent = l; ul.appendChild(li); });
    out.hidden = false;
  }
  document.addEventListener("click", function (e) {
    var b = e.target && e.target.closest ? e.target.closest("[data-suggest]") : null;
    if (!b || b.disabled) return;
    var form = b.form || b.closest("form");
    var csrf = form.elements.csrf ? form.elements.csrf.value : "";
    var body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("source", form.elements.source ? form.elements.source.value : "");
    body.set("git_token", form.elements.git_token ? form.elements.git_token.value : "");
    if (form.elements.mode && form.elements.mode.value === "edit" && form.elements.alias) body.set("alias_existing", form.elements.alias.value);
    b.disabled = true;
    show(form, "warn", "working", []);
    fetch(b.getAttribute("data-suggest"), { method: "POST", credentials: "same-origin", body: body })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (x) {
        if (!x.ok) { show(form, "bad", "failed", [x.j.error || "request failed"]); return; }
        apply(form, x.j);
        var lines = (x.j.warnings || []).concat(x.j.notes || []);
        lines.unshift("Review before saving. Replace the placeholder values with real ones.");
        show(form, x.j.confidence === "high" ? "ok" : "warn", x.j.confidence + " confidence", lines);
      })
      .catch(function () { show(form, "bad", "failed", ["request failed"]); })
      .then(function () { b.disabled = false; });
  });
})();
