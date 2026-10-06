// Polls the device sign-in state ([data-poll] holds the URL) and, when it changes, reads the status page again in
// place (window.skgateRefresh), so the card shows the account or the error without a reload. Cancel posts in place
// and takes the panel away, which ends the polling. The panel carries the verification address ([data-device-url],
// with the user code when one is known) and opens it in a window when the page loads with it. A blocked window
// leaves the address on the page.
(function () {
  var el = document.querySelector("[data-poll]");
  if (!el) return;
  var url = el.getAttribute("data-poll");
  var open = el.getAttribute("data-device-url");
  if (open) window.open(open, "skgate-device", "width=520,height=720,noopener,noreferrer");
  var t = setInterval(function () {
    // a refresh draws the panel again while the sign-in is pending; once no panel is in the page, it ended
    if (!document.querySelector("[data-poll]")) { clearInterval(t); return; }
    fetch(url, { credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (s) {
        if (s.state === "pending") return;
        clearInterval(t);
        if (!window.skgateRefresh) { location.href = "/admin"; return; }
        window.skgateRefresh().catch(function () { location.href = "/admin"; });
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
    if (el.hasAttribute && el.hasAttribute("data-autosave")) return false; // saves on change, so it is never an unsaved edit
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
    if (window.skgateLeaveGo) return; // this navigation was a Save or a chosen Leave
    if (window.skgateLeaveActive && window.skgateLeaveActive()) { e.preventDefault(); e.returnValue = ""; }
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
  var once = null; // the template of a result shown once (data-once) in view: it goes when the dialog closes
  var backing = false, queued = null; // a close went back in history; a dialog asked to open meanwhile waits for it

  // mode switches the one dialog between a confirmation and content copied from a template.
  function mode(content) {
    stack = null;
    dlg.classList.toggle("wide", content);
    bodyEl.hidden = closeBtn.hidden = !content;
    textEl.hidden = actionsEl.hidden = content;
    if (!content) bodyEl.textContent = "";
  }
  function safeDecode(h) { try { return decodeURIComponent(h); } catch (err) { return h; } }
  function fragmentId() { return safeDecode(location.hash.slice(1)); }
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
      if (byBack && had) {
        backing = true;
        history.back();
        setTimeout(function () { if (backing) { backing = false; openQueued(); } }, 600); // no popstate came
        return;
      }
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
    // the close event comes later than the close; a dialog opened again meanwhile (a result shown once, right after
    // the confirmation that asked for it) is not tidied away
    if (dlg.open) return;
    if (once) { once.remove(); once = null; } // a result shown once is gone with its dialog
    var p = pending, o = opener;
    pending = null; opener = null;
    var f = (p && p.opener) || o;
    if (f && f.focus) f.focus({ preventScroll: true }); // the page stays where it was under the dialog
    var mn = f && f.closest ? f.closest("[data-menu]") : null; // the opener was an item of a menu that has closed: back to its button
    if (mn && document.activeElement !== f) mn.querySelector("[data-menu-button]").focus({ preventScroll: true });
    mode(false);
    clearHash(false); // closed some other way than close(): never leave the fragment behind
  }
  // openContent shows the content dialog whose template has this id. A click records the fragment as a new
  // history entry (or replaces the one of the dialog it swaps out); a fragment already in the URL is left alone.
  function openContent(id, from, record) {
    var tpl = id ? document.getElementById(id) : null;
    if (!tpl || !tpl.hasAttribute("data-dialog-content")) return;
    mode(true);
    if (once && once !== tpl) once.remove();
    once = tpl.hasAttribute("data-once") ? tpl : null;
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
    bodyEl.removeAttribute("data-show-alias");
    var alias = from && from.getAttribute ? from.getAttribute("data-alias") : "";
    if (alias) bodyEl.setAttribute("data-show-alias", alias);
    showAlias(bodyEl, true);
    // A dialog that picks a helper model reloads that provider's list when it opens. Deferred so the save
    // listener is already registered when a fragment opens the dialog during the first load. file: snapshots
    // have no server, so they skip the reload.
    setTimeout(function () {
      if (location.protocol === "file:") return;
      var f = bodyEl.querySelector('form[action$="/models/reload"]');
      if (f) submitForm(f);
    }, 0);
  }
  // showAlias marks the alias row named on the dialog body, and focuses it when the dialog has just opened.
  function showAlias(body, focus) {
    if (!body) return;
    var name = body.getAttribute("data-show-alias") || "";
    Array.prototype.forEach.call(body.querySelectorAll("tr[data-current]"), function (tr) { tr.removeAttribute("data-current"); });
    if (!name) return;
    var tr = null;
    Array.prototype.forEach.call(body.querySelectorAll("tr[data-alias]"), function (row) {
      if (!tr && row.getAttribute("data-alias") === name) tr = row;
    });
    if (!tr) return;
    tr.setAttribute("data-current", "");
    if (!focus) return;
    var el = tr.querySelector("select, button");
    if (el && el.focus) { try { el.focus(); } catch (err) { /* jsdom has no focus layout */ } }
    try { tr.scrollIntoView({ block: "nearest" }); } catch (err) { /* no layout */ }
  }
  window.skgateShowAlias = showAlias;
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
  closeBtn.addEventListener("click", function () {
    if (pending && pending.leave) { pending = null; if (!unstack()) close(); return; }
    requestClose();
  });
  window.addEventListener("hashchange", follow);
  window.addEventListener("popstate", function () {
    follow();
    if (backing) { backing = false; openQueued(); }
  });
  follow();
  function openQueued() { var q = queued; queued = null; if (q) openContent(q.id, q.from, true); }
  // skgateOpen opens a content dialog from script (a result shown once), after a close that is still going back.
  window.skgateOpen = function (id, from) {
    if (backing) { queued = { id: id, from: from }; return; }
    openContent(id, from, true);
  };
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
    cancelBtn.textContent = "Cancel";
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
    if (p.leave) { if (!unstack()) close(); return; } // Stay
    if (p.discard) { stack = null; close(true); return; }
    if (stack) { // the form lives in the content: submit it first, then take the dialog away
      stack = null;
      submitPending(p);
      close(!p.form.hasAttribute("data-post")); // a post in place stays on the page: closing goes back, as by hand
      return;
    }
    close(true); // the form submit navigates away: replace the fragment, do not go back
    submitPending(p);
  });
  cancelBtn.addEventListener("click", function () {
    var p = pending;
    if (p && p.leave) {
      pending = null;
      if (window.skgateAbortSI) window.skgateAbortSI();
      var go = p.leave;
      close(true);
      window.skgateLeaveGo = true;
      go();
      return;
    }
    if (!unstack()) close();
  });
  // A click on the backdrop lands on the dialog element itself; clicks inside land on its children. Only a dialog
  // that merely shows information closes that way; one with a form, a field or a decision ignores it (a drag that
  // starts in a field and ends outside must not throw the input away). Escape and the buttons always close.
  dlg.addEventListener("click", function (e) { if (e.target === dlg && informational) close(); });
  // Escape fires cancel (native) and then close; both end up here.
  // With the focus outside the dialog Escape reaches it as a native cancel, not as a key press: content with unsaved
  // edits still asks. Anything else closes natively and restoreFocus tidies up.
  dlg.addEventListener("cancel", function (e) {
    if (pending && pending.leave) { e.preventDefault(); pending = null; if (!unstack()) close(); return; }
    if (shownId === null || (!stack && !(window.skgateDirty && window.skgateDirty(bodyEl).length))) return;
    e.preventDefault();
    if (!unstack()) requestClose();
  });
  dlg.addEventListener("close", restoreFocus);
  dlg.addEventListener("keydown", function (e) {
    if (e.key !== "Escape") return;
    e.preventDefault();
    if (pending && pending.leave) { pending = null; if (!unstack()) close(); return; }
    if (unstack()) return;
    if (requestClose()) restoreFocus();
  });
  // skgateLeaveAsk is the shared leave prompt. Stay is bottom-left, Leave beside it, Close is Stay.
  window.skgateLeaveAsk = function (why, go) {
    var title = why === "si" ? "SI is still working." : "Unsaved changes";
    var text = why === "si" ? "Leave and lose this result?" : "Leave and lose them?";
    if (shownId !== null && !stack && bodyEl && !bodyEl.hidden) {
      stack = { title: titleEl.textContent, informational: informational };
      bodyEl.hidden = true;
      dlg.classList.remove("wide");
    } else if (shownId === null) {
      mode(false);
    }
    informational = false;
    pending = { leave: go };
    titleEl.textContent = title;
    textEl.textContent = text;
    textEl.hidden = false;
    actionsEl.hidden = false;
    closeBtn.hidden = false;
    okBtn.textContent = "Stay";
    okBtn.classList.remove("confirm-danger");
    cancelBtn.textContent = "Leave";
    if (dlg.showModal) { if (!dlg.open) dlg.showModal(); } else dlg.setAttribute("open", "");
    if (window.skgateRaiseToasts) window.skgateRaiseToasts();
    okBtn.focus();
  };
})();

// Leave guard: while a helper call is running, or a form with an explicit Save has unsaved edits, in-app
// navigation asks first. Forms that save on change never ask. Tab close uses beforeunload only then.
(function () {
  function explicitForm(form) {
    if (!form || !form.querySelector) return false;
    var btn = form.querySelector("button.btn:not([type=button])");
    if (!btn) return false;
    return Array.prototype.some.call(form.querySelectorAll("input,select,textarea"), function (el) {
      if (!el.name || el.name === "csrf") return false;
      var t = (el.type || "").toLowerCase();
      if (t === "hidden" || t === "submit" || t === "button") return false;
      return !(el.hasAttribute && el.hasAttribute("data-autosave"));
    });
  }
  function formDirty(form) {
    if (!window.skgateDirtyControl) return false;
    return Array.prototype.some.call(form.querySelectorAll("input,select,textarea"), window.skgateDirtyControl);
  }
  window.skgateExplicitDirty = function (form) { return explicitForm(form) && formDirty(form); };
  window.skgateExplicitForm = explicitForm;
  window.skgateLeaveActive = function () {
    if (window.skgateSI) return true;
    return Array.prototype.some.call(document.querySelectorAll("form"), function (f) { return window.skgateExplicitDirty(f); });
  };
  function why() { return window.skgateSI ? "si" : "form"; }
  window.skgateGo = function (url) { window.skgateLeaveGo = true; location.href = url; };
  document.addEventListener("click", function (e) {
    if (!window.skgateLeaveAsk || !window.skgateLeaveActive()) return;
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    var a = e.target && e.target.closest ? e.target.closest("a[href]") : null;
    if (!a || a.target === "_blank" || a.hasAttribute("download")) return;
    var href = a.getAttribute("href") || "";
    if (!href || href.charAt(0) === "#") return;
    e.preventDefault();
    e.stopPropagation();
    window.skgateLeaveAsk(why(), function () { window.skgateGo(a.href); });
  }, true);
  document.addEventListener("submit", function (e) {
    if (!window.skgateLeaveAsk || !window.skgateLeaveActive()) return;
    var form = e.target;
    if (!form || !form.hasAttribute) return;
    if (form.hasAttribute("data-save") || form.hasAttribute("data-post") || form.hasAttribute("data-update") || form.hasAttribute("data-inline") || form.hasAttribute("data-confirm")) return;
    if (!window.skgateSI && window.skgateExplicitDirty(form)) return;
    if (e.defaultPrevented) return;
    e.preventDefault();
    e.stopPropagation();
    window.skgateLeaveAsk(why(), function () { form.submit(); });
  }, true);
})();

// A field with data-autosave posts its form on change. The saved value is the server's until the page is read again,
// so a newer edit can be copied onto the fresh markup (autosaveChanged). A failed save puts the field back.
function autosaveChanged(el) {
  if (!el || !el.hasAttribute || !el.hasAttribute("data-autosave") || el.disabled) return false;
  if (el.tagName === "SELECT") {
    var def = 0;
    Array.prototype.forEach.call(el.options, function (o, i) { if (o.defaultSelected) def = i; });
    return el.options.length > 0 && el.selectedIndex !== def;
  }
  return el.value !== el.defaultValue;
}
function revertAutosave(form) {
  Array.prototype.forEach.call(form.querySelectorAll("[data-autosave]"), function (el) {
    if (el.tagName === "SELECT") {
      var def = 0;
      Array.prototype.forEach.call(el.options, function (o, i) { if (o.defaultSelected) def = i; });
      if (el.options.length) el.selectedIndex = def;
    } else el.value = el.defaultValue;
  });
  if (form.querySelector("[data-frontier-hint]")) frontierHint(form);
}
// formURL is where a form posts. form.action is not used: a control named "action" (the process forms carry one)
// shadows that property, and the fetch would go to "[object HTMLInputElement]".
function formURL(form) {
  return form.getAttribute("action") || location.pathname + location.search;
}
function submitForm(form) {
  if (form.requestSubmit) form.requestSubmit();
  else form.dispatchEvent(new Event("submit", { cancelable: true, bubbles: true }));
}
function beginSave(form) {
  if (form._saving) { form._again = true; return false; }
  form._saving = true;
  form._again = false;
  form._inBody = !!(form.closest && form.closest("[data-modal-body]"));
  form.setAttribute("data-saving", "");
  return true;
}
// formTwin is the form after a refresh replaced it: same action, same alias name, same place.
function formTwin(form) {
  if (form.isConnected) return form;
  var action = form.getAttribute("action");
  var nameIn = form.querySelector('input[name="name"]');
  var wantName = nameIn ? nameIn.value : null;
  var wantAuto = !!form.querySelector("[data-autosave]");
  var hit = null;
  Array.prototype.forEach.call(document.querySelectorAll("form"), function (f) {
    if (hit || f.getAttribute("action") !== action) return;
    if (!!f.closest("[data-modal-body]") !== !!form._inBody) return;
    if (!!f.querySelector("[data-autosave]") !== wantAuto) return;
    if (wantName !== null) {
      var n = f.querySelector('input[name="name"]');
      if (!n || n.value !== wantName) return;
    }
    hit = f;
  });
  return hit;
}
function endSave(form, ok) {
  form._saving = false;
  form.removeAttribute("data-saving");
  var again = form._again;
  form._again = false;
  var live = formTwin(form);
  if (!live) return;
  if (again) { submitForm(live); return; }
  if (!ok) revertAutosave(live);
}
// Another form in the open dialog is mid-edit, so recloning the body would throw that edit away.
function bodyBusy(dlg, self) {
  var body = dlg && dlg.querySelector("[data-modal-body]");
  if (!body) return false;
  var busy = false;
  Array.prototype.forEach.call(body.querySelectorAll("form"), function (f) {
    if (busy || f === self) return;
    if (f._saving || f._again) { busy = true; return; }
    Array.prototype.forEach.call(f.querySelectorAll("[data-autosave]"), function (el) { if (autosaveChanged(el)) busy = true; });
  });
  return busy;
}

// In-place saves: a form with data-save posts by fetch (Accept: application/json) and the page stays where it is, so
// saving one section of a dialog never reloads the others. The answer is a toast. After a save the page is read again
// and what the server renders replaces the parts that show its state: elements with data-live and an id (the cards
// behind the dialog, the alias table), the dialog templates (so reopening shows the new state), and the sections of
// the open dialog. A section keeps what the user typed that differs from the server's value (carry), including a
// newer autosave edit, so an edit in one section survives a save in another. Scroll stays where it was.
(function () {
  function same(root, el) { // the control in root that is el's counterpart: same name, same position among them
    var q = '[name="' + el.name + '"]';
    var from = el.closest("[data-section]");
    var i = Array.prototype.indexOf.call(from.querySelectorAll(q), el);
    return root.querySelectorAll(q)[i] || null;
  }
  function carry(old, fresh) {
    Array.prototype.forEach.call(old.querySelectorAll("input,select,textarea"), function (el) {
      if (!window.skgateDirtyControl(el) && !autosaveChanged(el)) return;
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
  // keep carries what the person sees in a part over to its fresh copy: which <details> are open, and edits that are
  // not saved yet (a field is found by its row's data-key, else by its name and place in the part).
  function ident(el) { return el.getAttribute("data-key") || el.getAttribute("data-verbgroup") || el.id || ""; }
  function twin(list, el, i) {
    var id = ident(el);
    if (!id) return list[i] || null;
    for (var k = 0; k < list.length; k++) if (ident(list[k]) === id) return list[k];
    return null;
  }
  function keep(old, fresh) {
    var od = old.querySelectorAll("details"), fd = fresh.querySelectorAll("details");
    Array.prototype.forEach.call(od, function (d, i) { var n = twin(fd, d, i); if (n) n.open = d.open; });
    if (!window.skgateDirtyControl) return;
    Array.prototype.forEach.call(old.querySelectorAll("input,select,textarea"), function (el) {
      if (!el.name || el.type === "hidden" || !window.skgateDirtyControl(el)) return;
      var row = el.closest("[data-key]"), from = old, to = fresh;
      if (row && old.contains(row)) {
        to = null;
        Array.prototype.forEach.call(fresh.querySelectorAll("[data-key]"), function (r) { if (!to && r.getAttribute("data-key") === row.getAttribute("data-key")) to = r; });
        if (!to) return;
        from = row;
      }
      var q = '[name="' + el.name + '"]';
      var t = to.querySelectorAll(q)[Array.prototype.indexOf.call(from.querySelectorAll(q), el)];
      if (!t || t.type !== el.type || t.disabled) return;
      if (el.type === "checkbox" || el.type === "radio") t.checked = el.checked;
      else if (el.tagName !== "SELECT" || Array.prototype.some.call(t.options, function (o) { return o.value === el.value; })) t.value = el.value;
    });
  }
  function refresh() {
    var y = window.scrollY || window.pageYOffset || 0;
    var dlg = document.querySelector("[data-modal]");
    var top = dlg ? dlg.scrollTop : 0;
    return fetch(location.pathname + location.search, { credentials: "same-origin", cache: "no-store", headers: { Accept: "text/html" } })
      .then(function (r) {
        if (!r.ok) throw new Error("status " + r.status);
        // a redirect (a lost session) is another page: nothing on it may replace or remove this one's parts
        if (r.url && new URL(r.url, location.href).pathname !== location.pathname) throw new Error("moved");
        return r.text();
      })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        Array.prototype.forEach.call(doc.querySelectorAll("[data-live][id]"), function (f) {
          var o = document.getElementById(f.id);
          if (!o) return;
          var n = document.importNode(f, true);
          keep(o, n);
          o.replaceWith(n);
        });
        Array.prototype.forEach.call(doc.querySelectorAll("template[data-dialog-content][id]"), function (f) {
          var o = document.getElementById(f.id), n = document.importNode(f, true);
          if (o) { o.replaceWith(n); return; }
          // a new one (an added provider's dialogs) goes after the one before it, or at the end of the page
          var prev = f.previousElementSibling;
          while (prev && !(prev.id && document.getElementById(prev.id))) prev = prev.previousElementSibling;
          if (prev) document.getElementById(prev.id).after(n);
          else (document.querySelector("main") || document.body).appendChild(n);
        });
        // what the server no longer renders (a deleted row's dialog, a removed provider's card) goes too
        Array.prototype.forEach.call(document.querySelectorAll("[data-live][id], template[data-dialog-content][id]"), function (o) {
          if (!doc.getElementById(o.id) && !o.hasAttribute("data-once")) o.remove(); // a result shown once is not on the page
        });
        document.dispatchEvent(new Event("skgate:refreshed")); // scripts of a part draw it again (the tool counter, the filter)
        var body = document.querySelector("[data-modal-body]"), id = (function (h) { try { return decodeURIComponent(h); } catch (err) { return h; } })(location.hash.slice(1)), tpl = id && document.getElementById(id);
        if (body && !body.hidden && tpl && tpl.content) {
          merge(body, tpl);
          if (window.skgateShowAlias) window.skgateShowAlias(body, false);
        }
        window.scrollTo(0, y);
        var dlg2 = document.querySelector("[data-modal]");
        if (dlg2) dlg2.scrollTop = top;
      });
  }
  // settle makes what was just saved the form's own value, so it no longer counts as unsaved.
  // Autosave fields keep the server's default until the fresh markup arrives, so a newer edit is still carried.
  function settle(form) {
    Array.prototype.forEach.call(form.querySelectorAll("input,select,textarea"), function (el) {
      if (el.hasAttribute("data-autosave")) return;
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
    if (!beginSave(form)) return;
    var btns = form.querySelectorAll("button:not([type=button])");
    Array.prototype.forEach.call(btns, function (b) { b.disabled = true; });
    fetch(formURL(form), { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: new URLSearchParams(new FormData(form)) })
      .then(function (r) {
        if (!r.ok) throw { say: "skgate answered with an error (status " + r.status + "). Try again, or reload the page." };
        return r.json().catch(function () { throw { say: "skgate answered with something unexpected. Reload the page and check what was saved." }; });
      })
      .then(function (j) {
        var good = j.toast.k === "ok";
        if (!(form.hasAttribute("data-quiet") && good)) window.skgateToast(j.toast.k, j.toast.m);
        if (!good) return false;
        if (form.isConnected) settle(form);
        return refresh().catch(function () { /* the saved state shows on the next load */ }).then(function () { return true; });
      })
      .catch(function (err) { window.skgateToast("bad", err && err.say ? err.say : "Couldn't reach skgate. Check your connection and try again."); return false; })
      .then(function (good) {
        Array.prototype.forEach.call(btns, function (b) { if (b.isConnected) b.disabled = false; });
        endSave(form, good === true);
      });
  });

  // In-place actions: a form with data-post (a toggle, a delete, revoke, remove, a process action) posts by fetch
  // and the page stays where it is: no reload, no scroll jump. A good answer shows no toast: the parts of the page
  // that show the state (data-live, the dialog templates, the open dialog) are read again, and that change is the
  // answer; a row that is gone goes. Only an error is a toast. A switch flips at once and flips back on an error,
  // with the reason as a toast and as its tooltip, like the update pill. A control keeps its size while it posts.
  // data-post="close" also closes the open dialog after a good answer (a Save or an Add inside a dialog).
  // A confirmation (data-confirm) asks first; the post follows once it is confirmed.
  function flip(btn) {
    if (!btn || !btn.classList.contains("toggle") || btn.getAttribute("role") !== "switch") return null;
    var was = btn.getAttribute("aria-checked") === "true", text = btn.textContent;
    function set(on, label) {
      btn.setAttribute("aria-checked", on ? "true" : "false");
      btn.classList.toggle("on", on);
      btn.classList.toggle("off", !on);
      if (label) btn.textContent = label;
    }
    set(!was, btn.getAttribute(was ? "data-off" : "data-on"));
    return function () { set(was, text); };
  }
  // sig names a form by its action and its hidden fields (not the CSRF token), so the same control can be found
  // again after the page parts were replaced.
  function sig(form) {
    var parts = [form.getAttribute("action") || ""];
    Array.prototype.forEach.call(form.querySelectorAll("input[type=hidden]"), function (i) { if (i.name !== "csrf") parts.push(i.name + "=" + i.value); });
    return parts.join("&");
  }
  function refocus(key) {
    if (!key || (document.activeElement && document.activeElement !== document.body)) return;
    var hit = null;
    Array.prototype.forEach.call(document.querySelectorAll("form[data-post]"), function (f) { if (!hit && sig(f) === key) hit = f; });
    var b = hit && hit.querySelector("button:not([type=button])");
    if (b && b.focus) b.focus({ preventScroll: true });
  }
  // reopen shows the dialog the action was taken in (Detect) again from its fresh template, so it shows the new
  // state; the dialog of something that is gone closes. A dialog with sections is merged by refresh instead.
  function hashId() { var h = location.hash.slice(1); try { return decodeURIComponent(h); } catch (err) { return h; } }
  function reopen(id, x) {
    var dlg = document.querySelector("[data-modal][open]"), body = dlg && dlg.querySelector("[data-modal-body]");
    if (!id || !body || body.hidden || hashId() !== id) return;
    var tpl = document.getElementById(id);
    if (!tpl || !tpl.content) { if (x) x.click(); return; }
    if (body.querySelector("[data-section]")) return;
    var top = dlg.scrollTop;
    body.textContent = "";
    body.appendChild(tpl.content.cloneNode(true));
    dlg.scrollTop = top;
  }
  // showOnce opens a result that is shown once (a new key or client secret, an import's outcome, an update to
  // review): the answer carries its dialog template, which is put in the page only while the dialog is open (the
  // modal drops data-once templates when it closes) and is never on the page the server renders. Closing it leaves
  // the page as it was. Focus goes back to the form's button when it is still on the page.
  function showOnce(html, form) {
    var tpl = new DOMParser().parseFromString(html, "text/html").querySelector("template[data-dialog-content][id]");
    if (!tpl || !window.skgateOpen) return;
    var n = document.importNode(tpl, true), o = document.getElementById(tpl.id);
    n.setAttribute("data-once", "");
    if (o) o.remove();
    (document.querySelector("main") || document.body).appendChild(n);
    var act = form.getAttribute("action"), f = form.isConnected ? form : null;
    if (!f) Array.prototype.forEach.call(document.querySelectorAll("form"), function (x) { if (!f && x.getAttribute("action") === act) f = x; });
    var b = f && !f.closest("[data-modal]") ? f.querySelector("button:not([type=button])") : null;
    window.skgateOpen(n.id, b);
  }
  window.skgateRefresh = refresh;
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.hasAttribute || !form.hasAttribute("data-post") || e.defaultPrevented) return;
    e.preventDefault();
    if (form._posting) return;
    form._posting = true;
    var btn = e.submitter || form.querySelector("button:not([type=button])");
    var undo = flip(btn);
    var key = btn && document.activeElement === btn ? sig(form) : "";
    var dlgId = form.closest("[data-modal-body]") ? hashId() : "";
    if (btn && !undo) btn.disabled = true;
    form.setAttribute("data-saving", "");
    function done() {
      form._posting = false;
      form.removeAttribute("data-saving");
      if (btn && btn.isConnected && !undo) btn.disabled = false;
    }
    function fail(msg) {
      if (undo) undo();
      if (btn && btn.isConnected) btn.title = msg;
      window.skgateToast("bad", msg);
    }
    fetch(formURL(form), { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: new URLSearchParams(new FormData(form)) })
      .then(function (r) {
        if (!r.ok) throw { say: "skgate answered with an error (status " + r.status + "). Try again, or reload the page." };
        return r.json().catch(function () { throw { say: "skgate answered with something unexpected. Reload the page and check what was saved." }; });
      })
      .then(function (j) {
        var t = (j && j.toast) || {}, good = t.k === "ok", show = j && j.show;
        if (!good) {
          if (undo) { fail(t.m || "That did not work."); return; }
          window.skgateToast("bad", t.m || "That did not work.");
          // a form with typed fields (Create key, Save on the tools page) keeps every one: nothing is read again
          if (!show && window.skgateExplicitForm && window.skgateExplicitForm(form)) return;
        } else if (j.tell && t.m) {
          window.skgateToast("ok", t.m); // a notice, not a change: nothing on the page shows it ("No changes")
        }
        var x = document.querySelector("[data-modal] [data-modal-close]");
        if (good) settle(form); // what was sent is saved: no longer an unsaved edit, and not carried over the fresh part
        if (good && form.getAttribute("data-post") === "close") {
          if (x && document.querySelector("[data-modal][open]")) x.click();
          dlgId = "";
        }
        return refresh().then(function () { reopen(dlgId, x); refocus(key); if (show) showOnce(show, form); }, function () {
          window.skgateToast(good ? "warn" : "bad", "Reload the page to see the change.");
          if (show) showOnce(show, form);
        });
      })
      .catch(function (err) { fail(err && err.say ? err.say : "Couldn't reach skgate. Check your connection and try again."); })
      .then(done);
  });
})();

// Menus (the menu_start component, the More button of a row): the button opens its list below it, fixed to the window
// so a scrolling table cannot clip it, and the first item takes the focus. Arrow keys, Home and End move between
// items; Escape closes and returns to the button; so do a click outside, Tab, a scroll or resize, and choosing an
// item (the item then does its own thing: a link, a dialog, a form). One menu is open at a time. Taps work as clicks.
(function () {
  var current = null; // the open menu's element
  function parts(m) { return { btn: m.querySelector("[data-menu-button]"), list: m.querySelector("[role=menu]") }; }
  function items(m) { return Array.prototype.slice.call(parts(m).list.querySelectorAll("[role=menuitem]")).filter(function (i) { return !i.disabled; }); }
  function place(m) {
    var p = parts(m), r = p.btn.getBoundingClientRect(), l = p.list;
    var w = l.offsetWidth, h = l.offsetHeight;
    var left = Math.max(8, Math.min(r.right - w, window.innerWidth - w - 8));
    var top = r.bottom + 4;
    if (top + h > window.innerHeight - 8 && r.top - h - 4 >= 8) top = r.top - h - 4; // no room below: open upward
    l.style.left = left + "px";
    l.style.top = top + "px";
  }
  function close(back) {
    var m = current;
    if (!m) return;
    current = null;
    var p = parts(m);
    p.list.hidden = true;
    p.btn.setAttribute("aria-expanded", "false");
    if (back) p.btn.focus();
  }
  function open(m, last) {
    if (current && current !== m) close(false);
    var p = parts(m);
    p.list.hidden = false;
    p.btn.setAttribute("aria-expanded", "true");
    place(m);
    current = m;
    var it = items(m);
    if (it.length) it[last ? it.length - 1 : 0].focus({ preventScroll: true });
  }
  document.addEventListener("click", function (e) {
    var b = e.target && e.target.closest ? e.target.closest("[data-menu-button]") : null;
    if (b) {
      var m = b.closest("[data-menu]");
      if (current === m) close(true); else open(m, false);
      return;
    }
    if (!current) return;
    close(false); // a click outside, or an item chosen: the item's own action follows
  });
  document.addEventListener("keydown", function (e) {
    var m = current, t = e.target && e.target.closest ? e.target.closest("[data-menu]") : null;
    if (!m && t && e.target.matches("[data-menu-button]") && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
      e.preventDefault();
      open(t, e.key === "ArrowUp");
      return;
    }
    if (!m || t !== m) return;
    var it = items(m), i = it.indexOf(document.activeElement);
    switch (e.key) {
      case "Escape": e.preventDefault(); e.stopPropagation(); close(true); break;
      case "Tab": close(false); break;
      case "ArrowDown": e.preventDefault(); if (it.length) it[(i + 1) % it.length].focus(); break;
      case "ArrowUp": e.preventDefault(); if (it.length) it[(i <= 0 ? it.length : i) - 1].focus(); break;
      case "Home": e.preventDefault(); if (it.length) it[0].focus(); break;
      case "End": e.preventDefault(); if (it.length) it[it.length - 1].focus(); break;
    }
  }, true);
  window.addEventListener("resize", function () { close(false); });
  window.addEventListener("scroll", function (e) { if (current && !current.contains(e.target)) close(false); }, true);
})();

// Top bar (the header in layout.html): on phones the links, version, user and logout are a panel that the Menu button
// folds and unfolds; Escape, a click outside, choosing a link, leaving the panel with Tab, or coming back to the
// page from history fold it. Wider windows show everything and ignore the state. --header-h follows the bar's height,
// so anchors and focus scrolling land below the sticky bar.
(function () {
  var bar = document.querySelector("header[data-nav]");
  if (!bar) return;
  var btn = bar.querySelector("[data-nav-button]"), panel = bar.querySelector(".nav-panel");
  function set(open, back) {
    if (open) bar.removeAttribute("data-collapsed"); else bar.setAttribute("data-collapsed", "");
    btn.setAttribute("aria-expanded", open ? "true" : "false");
    if (!open && back) btn.focus();
  }
  function isOpen() { return btn.getAttribute("aria-expanded") === "true"; }
  set(false);
  btn.addEventListener("click", function () { set(!isOpen(), false); });
  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && isOpen() && !document.querySelector("dialog[open]")) { e.preventDefault(); set(false, true); }
  });
  document.addEventListener("click", function (e) {
    if (!isOpen()) return;
    if (!bar.contains(e.target) || (panel.contains(e.target) && e.target.closest("a"))) set(false, false);
  });
  bar.addEventListener("focusout", function (e) { if (isOpen() && e.relatedTarget && !bar.contains(e.relatedTarget)) set(false, false); });
  window.addEventListener("pageshow", function () { set(false, false); });
  window.addEventListener("hashchange", function () { set(false, false); });
  function size() { document.documentElement.style.setProperty("--header-h", Math.ceil(bar.getBoundingClientRect().height) + "px"); }
  size();
  if (window.ResizeObserver) new ResizeObserver(size).observe(bar); else window.addEventListener("resize", size);
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
    if (!ok) {
      // nothing could be copied: select the text, so the person can copy it by hand
      var code = b.querySelector ? b.querySelector("code") : null;
      if (code && window.getSelection && document.createRange) {
        var range = document.createRange();
        range.selectNodeContents(code);
        var sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        window.skgateToast("bad", "Couldn't copy. The text is selected, copy it by hand.");
        return;
      }
    }
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

// Prefix: lowercase letters and digits only, at most 16. Uppercase is lowercased as it is typed.
// Any other character is dropped, and the hint under the field turns red for a second. A paste is
// cleaned in one pass and flashes once when anything was dropped. No toast.
document.addEventListener("input", function (e) {
  var el = e.target;
  if (!el || !el.hasAttribute || !el.hasAttribute("data-prefix")) return;
  var raw = el.value, next = "", dropped = false, keep = 0, pos = el.selectionStart || 0;
  for (var i = 0; i < raw.length; i++) {
    var c = raw.charAt(i), okc = false;
    if (c >= "A" && c <= "Z") { c = c.toLowerCase(); okc = true; }
    else if ((c >= "a" && c <= "z") || (c >= "0" && c <= "9")) okc = true;
    else dropped = true;
    if (!okc) continue;
    if (next.length >= 16) { dropped = true; continue; }
    next += c;
    if (i < pos) keep++;
  }
  if (next === raw) return;
  el.value = next;
  if (el.setSelectionRange) el.setSelectionRange(keep, keep);
  if (!dropped) return;
  var hint = el.form && el.form.querySelector("[data-prefix-hint]");
  if (!hint) return;
  hint.classList.add("bad");
  clearTimeout(el._prefixFlash);
  el._prefixFlash = setTimeout(function () { hint.classList.remove("bad"); }, 1000);
});

// Presets: choosing an option of a [data-preset] select copies its data-set-<field> attributes into the
// form fields of that name (a field may be an <output>, for a hint).
document.addEventListener("change", function (e) {
  var sel = e.target && e.target.closest ? e.target.closest("[data-preset]") : null;
  if (!sel || !sel.form) return;
  var opt = sel.options[sel.selectedIndex];
  Array.prototype.forEach.call(opt.attributes, function (a) {
    if (a.name.indexOf("data-set-") !== 0) return;
    var f = sel.form.elements[a.name.slice(9)];
    if (f) f.value = a.value;
  });
  // [data-preset-href="docs"] is a link that follows the option's data-set-docs and hides when it is empty.
  Array.prototype.forEach.call(sel.form.querySelectorAll("[data-preset-href]"), function (l) {
    var u = opt.getAttribute("data-set-" + l.getAttribute("data-preset-href")) || "";
    if (u) l.setAttribute("href", u);
    l.hidden = !u;
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

// [data-when-select] shows the [data-when="a b"] fields that apply to its value, within its own [data-kind] group (or
// the page). Without JavaScript every field stays visible.
(function () {
  Array.prototype.forEach.call(document.querySelectorAll("[data-when-select]"), function (sel) {
    var scope = sel.closest("[data-kind]") || document;
    function apply() {
      Array.prototype.forEach.call(scope.querySelectorAll("[data-when]"), function (el) {
        el.hidden = el.getAttribute("data-when").split(" ").indexOf(sel.value) < 0;
      });
    }
    sel.addEventListener("change", apply);
    apply();
  });
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


// Helper calls share one progress treatment: the button keeps its label, a thin bar runs along its bottom edge,
// and the muted status line under its row (the si_status component) shows the stage. The line turns red in that
// same spot on an error. The line always keeps its room, so nothing moves. Every exit re-enables the button. The
// fetch is aborted on leave and after data-si-wait seconds (at least the helper timeout).
function siLine(btn) {
  var row = btn.closest ? btn.closest(".row") : null;
  var line = row && row.nextElementSibling;
  return line && line.hasAttribute("data-si-status") ? line : null;
}
function siSay(btn, text, bad) {
  var line = siLine(btn);
  if (!line) return;
  line.textContent = text || "";
  if (text) line.title = text; else line.removeAttribute("title");
  line.classList.toggle("bad", !!bad);
  line.classList.toggle("muted", !bad);
}
function siSecs(btn) {
  var s = parseInt(btn.getAttribute("data-si-wait"), 10);
  if (!s) {
    var other = document.querySelector("[data-si-wait]");
    if (other) s = parseInt(other.getAttribute("data-si-wait"), 10);
  }
  if (!s || s < 1) s = 300;
  return s;
}
function siStart(btn) {
  btn.classList.add("running");
  btn.disabled = true;
  var ac = new AbortController();
  var timed = false, left = false;
  var timer = setTimeout(function () { timed = true; ac.abort(); }, siSecs(btn) * 1000);
  var job = {
    signal: ac.signal,
    abort: function () { left = true; clearTimeout(timer); try { ac.abort(); } catch (err) { /* already ended */ } },
    finish: function () {
      clearTimeout(timer);
      btn.classList.remove("running");
      if (window.skgateSI === job) window.skgateSI = null;
    },
    timedOut: function () { return timed && !left; },
    left: function () { return left; }
  };
  window.skgateSI = job;
  window.skgateAbortSI = function () { if (window.skgateSI) window.skgateSI.abort(); };
  return job;
}
function readLines(r, onLine) {
  var type = (r.headers && r.headers.get && r.headers.get("Content-Type")) || "";
  if (type.indexOf("x-ndjson") < 0 || !r.body || !r.body.getReader) {
    return r.json().then(function (j) { onLine(j); });
  }
  var rd = r.body.getReader(), dec = new TextDecoder(), buf = "";
  function pump() {
    return rd.read().then(function (c) {
      if (c.done) {
        if (buf) { try { onLine(JSON.parse(buf)); } catch (err) { /* ignore a trailing scrap */ } }
        return;
      }
      buf += dec.decode(c.value, { stream: true });
      var parts = buf.split("\n");
      buf = parts.pop();
      parts.forEach(function (l) { if (!l) return; try { onLine(JSON.parse(l)); } catch (err) { /* skip */ } });
      return pump();
    });
  }
  return pump();
}

// The update pill posts one update and refreshes its row in place. The bar is drawn inside the pill.
// A failure, from the request or from the update itself, turns the pill red with the reason in its title and
// shows that reason as a toast, so it is seen on a phone too, where there is no hover.
document.addEventListener("submit", function (e) {
  var form = e.target;
  if (!form || !form.hasAttribute || !form.hasAttribute("data-update") || e.defaultPrevented) return;
  e.preventDefault();
  var btn = form.querySelector("button");
  var row = form.closest("tr");
  var id = row && row.id;
  if (!btn || btn.disabled) return;
  btn.disabled = true;
  btn.classList.add("running");
  function say(msg) { if (window.skgateToast) window.skgateToast("bad", msg); }
  function fail(msg) {
    var now = id && document.getElementById(id);
    var b = (now && now.querySelector("form[data-update] button")) || btn;
    b.classList.remove("running", "warn");
    b.removeAttribute("data-updating");
    b.disabled = false;
    b.classList.add("bad");
    b.title = msg;
    say(msg);
  }
  function poll() {
    fetch(location.pathname + location.search, { credentials: "same-origin", cache: "no-store", headers: { Accept: "text/html" } })
      .then(function (r) { if (!r.ok) throw { say: "skgate answered with an error (status " + r.status + "). Reload the page to see the update." }; return r.text(); })
      .then(function (html) {
        var doc = new DOMParser().parseFromString(html, "text/html");
        var fresh = id && doc.getElementById(id);
        var old = id && document.getElementById(id);
        if (fresh && old) old.replaceWith(document.importNode(fresh, true));
        var now = id && document.getElementById(id);
        if (now && now.querySelector("[data-updating]")) { setTimeout(poll, 700); return; }
        var b = now && now.querySelector("form[data-update] button.bad");
        if (b) say(b.title || "update failed");
      })
      .catch(function (err) { fail(err && err.say ? err.say : "Couldn't reach skgate. Reload the page to see the update."); });
  }
  fetch(formURL(form), { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: new URLSearchParams(new FormData(form)) })
    .then(function (r) {
      if (!r.ok) throw { say: "skgate answered with an error (status " + r.status + "). Try again, or reload the page." };
      return r.json().catch(function () { throw { say: "skgate answered with something unexpected. Reload the page and try again." }; });
    })
    .then(function (j) {
      if (!j || !j.toast || j.toast.k !== "ok") { fail((j && j.toast && j.toast.m) || "update failed"); return; }
      poll();
    })
    .catch(function (err) { fail(err && err.say ? err.say : "Couldn't reach skgate. Check your connection and try again."); });
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
      var empty = !src || !src.value.trim(), off = b.hasAttribute("data-suggest-off") && (!src || sourceKind(src.value) !== "api");
      b.disabled = off || empty || !!b._busy;
      if (!off) { if (empty) b.title = NEED_SOURCE; else b.removeAttribute("title"); }
      else if (src && sourceKind(src.value) === "api") b.removeAttribute("title");
    });
  }
  // Fields that only apply to one kind of source follow the source: the access token and the ref belong to a git
  // repository, the install command is hidden for a package (npm, PyPI) unless it already holds something.
  // sourceKind reads the address the way the server does, loosely: it only decides what is shown.
  function sourceKind(v) {
    v = v.trim();
    if (!v) return "";
    if (/^[\w.-]+:\d{1,5}(\/|$)/.test(v) && !/^(npm|pypi|uvx):/i.test(v)) return "api"; // mealie:9000
    var m = /^(https?):\/\/([^\/:@]+)([^@]*)$/i.exec(v);
    if (m && !/github|gitlab|bitbucket|gitea|forgejo|codeberg|sr\.ht|npmjs|pypi\.org/i.test(m[2]) &&
        (m[1].toLowerCase() === "http" || /(\.(json|ya?ml|toml)|openapi|swagger|api-docs)(\?|$)/i.test(m[3]) || m[3].split("/").filter(Boolean).length < 2)) return "api";
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
  function show(form, cls, label, lines, action, notes) {
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
    // a line is a string, or {warn: text}: a warning, led by the word Warning and listed before the rest
    lines.forEach(function (l) { var li = document.createElement("li"); if (l && l.warn) { li.className = "warn"; li.textContent = "Warning: " + l.warn; } else li.textContent = l; ul.appendChild(li); });
    // notes are folded away in a details block under the list; it is hidden when there are none
    var det = out.querySelector("[data-suggest-notes]"), nl = out.querySelector("[data-suggest-notes-list]");
    nl.textContent = "";
    (notes || []).forEach(function (n) { var li = document.createElement("li"); li.textContent = n; nl.appendChild(li); });
    det.hidden = !(notes && notes.length);
    det.open = false;
    det.querySelector("summary").textContent = "Notes (" + (notes ? notes.length : 0) + ")";
    out.hidden = false;
  }
  function sourceLine(s) {
    return [s.name, s.language, (s.files || []).join(", ")].filter(Boolean).join(" \u00b7 ");
  }
  function done(form, b, job, x) {
    if (job) job.finish();
    b._busy = false;
    sync(form);
    if (!x || x.left || (job && job.left())) return;
    if (x.timeout) { // a limit was hit: the line turns red, and a slow model offers the reasoning setting
      var t = x.timeout, slow = t.stage === "model";
      var why = t.kind === "idle" ? "No data for " + t.secs + "s." : "The overall limit of " + t.secs + "s was reached.";
      var lines = [why];
      if (slow) lines.push("Lower the MCP helper model's reasoning (now: " + (t.effort || "auto") + "), or pick a faster model.");
      siSay(b, "Timed out while " + t.where, true);
      show(form, "", "", lines, slow);
      return;
    }
    if (x.error) { // said once, on the status line under the button: no pill, no toast
      siSay(b, x.error, true);
      var out = form.querySelector("[data-suggest-out]");
      if (out) out.hidden = true;
      return;
    }
    siSay(b, "", false);
    if (x.result.kind === "openapi") { // an address of a REST API: the form becomes an OpenAPI upstream
      var sel = document.querySelector("[data-kind-select]");
      if (sel) { sel.value = "openapi"; sel.dispatchEvent(new Event("change", { bubbles: true })); }
      set(form, "oa_spec_url", x.result.spec_url);
      if (form.elements.alias && form.elements.alias.type !== "hidden" && !form.elements.alias.value) set(form, "alias", x.result.alias);
      form.dispatchEvent(new Event("input", { bubbles: true })); // the Add button follows the fields
      var found = x.result.title ? "Found " + x.result.title + "." : "Found the description.";
      siSay(b, found + " Review and save.", false);
      return;
    }
    apply(form, x.result);
    var lines = (x.result.warnings || []).map(function (w) { return { warn: w }; });
    lines.push("Review before saving");
    show(form, x.result.confidence === "high" ? "ok" : "warn", x.result.confidence + " confidence", lines, false, x.result.notes);
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
    var job = siStart(b);
    var stage = "Starting", chars = 0;
    function paint() {
      siSay(b, stage + (chars ? " \u00b7 " + (chars >= 1000 ? (chars / 1000).toFixed(1) + "k" : chars) + " chars" : ""), false);
    }
    paint();
    var finished = false;
    function finish(x) { if (!finished) { finished = true; done(form, b, job, x); } }
    function line(m) {
      if (!m) return;
      if (m.stage) {
        if (m.label && m.label !== stage) chars = 0;
        stage = m.label || stage;
        if (m.chars) chars = m.chars;
        paint();
      } else if (m.result || m.error) {
        finish(m);
      }
    }
    fetch(b.getAttribute("data-suggest"), { method: "POST", credentials: "same-origin", body: body, headers: { Accept: "application/x-ndjson" }, signal: job.signal })
      .then(function (r) {
        return readLines(r, function (m) {
          if (m && !m.stage && !m.result && !m.error && r.ok) m = { result: m };
          line(m);
        }).then(function () { if (!r.ok && !finished) finish({ error: "The request failed (HTTP " + r.status + ")." }); });
      })
      .catch(function () {
        if (job.left()) finish({ left: true });
        else if (job.timedOut()) finish({ error: "Timed out." });
        else finish({ error: "Couldn't reach skgate. Check your connection and try again." });
      })
      .then(function () { finish({ error: "the answer ended early" }); });
  });
})();

// JSON check: a textarea marked data-json-check is parsed as you type. The status line under it says "Valid JSON" (with
// the number of servers it can tell) or what is wrong; unreadable JSON marks the field invalid (aria-invalid, red
// border) and blocks the form's submit through the browser's own validation, like the expiration field. An empty
// field blocks too, quietly.
(function () {
  function count(v) {
    if (!v || typeof v !== "object" || Array.isArray(v)) return 0;
    var servers = v.mcpServers && typeof v.mcpServers === "object" ? v.mcpServers : v;
    if (servers === v && (v.command || v.url)) return 1;
    return Object.keys(servers).length;
  }
  function check(area) {
    var status = area.closest(".field") ? area.closest(".field").querySelector("[data-json-status]") : null;
    var text = area.value.trim(), msg = "", bad = false;
    if (!text) msg = "Paste or type the JSON to import.";
    else {
      try {
        var n = count(JSON.parse(text));
        msg = "Valid JSON" + (n ? " \u00b7 " + n + (n === 1 ? " server" : " servers") : "");
      } catch (err) { bad = true; msg = "Not valid JSON: " + err.message; }
    }
    area.setCustomValidity(bad || !text ? (bad ? "Fix the JSON first" : msg) : "");
    if (bad) area.setAttribute("aria-invalid", "true"); else area.removeAttribute("aria-invalid");
    if (status) { status.textContent = msg; status.classList.toggle("bad", bad); status.classList.toggle("muted", !bad); }
  }
  document.addEventListener("input", function (e) {
    var el = e.target && e.target.matches ? e.target : null;
    if (el && el.matches("[data-json-check]")) check(el);
  });
  Array.prototype.forEach.call(document.querySelectorAll("[data-json-check]"), check);
})();

// Description lines: a [data-desc-line] is one line of text with an ellipsis. Only while the text is cut off does it get
// .expands (the dotted underline, a pointer, button semantics) and open on a click or Enter/Space to show all of it; when it
// fits there is nothing to open and nothing looks clickable. It is checked again whenever the line changes size.
(function () {
  function fit(el) {
    var lh = parseFloat(getComputedStyle(el).lineHeight) || 20;
    if (el.classList.contains("open") && el.offsetHeight < lh * 1.5) el.classList.remove("open");
    var cut = el.classList.contains("open") || el.scrollWidth > el.clientWidth + 1;
    el.classList.toggle("expands", cut);
    if (cut) { el.setAttribute("role", "button"); el.tabIndex = 0; el.setAttribute("aria-expanded", el.classList.contains("open") ? "true" : "false"); }
    else { el.removeAttribute("role"); el.removeAttribute("tabindex"); el.removeAttribute("aria-expanded"); }
  }
  function toggle(el) {
    if (!el.classList.contains("expands")) return;
    el.setAttribute("aria-expanded", el.classList.toggle("open") ? "true" : "false");
  }
  document.addEventListener("click", function (e) { var el = e.target.closest && e.target.closest("[data-desc-line]"); if (el) toggle(el); });
  document.addEventListener("keydown", function (e) {
    var el = e.target.closest && e.target.closest("[data-desc-line]");
    if (el && el === e.target && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); toggle(el); }
  });
  var lines = Array.prototype.slice.call(document.querySelectorAll("[data-desc-line]"));
  lines.forEach(fit);
  if (window.ResizeObserver) {
    var ro = new ResizeObserver(function (list) { list.forEach(function (r) { fit(r.target); }); });
    lines.forEach(function (el) { ro.observe(el); });
  } else window.addEventListener("resize", function () { lines.forEach(fit); });
})();

// Filter box: [data-filter="#table"] narrows the rows marked data-filter-row to those whose text contains what is
// typed (case-insensitive), shows "n of N" beside the field and the row marked data-filter-empty when none match.
(function () {
  function apply(input) {
    var table = document.querySelector(input.getAttribute("data-filter"));
    if (!table) return;
    var q = input.value.trim().toLowerCase(), rows = table.querySelectorAll("tbody tr[data-filter-row]"), shown = 0;
    Array.prototype.forEach.call(rows, function (r) {
      var match = !q || r.textContent.toLowerCase().indexOf(q) >= 0;
      r.hidden = !match;
      if (match) shown++;
    });
    var empty = table.querySelector("tr[data-filter-empty]");
    if (empty) empty.hidden = shown > 0 || rows.length === 0;
    var count = input.parentNode.querySelector("[data-filter-count]");
    if (count) count.textContent = q ? shown + " of " + rows.length : "";
  }
  document.addEventListener("input", function (e) {
    var el = e.target && e.target.closest ? e.target.closest("[data-filter]") : null;
    if (el) apply(el);
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
    if (!beginSave(form)) return;
    var again = form.getAttribute("data-inline");
    var btn = form.querySelector("button");
    if (btn) btn.disabled = true;
    fetch(formURL(form), { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: new URLSearchParams(new FormData(form)) })
      .then(function (r) { if (!r.ok) throw new Error("status " + r.status); return r.json(); })
      .then(function (j) {
        var good = j.toast.k === "ok";
        if (!(form.hasAttribute("data-quiet") && good)) window.skgateToast(j.toast.k, j.toast.m);
        if (j.html) {
          var tpl = document.createElement("template");
          tpl.innerHTML = j.html;
          var fresh = tpl.content.firstElementChild;
          var old = document.querySelector("[data-suggest-controls]");
          if (fresh && old) old.replaceWith(fresh);
          else {
            // A page with its own Suggest controls (the tools page) takes only the helper model opener and its
            // dialog template. The dialog is reloaded from that template below; a stale one would show the old
            // values, and the next autosave would post them back.
            var opener = tpl.content.querySelector("[data-helper-opener]");
            if (opener) Array.prototype.forEach.call(document.querySelectorAll("[data-helper-opener]"), function (o) { o.replaceWith(document.importNode(opener, true)); });
          }
          if (window.skgateSuggestSync) window.skgateSuggestSync();
        }
        if (!good) return false;
        if (form._again) return true;
        var dlg = document.querySelector("[data-modal]");
        var top = dlg ? dlg.scrollTop : 0;
        if (again && bodyBusy(dlg, form)) return true;
        if (!again) {
          var close = dlg && dlg.querySelector("[data-modal-close]");
          if (close) close.click();
        } else {
          var src = document.querySelector(again), body = dlg && dlg.querySelector("[data-modal-body]");
          if (src && body) { body.textContent = ""; body.appendChild(src.content.cloneNode(true)); }
          if (dlg) dlg.scrollTop = top;
        }
        return true;
      })
      .catch(function () { window.skgateToast("bad", "request failed"); return false; })
      .then(function (good) {
        if (btn && btn.isConnected) btn.disabled = false;
        endSave(form, good === true);
      });
  });
})();

// data-autosave posts the field's form as soon as the value changes. A number field does this when the edit is
// committed, not on each keystroke.
document.addEventListener("change", function (e) {
  var el = e.target;
  if (!el || !el.hasAttribute || !el.hasAttribute("data-autosave") || !el.form) return;
  submitForm(el.form);
});

// Process output: [data-log] holds the filter bar and the [data-log-view] with one .logline per line. The page
// follows the live stream (server-sent events, resumed from the last line id) while it is open, keeps the view
// at the newest line until the reader scrolls up ("jump to latest" returns), and filters by text or /regex/,
// level and stderr. Times show as a clock or as "5 s ago".
(function () {
  var root = document.querySelector("[data-log]");
  if (!root) return;
  var view = root.querySelector("[data-log-view]");
  var box = root.querySelector("[data-log-filter]");
  var level = root.querySelector("[data-log-level]");
  var errBtn = root.querySelector("[data-log-stderr]");
  var timeBtn = root.querySelector("[data-log-time]");
  var count = root.querySelector("[data-log-count]");
  var jump = root.querySelector("[data-log-jump]");
  var empty = root.querySelector("[data-log-empty]");
  var copyBtn = root.querySelector("[data-copy]");
  var max = parseInt(root.getAttribute("data-max"), 10) || 1000;
  var last = parseInt(root.getAttribute("data-last"), 10) || 0;
  var rank = { debug: 0, info: 1, warn: 2, error: 3 };
  var follow = true, relative = false, es = null, lastLevel = "", quiet = false;
  try { relative = localStorage.getItem("skgate.logtime") === "ago"; } catch (e) {}

  // Removing old lines moves the scroll position by itself; that scroll is not the reader leaving the bottom.
  function settle() {
    quiet = true;
    requestAnimationFrame(function () { quiet = false; });
  }
  function lines() { return view.querySelectorAll(".logline"); }
  function atBottom() { return view.scrollHeight - view.scrollTop - view.clientHeight < 24; }
  function toBottom() { view.scrollTop = view.scrollHeight; }

  function ago(ms) {
    var s = Math.max(0, Math.round((Date.now() - ms) / 1000));
    if (s < 60) return s + " s ago";
    if (s < 3600) return Math.floor(s / 60) + " min ago";
    if (s < 86400) return Math.floor(s / 3600) + " h ago";
    return Math.floor(s / 86400) + " d ago";
  }
  function stamp(l) {
    var t = l.querySelector(".logtime");
    if (!t) return;
    if (!t._abs) t._abs = t.textContent;
    t.textContent = relative ? ago(parseInt(l.getAttribute("data-ms"), 10)) : t._abs;
  }
  function stampAll() { Array.prototype.forEach.call(lines(), stamp); }
  function mode() {
    timeBtn.textContent = relative ? "Times: ago" : "Times: clock";
    timeBtn.classList.toggle("on", relative);
    timeBtn.setAttribute("aria-pressed", relative ? "true" : "false");
  }

  // The level of a line without one is the level of the line before it (a stack trace belongs to its error).
  function effective(l) {
    var v = l.getAttribute("data-lvl");
    if (v) { lastLevel = v; return v; }
    if (l.getAttribute("data-src") === "sys") return "";
    return lastLevel;
  }
  function matcher() {
    var q = box.value;
    box.removeAttribute("aria-invalid");
    if (!q) return null;
    var m = /^\/(.+)\/(i?)$/.exec(q);
    if (m) {
      try { var re = new RegExp(m[1], m[2] || "i"); return function (t) { return re.test(t); }; }
      catch (e) { box.setAttribute("aria-invalid", "true"); return null; }
    }
    var low = q.toLowerCase();
    return function (t) { return t.toLowerCase().indexOf(low) >= 0; };
  }
  function apply() {
    var match = matcher(), min = level.value ? rank[level.value] : -1, errOnly = errBtn.classList.contains("on");
    var shown = 0, all = 0;
    lastLevel = "";
    Array.prototype.forEach.call(lines(), function (l) {
      var src = l.getAttribute("data-src"), lv = effective(l), ok = true;
      all++;
      if (src !== "sys") {
        if (errOnly && src !== "err") ok = false;
        if (ok && min >= 0 && !(lv in rank && rank[lv] >= min)) ok = false;
        if (ok && match && !match(l.querySelector(".logtext").textContent)) ok = false;
      } else if (match && min < 0 && !errOnly) {
        ok = match(l.querySelector(".logtext").textContent);
      }
      l.hidden = !ok;
      if (ok) shown++;
    });
    count.textContent = (box.value || level.value || errOnly) ? shown + " of " + all + " lines" : all + " lines";
    if (box.getAttribute("aria-invalid")) count.textContent = "not a valid pattern";
    empty.hidden = all > 0;
    if (follow) toBottom();
  }

  function make(j) {
    var l = document.createElement("div");
    l.className = "logline";
    l.setAttribute("data-id", j.id);
    l.setAttribute("data-run", j.run);
    l.setAttribute("data-src", j.src);
    if (j.lvl) l.setAttribute("data-lvl", j.lvl);
    l.setAttribute("data-ms", j.ms);
    var t = document.createElement("time");
    t.className = "logtime";
    t.title = j.full;
    t.textContent = j.abs;
    var s = document.createElement("span");
    s.className = "logsrc";
    s.textContent = j.src;
    var x = document.createElement("span");
    x.className = "logtext";
    x.textContent = j.text;
    l.appendChild(t); l.appendChild(s); l.appendChild(x);
    if (j.trunc) {
      var c = document.createElement("span");
      c.className = "chip";
      c.textContent = "cut";
      l.appendChild(c);
    }
    return l;
  }
  function divider(j) {
    var d = document.createElement("div");
    d.className = "logdivider";
    d.setAttribute("role", "separator");
    d.setAttribute("data-run", j.run);
    var m = document.createElement("span");
    m.className = "logmark";
    m.textContent = "since last start";
    var f = document.createElement("span");
    f.className = "muted";
    f.textContent = j.full;
    d.appendChild(m); d.appendChild(document.createTextNode(" ")); d.appendChild(f);
    return d;
  }
  // Only the current and the previous run are kept: older lines and their dividers go.
  function prune(run) {
    Array.prototype.forEach.call(view.querySelectorAll("[data-run]"), function (el) {
      if (parseInt(el.getAttribute("data-run"), 10) < run - 1) view.removeChild(el);
    });
  }
  function trim() {
    var ls = lines();
    for (var i = 0; i < ls.length - max; i++) view.removeChild(ls[i]);
    var first = view.firstElementChild;
    while (first && first.classList.contains("logdivider") && (!first.nextElementSibling || first.nextElementSibling.classList.contains("logdivider"))) {
      view.removeChild(first);
      first = view.firstElementChild;
    }
  }
  function add(j) {
    if (j.id <= last) return;
    last = j.id;
    if (j.start) {
      Array.prototype.forEach.call(view.querySelectorAll(".logmark"), function (m) { m.textContent = "previous run"; });
      prune(j.run);
      view.appendChild(divider(j));
    }
    var l = make(j);
    stamp(l);
    view.appendChild(l);
    if (j.lvl && level.hidden) level.hidden = false;
  }

  var pending = 0; // a burst of lines is filtered once, not once per line
  function later() {
    if (!pending) pending = setTimeout(function () { pending = 0; apply(); }, 60);
  }
  function open() {
    if (es || !window.EventSource) return;
    es = new EventSource(root.getAttribute("data-stream") + "?after=" + last);
    es.addEventListener("line", function (e) {
      settle();
      try { add(JSON.parse(e.data)); } catch (err) { return; }
      trim();
      later();
    });
    es.addEventListener("reset", function () {
      settle();
      view.textContent = "";
      last = 0;
      later();
    });
  }
  function shut() { if (es) { es.close(); es = null; } }

  view.addEventListener("scroll", function () {
    if (quiet) return;
    follow = atBottom();
    jump.hidden = follow;
  });
  jump.addEventListener("click", function () { follow = true; jump.hidden = true; toBottom(); view.focus(); });
  box.addEventListener("input", apply);
  level.addEventListener("change", apply);
  errBtn.addEventListener("click", function () {
    var on = !errBtn.classList.contains("on");
    errBtn.classList.toggle("on", on);
    errBtn.setAttribute("aria-pressed", on ? "true" : "false");
    apply();
  });
  timeBtn.addEventListener("click", function () {
    relative = !relative;
    try { localStorage.setItem("skgate.logtime", relative ? "ago" : "clock"); } catch (e) {}
    mode();
    stampAll();
  });
  if (copyBtn) copyBtn.addEventListener("click", function () {
    document.getElementById("log-copy").textContent = Array.prototype.filter.call(lines(), function (l) { return !l.hidden; })
      .map(function (l) {
        return l.querySelector(".logtime").title + " [" + l.getAttribute("data-src") + "] " + l.querySelector(".logtext").textContent;
      }).join("\n");
  });
  document.addEventListener("visibilitychange", function () { if (document.hidden) shut(); else open(); });
  window.addEventListener("pagehide", shut);
  setInterval(function () { if (relative && !document.hidden) stampAll(); }, 10000);

  mode();
  stampAll();
  apply();
  toBottom();
  open();
})();

// OpenAPI form: "Check" posts the address or the pasted text and shows what it holds and what is wrong
// with it. With the assistant, "Repair" proposes fixes as a diff; "Apply" puts the repaired text into the paste box and checks it again.
// Nothing is saved from here: the form's own Add or Save button does that.
(function () {
  var btn = document.querySelector("[data-oa-check]");
  if (!btn) return;
  var form = btn.closest("form"), out = form.querySelector("[data-oa-out]");
  var pill = out.querySelector("[data-oa-pill]"), summary = out.querySelector("[data-oa-summary]"), list = out.querySelector("[data-oa-issues]");
  var repairRow = out.querySelector("[data-oa-repair-row]"), diff = out.querySelector("[data-oa-diff]");
  var diffList = out.querySelector("[data-oa-diff-list]"), diffTitle = out.querySelector("[data-oa-diff-title]");
  var repaired = "";
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function post(url) {
    var body = new URLSearchParams();
    body.set("csrf", btn.getAttribute("data-csrf"));
    body.set("oa_spec_url", form.elements.oa_spec_url.value);
    body.set("oa_spec_text", form.elements.oa_spec_text.value);
    return fetch(url, { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: body })
      .then(function (r) { return r.json(); });
  }
  function show(cls, text) { pill.className = "pill " + cls; pill.textContent = text; out.hidden = false; }
  function clear() { list.textContent = ""; diff.hidden = true; repairRow.hidden = true; diffList.textContent = ""; repaired = ""; }
  btn.addEventListener("click", function () {
    clear();
    summary.textContent = "";
    show("off", "checking");
    btn.disabled = true;
    post(btn.getAttribute("data-oa-check-url")).then(function (j) {
      btn.disabled = false;
      if (!j.ok) { show("bad", "unreadable"); summary.textContent = j.error || "cannot read the description"; return; }
      summary.textContent = (j.title ? j.title + ": " : "") + j.reads + " read, " + j.writes + " write";
      // nothing to skip when no GET can run the check
      Array.prototype.forEach.call(form.querySelectorAll("[data-oa-skip-btn]"), function (x) { x.hidden = j.probe === false; });
      var base = form.querySelector("#oa-servers");
      if (base && j.servers) j.servers.forEach(function (u) {
        var have = Array.prototype.some.call(base.options, function (o) { return o.value === u; });
        if (!have) { var o = el("option"); o.value = u; base.appendChild(o); }
      });
      var n = (j.issues || []).length;
      show(n ? "warn" : "ok", n ? n + (n === 1 ? " problem" : " problems") : "OK");
      (j.issues || []).slice(0, 20).forEach(function (is) {
        list.appendChild(el("li", "", is.path + ": " + is.message));
      });
      if (n > 20) list.appendChild(el("li", "", "and " + (n - 20) + " more"));
      if (n) {
        if (j.ai) repairRow.hidden = false;
        else list.appendChild(el("li", "", "No repair: " + (j.aiWhy || "unavailable")));
      }
    }).catch(function () { btn.disabled = false; show("bad", "failed"); summary.textContent = ""; });
  });
  out.addEventListener("click", function (e) {
    var t = e.target && e.target.closest ? e.target : null;
    if (!t) return;
    var rb = t.closest("[data-oa-repair]");
    if (rb) {
      if (rb.disabled || rb.classList.contains("running")) return;
      if (!rb.getAttribute("data-si-wait") && btn.getAttribute("data-si-wait")) rb.setAttribute("data-si-wait", btn.getAttribute("data-si-wait"));
      var job = siStart(rb);
      diff.hidden = true;
      siSay(rb, "Asking SI", false);
      var body = new URLSearchParams();
      body.set("csrf", btn.getAttribute("data-csrf"));
      body.set("oa_spec_url", form.elements.oa_spec_url.value);
      body.set("oa_spec_text", form.elements.oa_spec_text.value);
      var done = false;
      function end(j) {
        if (done) return;
        done = true;
        job.finish();
        rb.disabled = false;
        if (!j || j.left || job.left()) return;
        if (j.error) { siSay(rb, j.error, true); return; }
        var res = j.result || j;
        siSay(rb, "", false);
        repaired = res.spec;
        diffList.textContent = "";
        (res.changes || []).forEach(function (c) {
          var li = el("li");
          li.appendChild(el("code", "", c.path));
          if (c.reason) li.appendChild(el("div", "muted", c.reason));
          if (c.before) li.appendChild(el("div", "del", "- " + c.before));
          if (c.after) li.appendChild(el("div", "add", "+ " + c.after));
          diffList.appendChild(li);
        });
        var left = (res.remaining || []).length;
        diffTitle.textContent = (res.changes || []).length + " changes, " + left + (left === 1 ? " problem" : " problems") + " left";
        diff.hidden = false;
      }
      fetch(btn.getAttribute("data-oa-repair-url"), { method: "POST", credentials: "same-origin", headers: { Accept: "application/x-ndjson" }, body: body, signal: job.signal })
        .then(function (r) {
          return readLines(r, function (m) {
            if (m && m.stage) { siSay(rb, m.label || m.stage, false); return; }
            if (m && !m.result && !m.error && r.ok) m = { result: m };
            end(m);
          });
        })
        .catch(function () { end(job.left() ? { left: true } : { error: job.timedOut() ? "Timed out." : "Couldn't reach skgate." }); })
        .then(function () { if (!done) end({ error: "the answer ended early" }); });
      return;
    }
    if (t.closest("[data-oa-apply]")) {
      form.elements.oa_spec_text.value = repaired;
      form.elements.oa_spec_text.dispatchEvent(new Event("input", { bubbles: true }));
      diff.hidden = true;
      window.skgateToast("ok", "Repaired");
      btn.click();
      return;
    }
    if (t.closest("[data-oa-discard]")) { diff.hidden = true; repaired = ""; }
  });
})();

// Tool picker (the tools page of an OpenAPI upstream). The counter follows the ticked tools live and is colored by
// level; the color alone signals the level, with no judgmental words. Switching on a whole verb raises a toast.
// Nothing is ever blocked. The verb switches act on the tools the filter shows. The filter narrows the rows by their
// text and opens the groups that have a match. "Suggest names and selection" asks the helper once for every tool:
// it fills names and descriptions and ticks a small core set. While it runs the button stays off.
(function () {
  var root = document.querySelector("[data-toolpick]");
  if (!root) return;
  var good = Number(root.getAttribute("data-good")), warn = Number(root.getAttribute("data-warn"));
  var counter = root.querySelector("[data-toolcount]");
  var nEl = root.querySelector("[data-toolcount-n]"), word = root.querySelector("[data-toolcount-word]");
  var filter = root.querySelector("[data-toolfilter]"), fcount = root.querySelector("[data-toolfilter-count]"), fempty = root.querySelector("[data-toolfilter-empty]");
  function level(n) { return n <= good ? "ok" : n <= warn ? "warn" : "bad"; }
  function boxes(scope) { return (scope || root).querySelectorAll("[data-tool-on]:not(:disabled)"); }
  function count() {
    var n = 0;
    Array.prototype.forEach.call(boxes(), function (b) { if (b.checked) n++; });
    return n;
  }
  function render() {
    var n = count(), l = level(n);
    nEl.textContent = n;
    word.textContent = n === 1 ? "tool exposed" : "tools exposed";
    counter.className = "toolcount " + l;
    Array.prototype.forEach.call(root.querySelectorAll("[data-verbgroup]"), function (g) {
      var all = boxes(g), on = 0;
      Array.prototype.forEach.call(all, function (b) { if (b.checked) on++; });
      g.querySelector("[data-verb-count]").textContent = on + " of " + all.length + " on";
      var t = g.querySelector("[data-verb-toggle]");
      t.checked = all.length > 0 && on === all.length;
      t.indeterminate = on > 0 && on < all.length;
      t.disabled = all.length === 0;
    });
  }
  root.addEventListener("change", function (e) {
    var t = e.target;
    if (t.matches && t.matches("[data-tool-on]")) { render(); return; }
    if (t.matches && t.matches("[data-verb-toggle]")) {
      var g = t.closest("[data-verbgroup]"), changed = 0;
      Array.prototype.forEach.call(boxes(g), function (b) {
        if (b.closest("[data-tool]").hidden || b.checked === t.checked) return;
        b.checked = t.checked;
        changed++;
      });
      var verb = g.getAttribute("data-verbgroup");
      render();
      if (t.checked && changed) window.skgateToast(verb === "GET" ? "ok" : "warn", changed + " " + verb + " " + (changed === 1 ? "tool" : "tools") + " on");
    }
  });
  function applyFilter() {
    var q = filter.value.trim().toLowerCase(), shown = 0, total = 0;
    Array.prototype.forEach.call(root.querySelectorAll("[data-tool]"), function (row) {
      total++;
      var text = row.textContent + " " + Array.prototype.map.call(row.querySelectorAll("input[type=text]"), function (i) { return i.value; }).join(" ");
      var hit = !q || text.toLowerCase().indexOf(q) >= 0;
      row.hidden = !hit;
      if (hit) shown++;
    });
    Array.prototype.forEach.call(root.querySelectorAll("[data-verbgroup]"), function (g) {
      var any = g.querySelector("[data-tool]:not([hidden])");
      g.hidden = !any;
      if (q && any) g.open = true;
    });
    fcount.textContent = q ? shown + " of " + total : "";
    fempty.hidden = !q || shown > 0;
  }
  filter.addEventListener("input", applyFilter);
  filter.addEventListener("keydown", function (e) { if (e.key === "Enter") e.preventDefault(); });
  // a save or an update draws the tool list again in place: count it and filter it as before
  document.addEventListener("skgate:refreshed", function () { if (!root.isConnected) return; render(); if (filter.value.trim()) applyFilter(); });
  var describe = root.querySelector("[data-tool-describe]");
  var busy = false;
  function allKeys() {
    var keys = [];
    Array.prototype.forEach.call(boxes(), function (b) { keys.push(b.value); });
    return keys;
  }
  function setBusy(on) {
    busy = on;
    if (describe && !describe.hasAttribute("title")) describe.disabled = on;
  }
  if (describe) describe.addEventListener("click", function () {
    if (describe.disabled || busy) return;
    var keys = allKeys();
    if (!keys.length) { siSay(describe, "no tools to name", true); return; }
    var job = siStart(describe);
    setBusy(true);
    siSay(describe, "Reading tools", false);
    var body = new URLSearchParams();
    body.set("csrf", root.querySelector('input[name="csrf"]').value);
    var done = false;
    function end(j) {
      if (done) return;
      done = true;
      job.finish();
      setBusy(false);
      if (!j || j.left || job.left()) return;
      if (j.error) { siSay(describe, j.error, true); return; }
      var res = j.result || j;
      var on = {};
      Array.prototype.forEach.call(res.on || [], function (k) { on[k] = true; });
      var named = 0, selected = 0;
      Array.prototype.forEach.call(root.querySelectorAll("[data-tool]"), function (row) {
        var key = row.getAttribute("data-key"), box = row.querySelector("[data-tool-on]");
        var s = res.tools && res.tools[key];
        if (s) {
          if (s.name) row.querySelector("[data-tool-name]").value = s.name;
          if (s.description) row.querySelector("[data-tool-desc]").value = s.description;
          named++;
        }
        if (box && !box.disabled) {
          box.checked = !!on[key];
          if (box.checked) selected++;
        }
      });
      render();
      siSay(describe, "Filled " + named + " " + (named === 1 ? "name" : "names") + ", " + selected + " on", false);
    }
    fetch(root.getAttribute("data-suggest-url"), { method: "POST", credentials: "same-origin", headers: { Accept: "application/x-ndjson" }, body: body, signal: job.signal })
      .then(function (r) {
        return readLines(r, function (m) {
          if (m && m.stage) { siSay(describe, m.label || m.stage, false); return; }
          if (m && !m.result && !m.error && r.ok) m = { result: m };
          end(m);
        });
      })
      .catch(function () { end(job.left() ? { left: true } : { error: job.timedOut() ? "Timed out." : "Couldn't reach skgate." }); })
      .then(function () { if (!done) end({ error: "the answer ended early" }); });
  });
  render();
})();

// A plain form (not an in-place save, not a confirmation) disables its buttons once it is sent, so a double click
// cannot post twice. They come back after a few seconds, if the answer was a download and the page stayed.
(function () {
  document.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form || !form.hasAttribute || e.defaultPrevented || form.hasAttribute("data-save") || form.hasAttribute("data-confirm")) return;
    if ((form.method || "").toLowerCase() !== "post") return;
    var btns = Array.prototype.slice.call(form.querySelectorAll("button:not([type=button]):not(:disabled), input[type=submit]:not(:disabled)"));
    setTimeout(function () {
      btns.forEach(function (b) { b.disabled = true; });
      setTimeout(function () { btns.forEach(function (b) { b.disabled = false; }); }, 8000);
    }, 0);
  });
  window.addEventListener("pageshow", function (e) {
    if (e.persisted) Array.prototype.forEach.call(document.querySelectorAll("button:disabled"), function (b) { b.disabled = false; });
  });
})();

// Upstream form: the submit button waits for what the chosen type needs, with the reason as its tooltip and as a
// visible hint. The form posts in place (Accept: application/json). The server tests the upstream first; on a
// failure the page stays as it is, with every field and the key kept, and a short error appears. After a good save
// the page goes on to the address the server names.
(function () {
  var form = document.querySelector("form[data-upstream-form]");
  if (!form) return;
  var wrap = form.querySelector("[data-gate-submit]");
  if (!wrap) return;
  var btn = wrap.querySelector("button"), why = wrap.querySelector("[data-gate-reason]");
  function val(n) { var f = form.elements[n]; return f && f.value != null ? String(f.value).trim() : ""; }
  function reason() {
    var kind = val("kind");
    if (val("mode") !== "edit" && !val("alias")) return "Enter an alias";
    if (kind === "remote" && !val("url")) return "Enter the URL";
    if (kind === "openapi" && !val("oa_spec_url") && !val("oa_spec_text") && !form.querySelector("[data-oa-has]")) return "Enter an address";
    if ((kind === "stdio" || kind === "git") && !val("source") && !val("command") && !val("command_pick") && !val("git_url")) return "Enter a source or a command";
    return "";
  }
  var busy = false;
  var skipBtn = form.querySelector("button[data-oa-skip-btn]"), skip = form.querySelector("[data-oa-skip]");
  function sync() {
    var r = busy ? "Testing" : reason();
    btn.disabled = !!r;
    if (skipBtn) skipBtn.disabled = !!r;
    if (r && !busy) btn.setAttribute("title", r); else btn.removeAttribute("title");
    btn.setAttribute("aria-describedby", why.id);
    why.textContent = r;
    why.hidden = !r;
  }
  form.addEventListener("input", sync);
  form.addEventListener("change", sync);
  // "Skip check" posts the same form with oa_skip_check=1: the server is not called.
  if (skipBtn && skip) skipBtn.addEventListener("click", function () { if (!busy && !reason()) { skip.value = "1"; form.requestSubmit(); } });
  sync();
  form.addEventListener("submit", function (e) {
    if (e.defaultPrevented) return;
    e.preventDefault();
    if (busy || reason()) return;
    busy = true;
    form.setAttribute("aria-busy", "true");
    sync();
    fetch(formURL(form), { method: "POST", credentials: "same-origin", headers: { Accept: "application/json", "X-Follow": "1" }, body: new URLSearchParams(new FormData(form)) })
      .then(function (r) { return r.json(); })
      .then(function (j) {
        if (j.to) { window.skgateLeaveGo = true; window.location.assign(j.to); return; }
        window.skgateToast(j.toast ? j.toast.k : "bad", j.toast ? j.toast.m : "The request failed.");
      })
      .catch(function () { window.skgateToast("bad", "The request failed."); })
      .then(function () { if (skip) skip.value = ""; busy = false; form.removeAttribute("aria-busy"); sync(); });
  });
})();

// Tool tester (OpenAPI tools page and the MCP Test page). A tool's Test button opens the shared dialog with a form made
// from the tool's input schema; "Edit as JSON" shows the raw arguments. Run calls the upstream through the gateway
// as the admin and the dialog then shows the answer, with a way back to the inputs. Nothing here is saved.
(function () {
  var body = document.querySelector("[data-modal-body]");
  if (!body) return;
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function kind(sch) {
    var t = sch && sch.type;
    if (Array.isArray(t)) t = t.filter(function (x) { return x !== "null"; })[0];
    if (sch && (sch.anyOf || sch.oneOf || sch.allOf)) return "json";
    return t || "string";
  }
  function sentence(text) { // one short plain line for helper text
    var t = String(text || "").replace(/\s+/g, " ").trim();
    var cut = t.search(/[.!?](\s|$)/);
    if (cut > 0) t = t.slice(0, cut + 1);
    return t.length > 140 ? t.slice(0, 137).trim() + "..." : t;
  }
  function start(root) {
    if (root._tryStarted) return;
    root._tryStarted = true;
    var q = function (n) { return root.querySelector("[data-try-" + n + "]"); };
    var edit = q("edit"), result = q("result"), form = q("form"), rawBox = q("rawbox"), raw = q("raw"), err = q("err");
    var run = q("run"), modeBtn = q("mode"), pill = q("pill").querySelector(".pill"), out = q("out");
    var csrf = root.getAttribute("data-csrf"), name = root.getAttribute("data-name");
    var fields = [], extra = {}, props = {}, asJSON = false;
    function post(url, data) {
      var b = new URLSearchParams();
      b.set("csrf", csrf);
      b.set("name", name);
      Object.keys(data || {}).forEach(function (k) { b.set(k, data[k]); });
      return fetch(url, { method: "POST", credentials: "same-origin", headers: { Accept: "application/json" }, body: b }).then(function (r) { return r.json(); });
    }
    function say(msg) { err.textContent = msg || ""; err.hidden = !msg; }
    function example(p, req) {
      if (p.default !== undefined) return p.default;
      if (p.example !== undefined) return p.example;
      if (Array.isArray(p.examples) && p.examples.length) return p.examples[0];
      if (Array.isArray(p.enum) && req) return p.enum[0];
      return undefined;
    }
    function build(schema, required) {
      form.textContent = "";
      fields = [];
      props = (schema && schema.properties) || {};
      var names = Object.keys(props);
      if (!names.length) form.appendChild(el("p", "muted", "This tool takes no arguments."));
      names.forEach(function (n) {
        var p = props[n] || {}, req = required.indexOf(n) >= 0, k = kind(p), ex = example(p, req), input, wrap = el("div", "field"), lab;
        if (Array.isArray(p.enum)) {
          input = el("select");
          if (!req) input.appendChild(el("option", "", ""));
          p.enum.forEach(function (v) { var o = el("option", "", String(v)); o.value = JSON.stringify(v); input.appendChild(o); });
          input.value = ex !== undefined ? JSON.stringify(ex) : (req ? JSON.stringify(p.enum[0]) : "");
          k = "enum";
        } else if (k === "boolean") {
          input = el("input"); input.type = "checkbox"; input.checked = ex === true;
        } else if (k === "integer" || k === "number") {
          input = el("input"); input.type = "number"; input.step = k === "integer" ? "1" : "any"; input.inputMode = "decimal";
          if (ex !== undefined) input.value = String(ex);
        } else if (k === "object" || k === "array" || k === "json") {
          input = el("textarea", "mono"); input.rows = 3; input.spellcheck = false;
          input.value = ex !== undefined ? JSON.stringify(ex, null, 2) : (req ? (k === "array" ? "[]" : "{}") : "");
        } else {
          input = el("input"); input.type = "text"; input.autocomplete = "off"; input.spellcheck = false;
          if (ex !== undefined) input.value = String(ex);
        }
        if (k === "boolean") {
          lab = el("label", "check"); lab.appendChild(input); lab.appendChild(document.createTextNode(" " + n));
          if (req) lab.appendChild(el("span", "muted", " required"));
        } else {
          lab = el("label"); lab.appendChild(document.createTextNode(n));
          if (req) lab.appendChild(el("span", "muted", " required"));
          lab.appendChild(input);
        }
        wrap.appendChild(lab);
        var help = sentence(p.description);
        if (help) wrap.appendChild(el("p", "muted", help));
        form.appendChild(wrap);
        fields.push({ name: n, kind: k, input: input, req: req, def: p.default });
      });
    }
    // fromForm collects the arguments the form holds; a string with the reason when a field is not valid.
    function fromForm() {
      var o = {};
      for (var i = 0; i < fields.length; i++) {
        var f = fields[i], v = f.input.value;
        if (f.kind === "boolean") {
          if (f.input.checked || f.req || f.def !== undefined) o[f.name] = f.input.checked;
          continue;
        }
        if (f.kind === "enum") { if (v !== "") o[f.name] = JSON.parse(v); continue; }
        if (String(v).trim() === "") {
          if (f.req) return f.name + " is required";
          continue;
        }
        if (f.kind === "integer" || f.kind === "number") {
          var n = Number(v);
          if (!isFinite(n)) return f.name + " must be a number";
          o[f.name] = n;
        } else if (f.kind === "object" || f.kind === "array" || f.kind === "json") {
          try { o[f.name] = JSON.parse(v); } catch (e) { return f.name + " is not valid JSON"; }
        } else {
          o[f.name] = v;
        }
      }
      Object.keys(extra).forEach(function (k) { if (!(k in o)) o[k] = extra[k]; });
      return o;
    }
    // toForm puts parsed arguments into the form; keys the schema does not list are kept aside.
    function toForm(o) {
      extra = {};
      Object.keys(o).forEach(function (k) { if (!props[k]) extra[k] = o[k]; });
      fields.forEach(function (f) {
        var v = o[f.name];
        if (f.kind === "boolean") f.input.checked = v === true;
        else if (f.kind === "enum") f.input.value = v === undefined ? "" : JSON.stringify(v);
        else if (f.kind === "object" || f.kind === "array" || f.kind === "json") f.input.value = v === undefined ? "" : JSON.stringify(v, null, 2);
        else f.input.value = v === undefined ? "" : String(v);
      });
    }
    function args() { // the arguments as an object, or a string with the reason
      if (asJSON) {
        var t = raw.value.trim();
        if (!t) return {};
        try { var v = JSON.parse(t); } catch (e) { return "The arguments are not valid JSON"; }
        return v && typeof v === "object" && !Array.isArray(v) ? v : "The arguments must be a JSON object";
      }
      return fromForm();
    }
    modeBtn.addEventListener("click", function () {
      say("");
      if (!asJSON) {
        var a = fromForm();
        raw.value = typeof a === "string" ? JSON.stringify({}, null, 2) : JSON.stringify(a, null, 2);
        asJSON = true;
        form.hidden = true; rawBox.hidden = false; modeBtn.textContent = "Edit as form";
      } else {
        var o = args();
        if (typeof o === "string") { say(o); return; }
        toForm(o);
        asJSON = false;
        form.hidden = false; rawBox.hidden = true; modeBtn.textContent = "Edit as JSON";
      }
    });
    run.addEventListener("click", function () {
      var a = args();
      if (typeof a === "string") { say(a); return; }
      say("");
      run.disabled = true;
      run.textContent = "Running";
      function show(cls, label, text) {
        pill.className = "pill " + cls; pill.textContent = label; out.textContent = text;
        edit.hidden = true; result.hidden = false;
      }
      post(root.getAttribute("data-run-url"), { args: JSON.stringify(a) }).then(function (j) {
        if (!j.ok) { show("bad", "Failed", j.error || "The request failed"); return; }
        show(j.isError ? "bad" : "ok", (j.isError ? "Error" : "OK") + " \u00b7 " + j.ms + " ms", j.text || "No answer text");
      }).catch(function () { show("bad", "Failed", "The request failed"); })
        .then(function () { run.disabled = false; run.textContent = "Run"; });
    });
    q("back").addEventListener("click", function () { result.hidden = true; edit.hidden = false; });
    post(root.getAttribute("data-info-url")).then(function (j) {
      if (j.error) { q("desc").textContent = j.error; return; }
      q("desc").textContent = sentence(j.description) || "";
      q("schema").textContent = JSON.stringify(j.schema || {}, null, 2);
      build(j.schema || {}, (j.schema && j.schema.required) || []);
      run.disabled = false;
    }).catch(function () { q("desc").textContent = "The request failed"; });
  }
  function scan() {
    Array.prototype.forEach.call(body.querySelectorAll("[data-try]"), start);
  }
  new MutationObserver(scan).observe(body, { childList: true });
  scan();
})();

// A submit that the page lets through is a chosen navigation (Save, Import, a confirmed delete). The tab-close
// warning stays quiet for it. The browser starts that navigation as its own task, so the quiet period has to
// outlast this turn. If the page stays (a download), the warning comes back.
document.addEventListener("submit", function (e) {
  if (e.defaultPrevented) return;
  window.skgateLeaveGo = true;
  setTimeout(function () { window.skgateLeaveGo = false; }, 3000);
});
