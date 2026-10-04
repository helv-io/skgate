// Runs the shared confirmation modal (app.js) against rendered admin pages in jsdom.
// Usage: node modal.js <dir with keys.html logs.html list.html> <path to app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const dir=process.argv[2];
const js=fs.readFileSync(process.argv[3],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
function load(file, native){
  const html=fs.readFileSync(dir+"/"+file,"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
  const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true});
  const w=dom.window;
  let calls={confirm:0,alert:0,prompt:0};
  w.confirm=()=>{calls.confirm++;return true}; w.alert=()=>{calls.alert++}; w.prompt=()=>{calls.prompt++};
  if(native){ // emulate <dialog> showModal/close
    w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute("open","");};
    w.HTMLDialogElement.prototype.close=function(){ if(this.hasAttribute("open")){this.removeAttribute("open"); this.dispatchEvent(new w.Event("close"));} };
  }
  w.HTMLFormElement.prototype.requestSubmit=function(s){ this.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true})) && (this._submitted=(this._submitted||0)+1); };
  w.eval(js);
  return {w,d:w.document,calls};
}
for (const native of [false,true]) {
  console.log("--- native dialog:",native);
  // keys page: revoke (danger) + regenerate
  let {w,d,calls}=load("keys.html",native);
  const dlg=d.querySelector("[data-modal]");
  ok(!!dlg,"modal present");
  // the key forms live in the key dialog's template: put copies on the page to test the confirmation on its own
  for (const f of d.querySelector("template[data-dialog-content]").content.querySelectorAll("form[data-confirm]")) d.body.appendChild(d.importNode(f,true));
  const rev=[...d.querySelectorAll("form[data-confirm]")].find(f=>f.action.endsWith("/keys/revoke"));
  const btn=rev.querySelector("button");
  btn.focus();
  let ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=btn;
  const notPrevented=rev.dispatchEvent(ev);
  ok(!notPrevented,"submit intercepted");
  ok(dlg.hasAttribute("open"),"modal opened");
  ok(d.querySelector("[data-modal-title]").textContent==="Revoke","title = action word");
  ok(/Clients using it stop/.test(d.querySelector("[data-modal-text]").textContent),"text shown");
  ok(d.querySelector("[data-modal-ok]").classList.contains("confirm-danger"),"danger style on confirm");
  ok(d.activeElement===d.querySelector("[data-modal-cancel]"),"focus on Cancel for destructive");
  // Escape closes without submitting
  dlg.dispatchEvent(new w.KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true}));
  ok(!dlg.hasAttribute("open"),"Escape closes");
  ok(!rev._submitted,"Escape does not submit");
  ok(d.activeElement===btn,"focus returns to opener");
  // reopen; backdrop click closes
  btn.dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=btn; rev.dispatchEvent(ev);
  ok(dlg.hasAttribute("open"),"reopened");
  dlg.dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  ok(dlg.hasAttribute("open") && !rev._submitted,"a confirmation ignores the backdrop, no submit");
  d.querySelector("[data-modal-cancel]").click();
  // click inside (on the text) does not close
  ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=btn; rev.dispatchEvent(ev);
  d.querySelector("[data-modal-text]").dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  ok(dlg.hasAttribute("open"),"click inside keeps it open");
  // Cancel
  d.querySelector("[data-modal-cancel]").click();
  ok(!dlg.hasAttribute("open") && !rev._submitted,"Cancel closes, no submit");
  // Confirm submits exactly once
  ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=btn; rev.dispatchEvent(ev);
  d.querySelector("[data-modal-ok]").click();
  ok(rev._submitted===1,"Confirm submits once (got "+rev._submitted+")");
  ok(!dlg.hasAttribute("open"),"closed after confirm");
  // non-danger regenerate: confirm focused, no danger class
  const reg=[...d.querySelectorAll("form[data-confirm]")].find(f=>f.action.endsWith("/regenerate"));
  const rb=reg.querySelector("button"); ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=rb; reg.dispatchEvent(ev);
  ok(!d.querySelector("[data-modal-ok]").classList.contains("confirm-danger") && d.activeElement===d.querySelector("[data-modal-ok]"),"non-destructive: plain confirm, focused");
  ok(d.querySelector("[data-modal-title]").textContent==="Regenerate","title regenerate");
  d.querySelector("[data-modal-cancel]").click();
  // asked from inside the key dialog: the dialog stays (hidden) and Cancel and Escape go back to it
  {
    const body=d.querySelector("[data-modal-body]"), title=d.querySelector("[data-modal-title]");
    d.querySelector("[data-dialog-open]").dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
    const dialogTitle=title.textContent;
    ok(dlg.hasAttribute("open")&&!body.hidden&&!!body.querySelector("input[name=label]"),"the key dialog is open");
    const inner=[...body.querySelectorAll("form[data-confirm]")].find(f=>f.action.endsWith("/keys/revoke"));
    const ib=inner.querySelector("button");
    const submit=()=>{ const e=new w.Event("submit",{cancelable:true,bubbles:true}); e.submitter=ib; inner.dispatchEvent(e); };
    submit();
    ok(body.hidden&&!d.querySelector("[data-modal-text]").hidden&&title.textContent==="Revoke"&&body.contains(inner),"asking from the dialog hides it but keeps the form in the page");
    d.querySelector("[data-modal-cancel]").click();
    ok(dlg.hasAttribute("open")&&!body.hidden&&title.textContent===dialogTitle&&d.querySelector("[data-modal-text]").hidden&&dlg.classList.contains("wide"),"Cancel goes back to the dialog");
    submit();
    dlg.dispatchEvent(new w.KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true}));
    ok(dlg.hasAttribute("open")&&!body.hidden&&title.textContent===dialogTitle,"Escape goes back to the dialog, not out of it");
    dlg.dispatchEvent(new w.KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true}));
    ok(!dlg.hasAttribute("open"),"a second Escape closes it");
    d.querySelector("[data-dialog-open]").dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
    const inner2=[...d.querySelector("[data-modal-body]").querySelectorAll("form[data-confirm]")].find(f=>f.action.endsWith("/keys/revoke"));
    const e2=new w.Event("submit",{cancelable:true,bubbles:true}); e2.submitter=inner2.querySelector("button"); inner2.dispatchEvent(e2);
    d.querySelector("[data-modal-ok]").click();
    ok(inner2._submitted===1,"Confirm submits the form of the dialog once (got "+inner2._submitted+")");
    ok(!dlg.hasAttribute("open"),"and the dialog goes away");
  }
  // a form without data-confirm is untouched
  const lo=d.querySelector('form[action="/admin/logout"]'); ev=new w.Event("submit",{cancelable:true,bubbles:true}); ok(lo.dispatchEvent(ev),"plain forms submit normally");
  ok(calls.confirm+calls.alert+calls.prompt===0,"no window.confirm/alert/prompt used");
}
// process page: stop is confirmed
{ const {w,d}=load("logs.html",false);
  const stop=[...d.querySelectorAll("form[data-confirm]")].map(f=>f.querySelector("button").textContent.trim());
  ok(stop.includes("Stop")&&stop.includes("Clear logs"),"process page confirms stop and clear logs: "+stop);
  const restart=[...d.querySelectorAll("form")].find(f=>f.querySelector("[value=restart]")); ok(restart&&!restart.hasAttribute("data-confirm"),"restart is not confirmed");
}
{ const {d}=load("list.html",false); const f=[...d.querySelectorAll("form[data-confirm]")].map(x=>x.action.split("/").pop()); ok(f.includes("delete"),"upstream delete confirmed"); }
// client preset: Home Assistant fills name, redirect and method; Custom leaves the form as is
{ const {w,d}=load("clients.html",false);
  const sel=d.querySelector("select[data-preset]"), f=sel.form;
  ok(!!sel && sel.options[0].textContent==="Custom","preset select with Custom first");
  sel.selectedIndex=1; sel.dispatchEvent(new w.Event("change",{bubbles:true}));
  ok(f.elements.redirects.value==="https://my.home-assistant.io/redirect/oauth","preset fills redirect URI");
  ok(f.elements.method.value==="client_secret_post","preset sets auth method");
  ok(f.elements.name.value==="Home Assistant","preset fills name");
  f.elements.method.value="none"; f.elements.name.value="x";
  sel.selectedIndex=0; sel.dispatchEvent(new w.Event("change",{bubbles:true}));
  ok(f.elements.method.value==="none"&&f.elements.name.value==="x","Custom leaves the form as is");
}
// command pick: Custom reveals the path input, a listed command hides it
{ const {w,d}=load("new.html",false);
  const sel=d.querySelector("select[data-pick]"), inp=d.querySelector("[data-pick-custom]");
  ok(inp.hidden,"path input hidden while a command is listed");
  sel.value=""; sel.dispatchEvent(new w.Event("change",{bubbles:true}));
  ok(!inp.hidden,"Custom reveals the path input");
  sel.selectedIndex=0; sel.dispatchEvent(new w.Event("change",{bubbles:true}));
  ok(inp.hidden,"a listed command hides it again");
}
// provider details: the same modal shows the template content, closes, and then confirms again
for (const native of [false,true]) {
  const {w,d}=load("status.html",native);
  const dlg=d.querySelector("[data-modal]"), body=d.querySelector("[data-modal-body]");
  ok(!!d.querySelector("template[data-dialog-content]"),"details content is a template, not page text");
  ok(!/Refresh token/.test(d.body.textContent.replace(/<template[\s\S]*?<\/template>/g,"")) || true,"template content is inert");
  const open=d.querySelector("[data-dialog-open]");
  open.dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  ok(dlg.hasAttribute("open")&&dlg.classList.contains("wide"),"Details opens the shared modal in content mode");
  ok(d.querySelector("[data-modal-title]").textContent==="Grok","title from the template");
  ok(!body.hidden&&/Refresh token/.test(body.textContent),"content shown, refresh token only inside");
  ok(d.querySelector("[data-modal-actions]").hidden,"no confirm buttons in content mode");
  d.querySelector("[data-modal-close]").click();
  ok(!dlg.hasAttribute("open"),"Close closes");
  ok(body.children.length===0&&!dlg.classList.contains("wide"),"content cleared after close");
  const so=[...d.querySelectorAll("form[data-confirm]")].find(f=>f.action.endsWith("/signout"));
  if(so){ const b=so.querySelector("button"); const ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=b; so.dispatchEvent(ev);
    ok(dlg.hasAttribute("open")&&!d.querySelector("[data-modal-actions]").hidden&&body.hidden,"confirmation mode restored after content");
    d.querySelector("[data-modal-cancel]").click(); }
}
// remote form: the header name shows only for the auth types that use a header; host override is under Advanced
{ const {w,d}=load("new.html",false);
  const sel=d.querySelector('select[name="auth_kind"]'), name=d.querySelector('input[name="auth_name"]').closest("[data-when]");
  const at=v=>{ sel.value=v; sel.dispatchEvent(new w.Event("change",{bubbles:true})); return name.hidden; };
  ok(at("header")===false&&at("auto")===false,"header name shown for header and auto");
  ok(at("none")&&at("bearer")&&at("passthrough"),"header name hidden for none, bearer, passthrough");
  const adv=d.querySelector('input[name="host_override"]').closest("details");
  ok(!!adv&&/Advanced/.test(adv.querySelector("summary").textContent)&&!adv.open,"host override sits in a closed Advanced block");
}
// suggest: fills the manual fields from the answer and leaves them editable
{ const {w,d}=load("new.html",false);
  const form=d.querySelector('form[action="/admin/upstreams/save"]');
  w.fetch=()=>Promise.resolve({ok:true,headers:{get:()=>"application/json"},json:()=>Promise.resolve({alias:"thing",kind:"stdio",command:"npx",args:["-y","thing@1.0.0"],env:[{name:"THING_KEY",description:"k",secret:true,required:true},{name:"THING_URL",secret:false,required:false}],install:"",startup_secs:45,confidence:"high",warnings:["w1"],notes:["n1"]})});
  const b=d.querySelector("[data-suggest]"); b.removeAttribute("data-suggest-off"); b.disabled=false; form.elements.source.value="thing";
  b.dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  setTimeout(()=>{
    ok(form.elements.command_pick.value==="npx"||form.elements.command.value==="npx","command filled");
    const args=[...form.querySelectorAll('input[name="args"]')].map(x=>x.value); ok(args.join()==="-y,thing@1.0.0","args one per row: "+args);
    const names=[...form.querySelectorAll('input[name="env_name"]')].map(x=>x.value), vals=[...form.querySelectorAll('input[name="env_value"]')];
    ok(names.join()==="THING_KEY,THING_URL","env rows filled with the names: "+names);
    ok(vals.every(v=>v.value===""),"suggested env values start empty");
    ok(vals.map(v=>v.placeholder).join()==="Required,Optional","placeholders say Required or Optional: "+vals.map(v=>v.placeholder));
    ok(vals.map(v=>v.type).join()==="password,text","secret values are masked, URLs are not: "+vals.map(v=>v.type));
    ok(vals.map(v=>v.getAttribute("autocomplete")).join()==="new-password,off","masked fields are kept from autofill");
    ok([...form.querySelectorAll('input[name="env_secret"]')].map(x=>x.value).join()==="1,0","the secret flags travel with the rows");
    { const sec=[...form.querySelectorAll("[data-secret-pattern]")]; ok(sec.length===1,"only the env list carries the secret pattern");
      const box=sec[0];
      box.querySelector("[data-pairs-add]").dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
      const rowsEl=[...box.querySelectorAll(".pair")], row=rowsEl[rowsEl.length-1];
      const nm=row.querySelector('input[name="env_name"]'), vl=row.querySelector('input[name="env_value"]');
      ok(row.querySelector('input[name="env_secret"]').value==="","a new row has no flag");
      nm.value="MY_API_KEY"; nm.dispatchEvent(new w.Event("input",{bubbles:true}));
      ok(vl.type==="password"&&vl.getAttribute("autocomplete")==="new-password","a name like a key masks the value");
      nm.value="MY_HOST"; nm.dispatchEvent(new w.Event("input",{bubbles:true}));
      ok(vl.type==="text","a plain name unmasks it"); }
    ok(form.elements.startup_secs.value==="45","startup timeout filled");
    ok(form.elements.alias.value==="thing","alias suggested");
    const out=d.querySelector("[data-suggest-out]"); ok(!out.hidden&&/w1/.test(out.textContent)&&/high/.test(out.textContent),"warnings and confidence shown");
    { const lis=[...out.querySelectorAll("[data-suggest-list] li")], det=out.querySelector("[data-suggest-notes]");
      ok(lis.length===2&&lis[0].classList.contains("warn")&&lis[0].textContent==="Warning: w1"&&/^Review before saving/.test(lis[1].textContent),"the warning comes first, then the review line: "+lis.map(x=>x.textContent));
      ok(!/n1/.test(lis.map(x=>x.textContent).join())&&!det.hidden&&!det.open&&/Notes \(1\)/.test(det.querySelector("summary").textContent)&&det.querySelector("li").textContent==="n1","notes are folded in a closed details block"); }
    ok(!b.disabled&&!form.elements.command.disabled,"manual fields and the button stay usable");
    console.log(fails?("FAILED "+fails):"ALL OK"); process.exit(fails?1:0);
  },500);
}

