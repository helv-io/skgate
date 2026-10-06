// Leave guard in jsdom: an explicit Save with unsaved edits, and a running helper call, ask before navigation.
// Stay, Close and Escape stay. Leave goes, and aborts the helper call. A field that saves on change does not ask.
// Usage: node leave.js <dir with new.html and status.html> <path to app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const dir=process.argv[2], js=fs.readFileSync(process.argv[3],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
function load(file, url){
  const html=fs.readFileSync(dir+"/"+file,"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
  const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:url||"http://localhost/admin/upstreams/new"});
  const w=dom.window;
  w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute("open","");};
  w.HTMLDialogElement.prototype.close=function(){ if(this.hasAttribute("open")){this.removeAttribute("open"); this.dispatchEvent(new w.Event("close"));} };
  w.fetch=()=>Promise.resolve({ok:true,json:()=>Promise.resolve({toast:{k:"ok",m:""}}),text:()=>Promise.resolve(""),headers:{get:()=>"application/json"}});
  w.eval(js);
  w._gone="";
  w.skgateGo=function(url){w._gone=String(url);};
  return {w,d:w.document};
}
const title=d=>d.querySelector("[data-modal-title]").textContent;
const text=d=>d.querySelector("[data-modal-text]").textContent;
(async()=>{
  { const {w,d}=load("new.html");
    const form=d.querySelector("form[data-upstream-form]"), alias=form.elements.alias;
    alias.value="thing"; alias.dispatchEvent(new w.Event("input",{bubbles:true}));
    let ev=new w.Event("beforeunload",{cancelable:true}); w.dispatchEvent(ev);
    ok(ev.defaultPrevented,"tab close is blocked while the form is dirty");
    const link=d.querySelector('a[href="/admin/keys"]');
    link.click();
    const dlg=d.querySelector("dialog");
    ok(dlg.hasAttribute("open")&&title(d)==="Unsaved changes"&&text(d)==="Leave and lose them?","unsaved changes asks: "+title(d)+" / "+text(d));
    const acts=[...d.querySelector("[data-modal-actions]").querySelectorAll("button")];
    ok(acts[0].textContent==="Stay"&&acts[1].textContent==="Leave"&&!acts[0].classList.contains("confirm-danger"),"Stay is bottom-left, Leave beside it");
    const close=d.querySelector("[data-modal-close]");
    ok(close.textContent==="Close"&&!close.hidden,"Close is shown");
    ok(d.activeElement===acts[0],"Stay is focused");
    const here=w.location.pathname;
    acts[0].click();
    ok(!dlg.hasAttribute("open")&&w.location.pathname===here,"Stay leaves the page where it is");
    link.click();
    close.click();
    ok(!dlg.hasAttribute("open")&&w.location.pathname===here,"Close is Stay");
    link.click();
    dlg.dispatchEvent(new w.KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true}));
    ok(!dlg.hasAttribute("open")&&w.location.pathname===here,"Escape is Stay");
    link.click();
    d.querySelector("[data-modal-cancel]").click();
    ok(w._gone.endsWith("/admin/keys"),"Leave follows the link: "+w._gone);
  }
  { const {w,d}=load("new.html");
    const alias=d.querySelector("form[data-upstream-form]").elements.alias;
    alias.value="thing"; alias.dispatchEvent(new w.Event("input",{bubbles:true}));
    alias.value=alias.defaultValue; alias.dispatchEvent(new w.Event("input",{bubbles:true}));
    const ev=new w.Event("beforeunload",{cancelable:true}); w.dispatchEvent(ev);
    ok(!ev.defaultPrevented,"a clean form does not block unload");
    d.querySelector('a[href="/admin/keys"]').click();
    ok(title(d)!=="Unsaved changes","a clean form does not ask");
  }
  { const {w,d}=load("new.html");
    const form=d.querySelector("form[data-upstream-form]");
    form.elements.alias.value="thing"; form.elements.alias.dispatchEvent(new w.Event("input",{bubbles:true}));
    form.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true}));
    ok(title(d)!=="Unsaved changes","submitting the form itself does not ask");
  }
  { const {w,d}=load("status.html","http://localhost/admin");
    const tpl=d.getElementById("helper-model");
    ok(!!tpl,"the helper dialog is on the status page");
    d.body.appendChild(tpl.content.cloneNode(true));
    const sel=d.querySelector('select[name=model][data-autosave]');
    ok(!!sel,"the helper model saves on change");
    if(sel.options.length>1){ sel.selectedIndex=sel.selectedIndex===0?1:0; sel.dispatchEvent(new w.Event("change",{bubbles:true})); }
    ok(!w.skgateLeaveActive(),"a field that saves on change does not count as unsaved");
  }
  { const {w,d}=load("new.html");
    let aborted=false;
    w.fetch=(url,init)=>new Promise((_,reject)=>{
      if(init&&init.signal) init.signal.addEventListener("abort",()=>{aborted=true; reject(Object.assign(new Error("Aborted"),{name:"AbortError"}));});
    });
    const b=d.querySelector("[data-suggest]"), form=b.closest("form");
    form.elements.source.value="https://github.com/acme/thing";
    form.elements.source.dispatchEvent(new w.Event("input",{bubbles:true}));
    b.removeAttribute("data-suggest-off"); b.disabled=false;
    b.click();
    ok(!!w.skgateSI&&b.disabled&&b.classList.contains("running")&&b.textContent==="Suggest configuration","the button keeps its label while SI runs");
    d.querySelector('a[href="/admin/keys"]').click();
    ok(title(d)==="SI is still working."&&text(d)==="Leave and lose this result?","a running helper call asks: "+title(d)+" / "+text(d));
    const ev=new w.Event("beforeunload",{cancelable:true}); w.dispatchEvent(ev);
    ok(ev.defaultPrevented,"tab close is blocked while SI runs");
    d.querySelector("[data-modal-cancel]").click();
    ok(aborted,"Leave aborts the fetch");
  }
  console.log(fails?"FAILED":"ALL OK"); process.exit(fails?1:0);
})();
