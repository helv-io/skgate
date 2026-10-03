// Dialog sections in jsdom: unsaved edits are tracked per section, closing asks first, and saving one section
// updates the page without losing what is typed in another.
// Usage: node dirty.js <dir with before.html after.html> <path to app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const dir=process.argv[2], js=fs.readFileSync(process.argv[3],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
const wait=ms=>new Promise(r=>setTimeout(r,ms));
const read=f=>fs.readFileSync(dir+"/"+f,"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
function load(posts){
  const dom=new JSDOM(read("before.html"),{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin"});
  const w=dom.window, d=w.document, toasts=[], sent=[];
  w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute("open","");};
  w.HTMLDialogElement.prototype.close=function(){ if(this.hasAttribute("open")){this.removeAttribute("open"); this.dispatchEvent(new w.Event("close"));} };
  w.HTMLFormElement.prototype.requestSubmit=function(){ this.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true})); };
  w.eval(js);
  w.skgateToast=(k,m)=>toasts.push([k,m]);
  w.fetch=(url,opt)=>{
    if(opt&&opt.method==="POST"){ sent.push([url,String(opt.body)]); const r=posts.shift(); if(r==="net") return Promise.reject(new TypeError("x"));
      return Promise.resolve({ok:true,json:()=>Promise.resolve(r)}); }
    return Promise.resolve({ok:true,text:()=>Promise.resolve(read("after.html"))});
  };
  return {w,d,toasts,sent};
}
const type=(w,el,v)=>{ el.value=v; el.dispatchEvent(new w.Event("input",{bubbles:true})); };
const key=(w,d,k)=>d.querySelector("dialog").dispatchEvent(new w.KeyboardEvent("keydown",{key:k,bubbles:true,cancelable:true}));
const open=d=>{ d.querySelector('[data-dialog-open="#provider-grok"]').click(); return d.querySelector("[data-modal-body]"); };
(async()=>{
  // typing marks only its own section; closing asks, Cancel keeps the edit, Discard closes
  { const {w,d}=load([]); const body=open(d), dlg=d.querySelector("dialog");
    const sec=n=>body.querySelector('[data-section="'+n+'"]'), chip=n=>sec(n).querySelector("[data-dirty-chip]");
    ok(dlg.hasAttribute("open"),"the dialog opens");
    ok([...body.querySelectorAll("[data-dirty-chip]")].every(c=>c.hidden),"nothing is dirty at first (a select without a marked option is not an edit)");
    const base=body.querySelector('input[name=base]'); type(w,base,"https://example.test/v1");
    ok(sec("upstream").hasAttribute("data-dirty")&&!chip("upstream").hidden,"the edited section is marked");
    ok(!sec("aliases").hasAttribute("data-dirty")&&chip("aliases").hidden,"the others are not");
    d.querySelector("[data-modal-close]").click();
    ok(dlg.hasAttribute("open")&&body.hidden,"Close with unsaved edits does not close");
    ok(d.querySelector("[data-modal-title]").textContent==="Discard changes"&&/Upstream URLs/.test(d.querySelector("[data-modal-text]").textContent),"it asks, naming the section: "+d.querySelector("[data-modal-text]").textContent);
    ok(d.querySelector("[data-modal-ok]").textContent==="Discard","the button names the action");
    d.querySelector("[data-modal-cancel]").click();
    ok(!body.hidden&&dlg.hasAttribute("open")&&base.value==="https://example.test/v1","Cancel returns to the edit, unchanged");
    key(w,d,"Escape");
    ok(body.hidden&&dlg.hasAttribute("open"),"Escape asks too");
    key(w,d,"Escape");
    ok(!body.hidden&&base.value==="https://example.test/v1","Escape on the question goes back to the edit");
    key(w,d,"Escape"); d.querySelector("[data-modal-ok]").click();
    ok(!dlg.hasAttribute("open"),"Discard closes the dialog");
    const again=open(d);
    ok(again.querySelector('input[name=base]').value!=="https://example.test/v1"&&again.querySelectorAll("[data-dirty]").length===0,"reopened, the discarded edit is gone");
    d.querySelector("[data-modal-close]").click();
    ok(!dlg.hasAttribute("open"),"a clean dialog closes at once");
    // the dialog went away with edits in the model section: also asks
    open(d); const sel=d.querySelector('[data-modal-body] select[name=model]'); sel.selectedIndex=sel.options.length-1; sel.dispatchEvent(new w.Event("change",{bubbles:true}));
    d.querySelector("[data-modal-close]").click();
    ok(/MCP helper model/.test(d.querySelector("[data-modal-text]").textContent),"a changed select counts as an edit");
  }
  // saving the alias section updates the page and leaves the edit of another section alone
  { const {w,d,toasts,sent}=load([{toast:{k:"ok",m:"alias saved"}}]); const body=open(d);
    const base=body.querySelector('input[name=base]'); type(w,base,"https://example.test/v1");
    const cardBefore=d.getElementById("grok").textContent;
    const af=body.querySelector('form[action$="/aliases/put"]'); type(w,af.querySelector('input[name=name]'),"fast");
    af.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true}));
    await wait(80);
    ok(sent.length===1&&/name=fast/.test(sent[0][1])&&/csrf=/.test(sent[0][1]),"the form was posted in place: "+sent.map(s=>s[0]));
    ok(toasts.length===1&&toasts[0][0]==="ok"&&toasts[0][1]==="alias saved","the toast is shown");
    const b2=d.querySelector("[data-modal-body]");
    ok(/fast/.test(b2.querySelector('[data-section=aliases]').textContent),"the alias list shows the new alias without a reload");
    ok(b2.querySelector('input[name=base]').value==="https://example.test/v1","the edit in another section is still there");
    ok(b2.querySelector('[data-section=upstream]').hasAttribute("data-dirty")&&!b2.querySelector('[data-section=upstream] [data-dirty-chip]').hidden,"and still counts as unsaved");
    ok(!b2.querySelector('[data-section=aliases]').hasAttribute("data-dirty")&&b2.querySelector('form[action$="/aliases/put"] input[name=name]').value==="","the saved section is clean");
    ok(d.getElementById("grok").textContent!==cardBefore,"the card behind the dialog shows the new state");
    ok(d.getElementById("provider-grok").content.textContent.includes("fast"),"reopening shows the new state");
    d.querySelector("[data-modal-close]").click();
    ok(/Upstream URLs/.test(d.querySelector("[data-modal-text]").textContent)&&!/Model aliases/.test(d.querySelector("[data-modal-text]").textContent),"closing still asks about the unsaved section only");
  }
  // a refused save keeps the edit and stays dirty; a network failure says so
  { const {w,d,toasts}=load([{toast:{k:"bad",m:"URLs must be absolute http(s)"}},"net"]); const body=open(d);
    const f=body.querySelector('form[action$="/settings"]'), base=f.querySelector('input[name=base]'); type(w,base,"nope");
    f.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true})); await wait(60);
    ok(toasts[0]&&toasts[0][0]==="bad"&&base.value==="nope"&&body.querySelector('[data-section=upstream]').hasAttribute("data-dirty"),"a refused save keeps the edit and the mark");
    ok(!f.querySelector("button").disabled,"and the button works again");
    f.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true})); await wait(60);
    ok(toasts[1]&&toasts[1][1]==="Couldn't reach skgate. Check your connection and try again.","a network failure is explained");
  }
  console.log(fails?"FAILED "+fails:"ALL OK"); process.exit(fails?1:0);
})();
