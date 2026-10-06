// Dialog sections in jsdom: unsaved edits are tracked per section, closing asks first, and saving one
// section updates the page. The helper model and the aliases are separate dialogs.
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
const open=(d,id)=>{ d.querySelector('[data-dialog-open="#'+id+'"]').click(); return d.querySelector("[data-modal-body]"); };
const addName=body=>body.querySelector('input[type=text][name=name]');
(async()=>{
  // typing in the alias dialog marks that section; closing asks, Cancel keeps the edit, Discard closes
  { const {w,d}=load([]); const body=open(d,"aliases-grok"), dlg=d.querySelector("dialog");
    const sec=body.querySelector('[data-section=aliases]'), chip=sec.querySelector("[data-dirty-chip]");
    ok(dlg.hasAttribute("open"),"the alias dialog opens");
    ok(chip.hidden,"nothing is dirty at first");
    const name=addName(body); type(w,name,"fast");
    ok(sec.hasAttribute("data-dirty")&&!chip.hidden,"the edited section is marked");
    d.querySelector("[data-modal-close]").click();
    ok(dlg.hasAttribute("open")&&body.hidden,"Close with unsaved edits does not close");
    ok(d.querySelector("[data-modal-title]").textContent==="Discard changes"&&/Model aliases/.test(d.querySelector("[data-modal-text]").textContent),"it asks, naming the section: "+d.querySelector("[data-modal-text]").textContent);
    ok(d.querySelector("[data-modal-ok]").textContent==="Discard","the button names the action");
    d.querySelector("[data-modal-cancel]").click();
    ok(!body.hidden&&dlg.hasAttribute("open")&&name.value==="fast","Cancel returns to the edit, unchanged");
    key(w,d,"Escape");
    ok(body.hidden&&dlg.hasAttribute("open"),"Escape asks too");
    key(w,d,"Escape");
    ok(!body.hidden&&name.value==="fast","Escape on the question goes back to the edit");
    key(w,d,"Escape"); d.querySelector("[data-modal-ok]").click();
    ok(!dlg.hasAttribute("open"),"Discard closes the dialog");
    const again=open(d,"aliases-grok");
    ok(addName(again).value!=="fast"&&again.querySelectorAll("[data-dirty]").length===0,"reopened, the discarded edit is gone");
    d.querySelector("[data-modal-close]").click();
    ok(!dlg.hasAttribute("open"),"a clean dialog closes at once");
    // a changed helper-model select counts as an edit
    open(d,"helper-model"); const sel=d.querySelector('[data-modal-body] select[name=model]'); sel.selectedIndex=sel.options.length-1; sel.dispatchEvent(new w.Event("change",{bubbles:true}));
    d.querySelector("[data-modal-close]").click();
    ok(/MCP helper model/.test(d.querySelector("[data-modal-text]").textContent),"a changed select counts as an edit");
    d.querySelector("[data-modal-ok]").click();
  }
  // saving the alias updates the page and clears the field
  { const {w,d,toasts,sent}=load([{toast:{k:"ok",m:"alias saved"}}]); const body=open(d,"aliases-grok");
    const cardBefore=d.getElementById("grok").textContent;
    const af=addName(body).closest("form"); type(w,addName(body),"fast");
    af.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true}));
    await wait(80);
    ok(sent.length===1&&/name=fast/.test(sent[0][1])&&/csrf=/.test(sent[0][1]),"the form was posted in place: "+sent.map(s=>s[0]));
    ok(toasts.length===1&&toasts[0][0]==="ok"&&toasts[0][1]==="alias saved","the toast is shown");
    const b2=d.querySelector("[data-modal-body]");
    ok(/fast/.test(b2.querySelector("[data-section=aliases]").textContent),"the alias list shows the new alias without a reload");
    ok(!b2.querySelector("[data-section=aliases]").hasAttribute("data-dirty")&&addName(b2).value==="","the saved section is clean");
    ok(d.getElementById("grok").textContent!==cardBefore,"the card behind the dialog shows the new state");
    ok(d.getElementById("aliases-grok").content.textContent.includes("fast"),"reopening shows the new state");
    d.querySelector("[data-modal-close]").click();
    ok(!d.querySelector("dialog").hasAttribute("open"),"a clean dialog closes at once");
  }
  // a refused save keeps the edit and stays dirty; a network failure says so
  { const {w,d,toasts}=load([{toast:{k:"bad",m:"alias name is not valid"}},"net"]); const body=open(d,"aliases-grok");
    const f=addName(body).closest("form"), name=addName(body); type(w,name,"Bad");
    f.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true})); await wait(60);
    ok(toasts[0]&&toasts[0][0]==="bad"&&name.value==="Bad"&&body.querySelector("[data-section=aliases]").hasAttribute("data-dirty"),"a refused save keeps the edit and the mark");
    ok(!f.querySelector("button").disabled,"and the button works again");
    f.dispatchEvent(new w.Event("submit",{cancelable:true,bubbles:true})); await wait(60);
    ok(toasts[1]&&toasts[1][1]==="Couldn't reach skgate. Check your connection and try again.","a network failure is explained");
  }
  console.log(fails?"FAILED "+fails:"ALL OK"); process.exit(fails?1:0);
})();
