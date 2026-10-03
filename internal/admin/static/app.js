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

// Toasts: expire after 5 seconds, click to dismiss. window.skgateToast(kind, text) shows one from script.
// They always sit in front of everything: the container is a popover, which lives in the top layer like a
// modal dialog does, and it is raised again (hidden and shown, which moves it to the end of the top layer)
// whenever a toast is added and whenever a dialog opens (window.skgateRaiseToasts). While a modal dialog is
// open the container sits inside it, because everything outside a modal is inert and could not be clicked.
// Browsers without popovers fall back to the shared z-index token --z-toast.
(function () {
  function drop(el) {
    el.classList.add("gone");
    setTimeout(function () {
      var box = el.parentNode;
      if (box) box.removeChild(el);
      if (box && !box.children.length && box.hidePopover) { try { box.hidePopover(); } catch (err) {} }
    }, 250);
  }
  function arm(el) {
    el.addEventListener("click", function () { drop(el); });
    setTimeout(function () { drop(el); }, 5000);
  }
  function raise() {
    var box = document.querySelector(".toasts");
    if (!box || !box.showPopover || !box.children.length) return;
    // A modal dialog makes the rest of the page inert, so a toast outside it could be seen but not clicked.
    // While one is open the container lives inside it (still a popover, so still in front); it moves back
    // to the body when the dialog closes.
    var dlg = document.querySelector("dialog[open]");
    var home = dlg || document.body;
    if (box.parentNode !== home) home.appendChild(box);
    box.setAttribute("popover", "manual");
    try { box.hidePopover(); } catch (err) {}
    try { box.showPopover(); } catch (err) {}
  }
  Array.prototype.forEach.call(document.querySelectorAll(".toast"), arm);
  raise();
  window.skgateRaiseToasts = raise;
  document.addEventListener("close", raise, true); // a dialog closed: the toasts go back to the page
  window.skgateToast = function (kind, text) {
    var box = document.querySelector(".toasts");
    if (!box) {
      box = document.createElement("div");
      box.className = "toasts";
      document.body.appendChild(box);
    }
    var el = document.createElement("div");
    el.className = "toast " + kind;
    el.textContent = text;
    box.appendChild(el);
    arm(el);
    raise();
  };
})();

// Unsaved edits: a dialog is made of sections ([data-section]), each with its own form(s). A section is dirty
// while one of its controls differs from the value the server rendered (the control's default). The heading's chip
// shows it, and window.skgateDirty(root) names the dirty sections under root, which the modal asks about before it
// closes and the page asks about before it unloads.
(function () {
  function dirtyControl(el) {
    if (!el.name || el.name === "csrf" || el.disabled) return false;
    var t = (el.type || "").toLowerCase();
    if (t === "hidden" || t === "submit" || t === "button") return false;
    if (t === "checkbox" || t === "radio") return el.checked !== el.defaultChecked;
    if (el.tagName === "SELECT") { // the default is the option the server marked, or the first when none is
      var def = 0;
      Array.prototype.forEach.call(el.options, function (o, i) { if (o.defaultSelected) def = i; });
      return el.options.length > 0 && el.selectedIndex !== def;
    }
    return el.value !== el.defaultValue;
  }
  function dirty(sec) { return Array.prototype.some.call(sec.querySelectorAll("input,select,textarea"), dirtyControl); }
  function title(sec) {
    var h = sec.querySelector("h4,summary");
    return h ? Array.prototype.filter.call(h.childNodes, function (n) { return n.nodeType === 3; }).map(function (n) { return n.textContent; }).join("").trim() || h.textContent.trim() : "this section";
  }
  function mark(sec) {
    var d = dirty(sec), chip = sec.querySelector("[data-dirty-chip]");
    if (d) sec.setAttribute("data-dirty", ""); else sec.removeAttribute("data-dirty");
    if (chip) chip.hidden = !d;
  }
  ["input", "change"].forEach(function (ev) {
    document.addEventListener(ev, function (e) {
      var sec = e.target && e.target.closest ? e.target.closest("[data-section]") : null;
      if (sec) mark(sec);
    });
  });
  window.skgateDirty = function (root) {
    return Array.prototype.filter.call((root || document).querySelectorAll("[data-section]"), dirty).map(title);
  };
  window.skgateDirtyMark = mark;
  window.skgateDirtyControl = dirtyControl;
  window.addEventListener("beforeunload", function (e) {
    var body = document.querySelector("[data-modal-body]");
    if (body && !body.hidden && window.skgateDirty(body).length) { e.preventDefault(); e.returnValue = ""; }
  });
})();

// Confirmation: a form with data-confirm="text" opens the shared modal (<dialog data-modal>) on submit
// instead of submitting. Title and confirm label are the submit button's label; a button with class
// "danger" makes the confirm button destructive.
// Escape and Cancel close it; a click on the backdrop only closes a dialog marked data-informational (see below).
// A form inside a content dialog (regenerate, revoke) keeps that content in place, hidden, while it asks; the form
// has to stay in the page to submit, and Cancel or Escape go back to the content instead of closing it.
// Content dialogs are addressed by the URL fragment (#id of their <template>): opening one sets it, a load or
// hashchange with that fragment opens it, closing it by any route takes the fragment out of the URL again, and
// the Back button closes it. A dialog opened by a click adds one history entry that closing it by hand removes
// (history.back); a dialog opened by the address itself has none to remove, so its fragment is replaced.
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
  var stack = null; // a confirmation asked from inside a content dialog: { title, informational }; the content stays, hidden
  var opener = null; // the button that opened content
  var informational = false; // the dialog in view only shows information (template attribute data-informational)
  var shownId = null; // id of the content dialog in view (its fragment is in the URL)
  var pushed = false; // opening it added a history entry

  // mode switches the one dialog between a confirmation and content copied from a template.
  function mode(content) {
    stack = null;
    dlg.classList.toggle("wide", content);
    bodyEl.hidden = closeBtn.hidden = !content;
    textEl.hidden = actionsEl.hidden = content;
    if (!content) bodyEl.textContent = "";
  }
  function fragmentId() {
    var h = location.hash.slice(1);
    try { return decodeURIComponent(h); } catch (err) { return h; }
  }
  function urlWith(id) {
    return location.pathname + location.search + (id ? "#" + encodeURIComponent(id) : "");
  }
  // clearHash takes the fragment of the shown content dialog out of the URL. byBack removes the history entry
  // the click added; it is asynchronous, so a close that is followed by a navigation replaces the entry instead.
  function clearHash(byBack) {
    if (shownId === null) return;
    shownId = null;
    var had = pushed;
    pushed = false;
    try {
      if (byBack && had) { history.back(); return; }
      if (location.hash) history.replaceState(history.state, "", urlWith(""));
    } catch (err) { /* a document without a real address has no fragment to clear */ }
  }
  // requestClose closes the content dialog, unless a section holds unsaved edits: then it asks first, in place of the
  // content (which stays, hidden, with every edit in it). Cancel goes back to the edits; Discard closes.
  function requestClose() {
    var names = shownId !== null && !stack && window.skgateDirty ? window.skgateDirty(bodyEl) : [];
    if (!names.length) { close(); return true; }
    var from = document.activeElement; // taken before the content is hidden, which takes the focus away
    stack = { title: titleEl.textContent, informational: informational };
    bodyEl.hidden = closeBtn.hidden = true;
    textEl.hidden = actionsEl.hidden = false;
    dlg.classList.remove("wide");
    informational = false;
    pending = { discard: true, opener: from };
    titleEl.textContent = "Discard changes";
    textEl.textContent = "Unsaved changes in " + names.join(", ") + " will be lost.";
    okBtn.textContent = "Discard";
    okBtn.classList.add("confirm-danger");
    cancelBtn.focus();
    return false;
  }
  function close(replace) {
    if (dlg.close && dlg.open) dlg.close(); else dlg.removeAttribute("open");
    mode(false);
    clearHash(replace !== true);
  }
  function restoreFocus() {
    var p = pending, o = opener;
    pending = null; opener = null;
    var f = (p && p.opener) || o;
    if (f && f.focus) f.focus();
    mode(false);
    clearHash(false); // closed some other way than close(): never leave the fragment behind
  }
  // openContent shows the content dialog whose template has this id. A click records the fragment as a new
  // history entry (or replaces the one of the dialog it swaps out); a fragment already in the URL is left alone.
  function openContent(id, from, record) {
    var tpl = id ? document.getElementById(id) : null;
    if (!tpl || !tpl.hasAttribute("data-dialog-content")) return;
    mode(true);
    informational = tpl.hasAttribute("data-informational");
    bodyEl.textContent = "";
    titleEl.textContent = tpl.getAttribute("data-title") || "";
    bodyEl.appendChild(tpl.content.cloneNode(true));
    opener = from || null;
    if (record && fragmentId() !== id) {
      try {
        if (shownId === null) { history.pushState(null, "", urlWith(id)); pushed = true; }
        else history.replaceState(history.state, "", urlWith(id));
      } catch (err) { /* no real address: the dialog still opens */ }
    }
    shownId = id;
    if (dlg.showModal) { if (!dlg.open) dlg.showModal(); } else dlg.setAttribute("open", "");
    if (window.skgateRaiseToasts) window.skgateRaiseToasts();
  }
  // The address changed (Back, Forward, a typed fragment): show the dialog it names, or close the one in view.
  function follow() {
    var id = fragmentId();
    var tpl = id ? document.getElementById(id) : null;
    if (tpl && tpl.hasAttribute("data-dialog-content")) {
      if (shownId !== id) openContent(id, null, false);
    } else if (shownId !== null) {
      if (!stack && window.skgateDirty && window.skgateDirty(bodyEl).length) { // Back with unsaved edits: keep the dialog and ask
        try { history.pushState(null, "", urlWith(shownId)); pushed = true; } catch (err) { /* no real address */ }
        requestClose();
        return;
      }
      shownId = null; pushed = false; // the address is already clear
      if (dlg.close && dlg.open) dlg.close(); else dlg.removeAttribute("open");
      mode(false);
    }
  }
  document.addEventListener("click", function (e) {
    var o = e.target && e.target.closest ? e.target.closest("[data-dialog-open]") : null;
    if (!o) return;
    if (o.tagName === "A") e.preventDefault(); // a link to the dialog's fragment: the dialog opens, the address is set once
    openContent((o.getAttribute("data-dialog-open") || "").replace(/^#/, ""), o, true);
  });
  closeBtn.addEventListener("click", function () { requestClose(); });
  window.addEventListener("hashchange", follow);
  window.addEventListener("popstate", follow);
  follow();
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
    // A form inside a content dialog (regenerate, revoke) keeps the content in place, hidden, while it asks: the
    // form has to stay in the page for the submit, and Cancel goes back to the content instead of closing it.
    if (shownId !== null && bodyEl.contains(form)) {
      stack = { title: titleEl.textContent, informational: informational };
      bodyEl.hidden = closeBtn.hidden = true;
      textEl.hidden = actionsEl.hidden = false;
      dlg.classList.remove("wide");
    } else {
      mode(false);
    }
    informational = false;
    pending = { form: form, submitter: btn, opener: btn || document.activeElement };
    titleEl.textContent = form.getAttribute("data-confirm-title") || (word ? word.charAt(0).toUpperCase() + word.slice(1) : "Confirm");
    textEl.textContent = text;
    okBtn.textContent = form.getAttribute("data-confirm-ok") || (word ? word.charAt(0).toUpperCase() + word.slice(1) : "Confirm");
    okBtn.classList.toggle("confirm-danger", danger);
    if (dlg.showModal) dlg.showModal(); else dlg.setAttribute("open", "");
    if (window.skgateRaiseToasts) window.skgateRaiseToasts();
    (danger ? cancelBtn : okBtn).focus(); // a destructive action is never the default key press
  });

  // unstack goes back from a confirmation to the content dialog it was asked in. False when there is none.
  function unstack() {
    var s = stack;
    if (!s) return false;
    stack = null;
    var p = pending;
    pending = null;
    bodyEl.hidden = closeBtn.hidden = false;
    textEl.hidden = actionsEl.hidden = true;
    dlg.classList.add("wide");
    titleEl.textContent = s.title;
    informational = s.informational;
    if (p && p.opener && p.opener.isConnected && p.opener.focus) p.opener.focus();
    if (!dlg.contains(document.activeElement) && closeBtn.focus) closeBtn.focus(); // never leave the focus outside: Escape would then close the dialog natively
    return true;
  }
  function submitPending(p) {
    p.form._confirmed = true;
    if (p.form.requestSubmit) {
      try { p.form.requestSubmit(p.submitter && p.submitter.form === p.form ? p.submitter : undefined); } catch (err) { p.form.requestSubmit(); }
    } else {
      p.form.submit();
    }
  }
  okBtn.addEventListener("click", function () {
    var p = pending;
    if (!p) return close();
    pending = null;
    if (p.discard) { stack = null; close(true); return; }
    if (stack) { // the form lives in the content: submit it first, then take the dialog away
      stack = null;
      submitPending(p);
      close(true);
      return;
    }
    close(true); // the form submit navigates away: replace the fragment, do not go back
    submitPending(p);
  });
  cancelBtn.addEventListener("click", function () { if (!unstack()) close(); });
  // A click on the backdrop lands on the dialog element itself; clicks inside land on its children. Only a dialog
  // that merely shows information closes that way; one with a form, a field or a decision ignores it (a drag that
  // starts in a field and ends outside must not throw the input away). Escape and the buttons always close.
  dlg.addEventListener("click", function (e) { if (e.target === dlg && informational) close(); });
  // Escape fires cancel (native) and then close; both end up here.
  // With the focus outside the dialog Escape reaches it as a native cancel, not as a key press: content with unsaved
  // edits still asks. Anything else closes natively and restoreFocus tidies up.
  dlg.addEventListener("cancel", function (e) {
    if (shownId === null || (!stack && !(window.skgateDirty && window.skgateDirty(bodyEl).length))) return;
    e.preventDefault();
    if (!unstack()) requestClose();
  });
  dlg.addEventListener("close", restoreFocus);
  dlg.addEventListener("keydown", function (e) {
    if (e.key === "Escape") { e.preventDefault(); if (unstack()) return; if (requestClose()) restoreFocus(); }
  });
})();

// In-place saves: a form with data-save posts by fetch (Accept: application/json) and the page stays where it is, so
// saving one section of a dialog never reloads the others. The answer is a toast. After a save the page is read again
// and what the server renders replaces the parts that show its state: elements with data-live and an id (the cards
// behind the dialog), the dialog templates (so reopening shows the new state), and the sections of the open dialog.
// A section keeps what the user typed that differs from the server's value (carry), so an edit in one section
// survives a save in another.
(function () {
  function same(root, el) { // the control in root that is el's counterpart: same name, same position among them
    var q = '[name="' + el.name + '"]';
    var from = el.closest("[data-section]");
    var i = Array.prototype.indexOf.call(from.querySelectorAll(q), el);
    return root.querySelectorAll(q)[i] || null;
  }
  function carry(old, fresh) {
    Array.prototype.forEach.call(old.querySelectorAll("input,select,textarea"), function (el) {
      if (!window.skgateDirtyControl(el)) return;
      var to = same(fresh, el);
      if (!to) return;
      if (el.type === "checkbox" || el.type === "radio") to.checked = el.checked;
      else if (el.tagName !== "SELECT" || Array.prototype.some.call(to.options, function (o) { return o.value === el.value; })) to.value = el.value;
    });
    var a = old.matches("details") ? [old] : [], b = fresh.matches("details") ? [fresh] : [];
    a = a.concat(Array.prototype.slice.call(old.querySelectorAll("details")));
    b = b.concat(Array.prototype.slice.call(fresh.querySelectorAll("details")));
    a.forEach(function (d, i) { if (b[i]) b[i].open = d.open; });
  }
  function merge(body, tpl) {
    var active = document.activeElement, act = null;
    if (active && body.contains(active)) {
      var f = active.closest("form");
      act = { action: f && f.getAttribute("action"), name: active.name, tag: active.tagName };
    }
    Array.prototype.forEach.call(body.querySelectorAll("[data-section]"), function (old) {
      var fresh = tpl.content.querySelector('[data-section="' + old.getAttribute("data-section") + '"]');
      if (!fresh) return;
      fresh = document.importNode(fresh, true);
      carry(old, fresh);
      old.replaceWith(fresh);
      window.skgateDirtyMark(fresh);
    });
    if (!act) return;
    var forms = body.querySelectorAll("form"), form = null;
    Array.prototype.forEach.call(forms, function (x) { if (!form && x.getAttribute("action") === act.action) form = x; });
    var el = form && ((act.name && form.querySelector('[name="' + act.name + '"]')) || form.querySelector("button"));
    if (el && el.focus) el.focus();
  }
  function refresh() {
    return fetch(location.pathname + location.search, { credentials: "same-origin", headers: { Accept: "text/html" } })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.text(); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        Array.prototype.forEach.call(doc.querySelectorAll("[data-live][id]"), function (f) {
          var o = document.getElementById(f.id);
          if (o) o.replaceWith(document.importNode(f, true));
        });
        Array.prototype.forEach.call(doc.querySelectorAll("template[data-dialog-content][id]"), function (f) {
          var o = document.getElementById(f.id);
          if (o) o.replaceWith(document.importNode(f, true));
        });
        var body = document.querySelector("[data-modal-body]"), id = decodeURIComponent(location.hash.slice(1)), tpl = id && document.getElementById(id);
        if (body && !body.hidden && tpl && tpl.content) merge(body, tpl);
      });
  }
  // settle makes what was just saved the form's own value, so it no longer counts as unsaved.
  function settle(form) {
    Array.prototype.forEach.call(form.querySelectorAll("input,select,textarea"), function (el) {
      if (el.type === "checkbox" || el.type === "radio") el.defaultChecked = el.checked;
      else if (el.tagName === "SELECT") Array.prototype.forEach.call(el.options, function (o) { o.defaultSelected = o.selected; });
      else el.defaultValue = el.value;
    });
    var sec = form.closest("[data-section]");
    if (sec) window.skgateDirtyMark(sec);
  }
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.hasAttribute || !form.hasAttribute("data-save") || e.defaultPrevented) return;
    e.preventDefault();
    var btns = form.querySelectorAll("button:not([type=button])");
    Array.prototype.forEach.call(btns, function (b) { b.disabled = true; });
    fetch(form.action, { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: new URLSearchParams(new FormData(form)) })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (j) {
        window.skgateToast(j.toast.k, j.toast.m);
        if (j.toast.k !== "ok") return;
        if (form.isConnected) settle(form);
        return refresh().catch(function () { /* the saved state shows on the next load */ });
      })
      .catch(function () { window.skgateToast("bad", "Couldn't reach skgate. Check your connection and try again."); })
      .then(function () { Array.prototype.forEach.call(btns, function (b) { if (b.isConnected) b.disabled = false; }); });
  });
})();

// Expiration field (the expiry_field component): the text is read by the server's parser (one parser, so the
// preview and the saved value always agree). Typing shows what it means, or why it does not, under the field;
// the quick buttons fill the text in. A response that arrives late for older text is ignored.
(function () {
  var timers = new WeakMap(), seq = 0;
  // An unreadable text marks the field invalid (aria-invalid, red border) and blocks the form's submit through the
  // browser's own validation until it is fixed or cleared; the preview line says why.
  function validity(box, ok) {
    var input = box.querySelector("[data-expiry-input]");
    if (ok) input.removeAttribute("aria-invalid"); else input.setAttribute("aria-invalid", "true");
    input.setCustomValidity(ok ? "" : "Fix or clear the expiration");
  }
  // A blocked submit opens the folded section that holds the field, so the browser can show it.
  document.addEventListener("invalid", function (e) {
    var d = e.target && e.target.closest ? e.target.closest("details") : null;
    if (d && !d.open) d.open = true;
  }, true);
  function show(box, ok, text) {
    var p = box.querySelector("[data-expiry-preview]");
    if (!p) return;
    validity(box, ok);
    p.textContent = text;
    p.classList.toggle("bad", !ok);
    p.classList.toggle("muted", ok);
  }
  function check(box) {
    var input = box.querySelector("[data-expiry-input]"), mine = ++seq;
    box._seq = mine;
    fetch(box.getAttribute("data-expiry-url") + "?q=" + encodeURIComponent(input.value), { credentials: "same-origin", headers: { Accept: "application/json" } })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (j) { if (box._seq === mine) show(box, j.ok, j.text); })
      .catch(function () { if (box._seq === mine) show(box, true, "Could not check this now; the server reads it when you save."); });
  }
  // The quick button whose value is what the field says is pressed; typing anything else releases it.
  function mark(box) {
    var v = box.querySelector("[data-expiry-input]").value.trim().toLowerCase();
    box.querySelectorAll("[data-expiry-set]").forEach(function (b) {
      var set = b.getAttribute("data-expiry-set");
      b.setAttribute("aria-pressed", String(set === "never" ? v === "" || v === "never" : v === set));
    });
  }
  document.addEventListener("input", function (e) {
    var input = e.target && e.target.closest ? e.target.closest("[data-expiry-input]") : null;
    var box = input && input.closest("[data-expiry]");
    if (!box) return;
    mark(box);
    clearTimeout(timers.get(box));
    timers.set(box, setTimeout(function () { check(box); }, 150));
  });
  document.addEventListener("click", function (e) {
    var b = e.target && e.target.closest ? e.target.closest("[data-expiry-set]") : null;
    var box = b && b.closest("[data-expiry]");
    if (!box) return;
    var input = box.querySelector("[data-expiry-input]");
    input.value = b.getAttribute("data-expiry-set") === "never" ? "" : b.getAttribute("data-expiry-set");
    mark(box);
    clearTimeout(timers.get(box));
    check(box);
  });
})();

// Copy buttons: data-copy="#id" copies that element's text, data-copy-text="value" copies the value itself.
// Clipboard API, with an execCommand fallback for non-secure contexts. The result is a toast; the optional
// data-copied attribute is its text.
document.addEventListener("click", function (e) {
  var b = e.target && e.target.closest ? e.target.closest("[data-copy],[data-copy-text]") : null;
  if (!b) return;
  var text;
  if (b.hasAttribute("data-copy-text")) {
    text = b.getAttribute("data-copy-text");
  } else {
    var src = document.querySelector(b.getAttribute("data-copy"));
    if (!src) return;
    text = src.textContent.trim();
  }
  function done(ok) {
    window.skgateToast(ok ? "ok" : "bad", ok ? (b.getAttribute("data-copied") || "Copied") : "Copy failed");
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
    var inp = (sel.closest(".field") || sel.parentNode).querySelector("[data-pick-custom]");
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

// Include in /mcp: managed servers can join only when always-on, so the checkbox is disabled (and cleared) for an
// on-demand managed upstream. The server enforces the same rule.
(function () {
  var inc = document.querySelector("[data-include]");
  var sel = document.querySelector("[data-kind-select]");
  if (!inc || !sel || !inc.form) return;
  function sync() {
    var managed = sel.value === "stdio" || sel.value === "git";
    var life = inc.form.elements.lifecycle;
    var off = managed && life && life.value !== "always";
    inc.disabled = !!off;
    if (off) inc.checked = false;
  }
  inc.form.addEventListener("change", sync);
  sync();
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
    var inputs = rows.querySelectorAll("input:not([type=hidden])");
    if (inputs.length >= 2) inputs[inputs.length - 2].focus();
    return;
  }
  var rm = t.closest("[data-pairs-remove]");
  if (rm) {
    var row = rm.closest(".pair");
    if (row && row.parentNode) row.parentNode.removeChild(row);
  }
});

// Secret values: a pairs list with data-secret-pattern masks the value of a row once its name matches
// (KEY, TOKEN, ...), unless the row's flag already says so (stored rows and Suggest results carry "1"/"0").
// The server applies the same pattern to rows submitted without a flag.
document.addEventListener("input", function (e) {
  var box = e.target.closest ? e.target.closest("[data-secret-pattern]") : null;
  var row = box && e.target.closest(".pair");
  if (!row) return;
  var flag = row.querySelector("input[type=hidden]");
  var fields = row.querySelectorAll("input:not([type=hidden])");
  if (!flag || flag.value !== "" || fields.length < 2 || e.target !== fields[0]) return;
  var secret = new RegExp(box.getAttribute("data-secret-pattern"), "i").test(fields[0].value);
  fields[1].type = secret ? "password" : "text";
  fields[1].setAttribute("autocomplete", secret ? "new-password" : "off");
});


// Suggest configuration: [data-suggest="url"] posts the source and token of its form and fills the manual fields
// with the validated answer, for review. Nothing is saved. The fields stay usable whatever the outcome.
// The server streams one JSON object per line (stage events, then a result or an error); the status pill
// shows the current stage with an elapsed timer, and the variable rows appear one by one.
(function () {
  var calm = window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  function rows(form, name) {
    var first = form.querySelector('[name="' + name + '"]');
    return first ? first.closest("[data-pairs]") : null;
  }
  // fill replaces the rows of a dynamic list: items are strings (single) or [name, value] (pairs). A pairs
  // item may carry a third element {secret, required} (a suggested variable): the value starts empty, its
  // field is masked when secret (and kept from browser autofill) and its placeholder says Required or
  // Optional. With animate the rows after the first are added one at a time and fade in.
  function fill(box, items, pairs, animate) {
    if (!box) return;
    var list = box.querySelector("[data-pairs-rows]");
    var tpl = box.querySelector("[data-pairs-template]");
    while (list.children.length > 1) list.removeChild(list.lastChild);
    function put(row, it) {
      var inputs = row.querySelectorAll("input:not([type=hidden])");
      inputs[0].value = it === undefined ? "" : pairs ? it[0] : it;
      if (!pairs) return;
      var val = inputs[1], flag = row.querySelector("input[type=hidden]"), opt = it && it[2];
      val.value = it === undefined ? "" : it[1];
      if (it === undefined) { // nothing suggested: the first row goes back to a blank row
        val.type = "text";
        val.placeholder = "Optional";
        val.setAttribute("autocomplete", "off");
        if (flag) flag.value = "";
      }
      if (!opt) return;
      val.placeholder = opt.required ? "Required" : "Optional";
      val.type = opt.secret ? "password" : "text";
      val.setAttribute("autocomplete", opt.secret ? "new-password" : "off");
      if (flag) flag.value = opt.secret ? "1" : "0";
    }
    put(list.children[0], items[0]);
    items.slice(1).forEach(function (it, i) {
      function add() {
        list.appendChild(tpl.content.cloneNode(true));
        var row = list.lastElementChild;
        put(row, it);
        if (animate && !calm) row.classList.add("reveal");
      }
      if (animate && !calm) setTimeout(add, (i + 1) * 140); else add();
    });
  }
  function set(form, name, v) { var f = form.elements[name]; if (f) f.value = v; }
  // The Suggest button needs a source (and a helper model, which the server marks with data-suggest-off), so it is
  // disabled while the source is empty or a request is running; its tooltip says what is missing.
  var NEED_SOURCE = "Enter a source first";
  function sync(form) {
    var src = form.elements.source;
    Array.prototype.forEach.call(form.querySelectorAll("[data-suggest]"), function (b) {
      var empty = !src || !src.value.trim(), off = b.hasAttribute("data-suggest-off");
      b.disabled = off || empty || !!b._busy;
      if (!off) { if (empty) b.title = NEED_SOURCE; else b.removeAttribute("title"); }
    });
  }
  // Fields that only apply to one kind of source follow the source: the access token and the ref belong to a git
  // repository, the install command is hidden for a package (npm, PyPI) unless it already holds something.
  // sourceKind reads the address the way the server does, loosely: it only decides what is shown.
  function sourceKind(v) {
    v = v.trim();
    if (!v) return "";
    if (/^(npm|pypi|uvx):/i.test(v) || /^@/.test(v) || /==/.test(v)) return "package";
    if (/^git@/i.test(v) || /^https?:\/\/(www\.)?(npmjs\.com|pypi\.org)\//i.test(v)) return /^git@/i.test(v) ? "git" : "package";
    if (/^https?:\/\//i.test(v)) return "git";
    if (/^[^\/@\s]+\.[^\/\s]+\//.test(v)) return "git"; // github.com/owner/repo
    return "package";
  }
  function kinds(form) {
    var src = form.elements.source, k = src ? sourceKind(src.value) : "";
    Array.prototype.forEach.call(form.querySelectorAll("[data-show-for]"), function (el) { el.hidden = el.getAttribute("data-show-for") !== k; });
    Array.prototype.forEach.call(form.querySelectorAll("[data-hide-for]"), function (el) {
      var f = el.querySelector("textarea,input");
      el.hidden = el.getAttribute("data-hide-for") === k && !(f && f.value.trim());
    });
  }
  function refresh(form) { sync(form); kinds(form); }
  window.skgateSuggestSync = function () {
    Array.prototype.forEach.call(document.querySelectorAll("form"), function (f) { if (f.querySelector("[data-suggest-source]")) refresh(f); });
  };
  document.addEventListener("input", function (e) {
    var el = e.target;
    if (!el || !el.closest) return;
    var form = el.closest("form");
    if (form && (el.matches("[data-suggest-source]") || el.matches("[data-hide-for] textarea,[data-hide-for] input"))) refresh(form);
  });
  window.skgateSuggestSync();
  function apply(form, r) {
    if (form.elements.alias && form.elements.alias.type !== "hidden" && !form.elements.alias.value) set(form, "alias", r.alias);
    var pick = form.elements.command_pick;
    if (pick) {
      var known = Array.prototype.some.call(pick.options, function (o) { return o.value === r.command; });
      pick.value = known ? r.command : "";
      pick.dispatchEvent(new Event("change", { bubbles: true }));
      if (!known) set(form, "command", r.command);
    }
    fill(rows(form, "args"), r.args || [], false, false);
    fill(rows(form, "env_name"), (r.env || []).map(function (e) { return [e.name, "", { secret: !!e.secret, required: !!e.required }]; }), true, true);
    set(form, "install", r.install || "");
    set(form, "startup_secs", r.startup_secs || "");
    if (r.kind === "git") { set(form, "source", r.git_url || ""); set(form, "git_ref", r.git_ref || ""); }
    var d = form.querySelector("[data-manual]");
    if (d) d.open = true;
    refresh(form);
  }
  function show(form, cls, label, lines, action) {
    var out = form.querySelector("[data-suggest-out]");
    var pill = out.querySelector("[data-suggest-pill]");
    var act = out.querySelector("[data-suggest-action]");
    if (act) act.remove();
    if (action && document.getElementById("helper-model")) { // the dialog of the MCP helper model, where the reasoning is set
      act = document.createElement("button");
      act.type = "button";
      act.className = "act";
      act.setAttribute("data-suggest-action", "");
      act.setAttribute("data-dialog-open", "#helper-model");
      act.textContent = "Lower reasoning";
      pill.parentNode.appendChild(act);
    }
    var ul = out.querySelector("[data-suggest-list]");
    pill.className = "pill " + cls;
    pill.textContent = label;
    pill.hidden = !label;
    ul.textContent = "";
    lines.forEach(function (l) { var li = document.createElement("li"); li.textContent = l; ul.appendChild(li); });
    out.hidden = false;
  }
  function sourceLine(s) {
    return [s.name, s.language, (s.files || []).join(", ")].filter(Boolean).join(" \u00b7 ");
  }
  function done(form, b, timer, x, secs) {
    clearInterval(timer);
    b._busy = false;
    sync(form);
    if (x.timeout) { // a limit was hit: say where and after how long, and offer the reasoning setting when the model was slow
      var t = x.timeout, slow = t.stage === "model";
      var why = t.kind === "idle" ? "No data for " + t.secs + "s." : "The overall limit of " + t.secs + "s was reached.";
      var lines = [why];
      if (slow) lines.push("Lower the MCP helper model's reasoning (now: " + (t.effort || "auto") + "), or pick a faster model.");
      show(form, "bad", "Timed out while " + t.where + " \u00b7 " + secs + "s", lines, slow);
      return;
    }
    if (x.error) { // said once, here: no pill, no toast
      show(form, "bad", "", [x.error]);
      return;
    }
    apply(form, x.result);
    var lines = (x.result.warnings || []).concat(x.result.notes || []);
    lines.unshift("Review before saving. Fill in the values; empty ones are not passed to the server.");
    show(form, x.result.confidence === "high" ? "ok" : "warn", x.result.confidence + " confidence", lines);
  }
  document.addEventListener("click", function (e) {
    var b = e.target && e.target.closest ? e.target.closest("[data-suggest]") : null;
    if (!b || b.disabled) return;
    var form = b.form || b.closest("form");
    if (!form.elements.source || !form.elements.source.value.trim()) return;
    var csrf = form.elements.csrf ? form.elements.csrf.value : "";
    var body = new URLSearchParams();
    body.set("csrf", csrf);
    body.set("source", form.elements.source ? form.elements.source.value : "");
    body.set("git_token", form.elements.git_token ? form.elements.git_token.value : "");
    if (form.elements.mode && form.elements.mode.value === "edit" && form.elements.alias) body.set("alias_existing", form.elements.alias.value);
    b._busy = true;
    b.disabled = true;
    var t0 = Date.now(), stage = "Starting", info = [];
    var chars = 0;
    function tick() { show(form, "warn", stage + (chars ? " \u00b7 " + (chars >= 1000 ? (chars / 1000).toFixed(1) + "k" : chars) + " chars" : "") + " \u00b7 " + Math.round((Date.now() - t0) / 1000) + "s", info); }
    var timer = setInterval(tick, 1000);
    tick();
    var finished = false;
    function finish(x) { if (!finished) { finished = true; done(form, b, timer, x, Math.round((Date.now() - t0) / 1000)); } }
    function line(l) {
      var m;
      try { m = JSON.parse(l); } catch (err) { return; }
      if (m.stage) {
        if (m.label && m.label !== stage) chars = 0;
        stage = m.label || stage;
        if (m.chars) chars = m.chars;
        if (m.source) info = [sourceLine(m.source)];
        tick();
      } else if (m.result || m.error) {
        finish(m);
      }
    }
    fetch(b.getAttribute("data-suggest"), { method: "POST", credentials: "same-origin", body: body, headers: { Accept: "application/x-ndjson" } })
      .then(function (r) {
        var type = r.headers.get("Content-Type") || "";
        if (type.indexOf("x-ndjson") < 0 || !r.body || !r.body.getReader) { // refused before streaming, or no stream support
          return r.json().then(function (j) { finish(r.ok ? { result: j } : { error: j.error || "The request failed (HTTP " + r.status + ")." }); });
        }
        var rd = r.body.getReader(), dec = new TextDecoder(), buf = "";
        function pump() {
          return rd.read().then(function (c) {
            if (c.done) { buf.split("\n").forEach(function (l) { if (l) line(l); }); return; }
            buf += dec.decode(c.value, { stream: true });
            var parts = buf.split("\n");
            buf = parts.pop();
            parts.forEach(function (l) { if (l) line(l); });
            return pump();
          });
        }
        return pump();
      })
      .catch(function () { finish({ error: "Couldn't reach skgate. Check your connection and try again." }); })
      .then(function () { finish({ error: "the answer ended early" }); });
  });
})();

// Heavy-model hint: the model picker shows its timeout suggestion (data-frontier-hint) only while the chosen model
// is marked data-frontier and the timeout is below the suggested one. The suggestion is never applied.
function frontierHint(form) {
  var hint = form.querySelector("[data-frontier-hint]"), sel = form.querySelector('select[name="model"]'), t = form.querySelector('input[name="timeout"]');
  if (!hint || !sel || !t) return;
  var opt = sel.options[sel.selectedIndex];
  hint.hidden = !(opt && opt.hasAttribute("data-frontier") && Number(t.value) < Number(hint.getAttribute("data-frontier-hint")));
}
["change", "input"].forEach(function (ev) {
  document.addEventListener(ev, function (e) {
    var form = e.target && e.target.closest ? e.target.closest("form") : null;
    if (form && form.querySelector("[data-frontier-hint]")) frontierHint(form);
  });
});

// Inline forms: a form with data-inline posts by fetch and the page updates in place, without a reload. The
// server answers {toast, html}: the toast is shown, and html (the element with data-suggest-controls)
// replaces the current one. data-inline="" closes the open dialog on success; data-inline="#id" keeps it open
// and reloads its body from <template id>.
(function () {
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.hasAttribute || !form.hasAttribute("data-inline") || e.defaultPrevented) return;
    e.preventDefault();
    var again = form.getAttribute("data-inline");
    var btn = form.querySelector("button");
    if (btn) btn.disabled = true;
    fetch(form.action, { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: new URLSearchParams(new FormData(form)) })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (j) {
        var tpl = document.createElement("template");
        tpl.innerHTML = j.html;
        var fresh = tpl.content.firstElementChild;
        var old = document.querySelector("[data-suggest-controls]");
        if (fresh && old) old.replaceWith(fresh);
        if (window.skgateSuggestSync) window.skgateSuggestSync();
        var dlg = document.querySelector("[data-modal]");
        if (j.toast.k === "ok" && !again) {
          var close = dlg && dlg.querySelector("[data-modal-close]");
          if (close) close.click();
        } else if (again) {
          var src = document.querySelector(again), body = dlg && dlg.querySelector("[data-modal-body]");
          if (src && body) { body.textContent = ""; body.appendChild(src.content.cloneNode(true)); }
        }
        window.skgateToast(j.toast.k, j.toast.m);
      })
      .catch(function () { window.skgateToast("bad", "request failed"); })
      .then(function () { if (btn) btn.disabled = false; });
  });
})();
