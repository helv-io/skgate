// Dialogs are addressed by the URL fragment: opening sets it, every way of closing clears it, Back closes,
// a load or hashchange with the fragment opens. Usage: node modal_hash.js <dir with pages> <app.js> <cases json>
const {JSDOM}=require("jsdom");const fs=require("fs");
const dir=process.argv[2], js=fs.readFileSync(process.argv[3],"utf8"), cases=JSON.parse(process.argv[4]);
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
const tick=(w)=>new Promise(r=>w.setTimeout(r,40));
function load(file, path, hash, native){
  const html=fs.readFileSync(dir+"/"+file,"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
  const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost"+path+(hash||"")});
  const w=dom.window;
  if(native){
    w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute("open","");};
    w.HTMLDialogElement.prototype.close=function(){ if(this.hasAttribute("open")){this.removeAttribute("open"); this.dispatchEvent(new w.Event("close"));} };
  }
  w.eval(js);
  return {w,d:w.document};
}
(async()=>{
for (const native of [false,true]) for (const c of cases) {
  const tag=`[${c.page} ${c.id}${native?" native":""}] `;
  const hash=()=>w.location.hash, shown=()=>dlg.hasAttribute("open");
  const id=c.id, frag="#"+encodeURIComponent(id);
  let {w,d}=load(c.page,c.path,"",native);
  let dlg=d.querySelector("[data-modal]");
  const opener=()=>d.querySelector(`[data-dialog-open="#${id}"]`);
  ok(!!opener(),tag+"has an opener");
  ok(!!d.getElementById(id)&&d.getElementById(id).hasAttribute("data-dialog-content"),tag+"has its content template");
  const len0=w.history.length;
  // click sets the fragment
  opener().dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  ok(shown()&&hash()===frag,tag+"click opens and sets the fragment ("+hash()+")");
  // close button clears it
  d.querySelector("[data-modal-close]").click(); await tick(w);
  ok(!shown()&&hash()==="","close button closes and clears the fragment ("+hash()+")");
  ok(w.location.pathname===c.path&&w.history.length<=len0+1,tag+"same page, no stray entries");
  // Escape
  opener().dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  dlg.dispatchEvent(new w.KeyboardEvent("keydown",{key:"Escape",bubbles:true,cancelable:true})); await tick(w);
  ok(!shown()&&hash()==="",tag+"Escape clears the fragment ("+hash()+")");
  // backdrop
  opener().dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  dlg.dispatchEvent(new w.MouseEvent("click",{bubbles:true})); await tick(w);
  ok(!shown()&&hash()==="",tag+"backdrop click clears the fragment ("+hash()+")");
  // a click inside keeps it open and keeps the fragment
  opener().dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  d.querySelector("[data-modal-body]").dispatchEvent(new w.MouseEvent("click",{bubbles:true})); await tick(w);
  ok(shown()&&hash()===frag,tag+"a click inside changes nothing");
  // Back closes, Forward reopens
  w.history.back(); await tick(w);
  ok(!shown()&&hash()==="",tag+"Back closes the dialog ("+hash()+")");
  w.history.forward(); await tick(w);
  ok(shown()&&hash()===frag&&d.querySelector("[data-modal-title]").textContent!=="",tag+"Forward reopens it");
  w.history.back(); await tick(w);
  // hashchange opens and closes
  w.location.hash=frag; await tick(w);
  ok(shown(),tag+"setting the fragment opens the dialog");
  w.location.hash=""; await tick(w);
  ok(!shown(),tag+"clearing the fragment closes it");
  // a load with the fragment opens it, and closing it clears the fragment without adding history
  ({w,d}=load(c.page,c.path,frag,native)); dlg=d.querySelector("[data-modal]");
  ok(shown()&&d.querySelector("[data-modal-title]").textContent!=="",tag+"a load with the fragment opens the dialog");
  const len1=w.history.length;
  d.querySelector("[data-modal-close]").click(); await tick(w);
  ok(!shown()&&hash()===""&&w.history.length===len1,tag+"closing it clears the fragment in place ("+hash()+", "+len1+"->"+w.history.length+")");
  // an unknown fragment opens nothing
  ({w,d}=load(c.page,c.path,"#nothing-here",native)); dlg=d.querySelector("[data-modal]");
  ok(!shown(),tag+"an unknown fragment opens nothing");
}
// two dialogs on one page: opening the second from the first keeps one entry; closing clears the fragment
{ const c=cases.find(x=>x.second); if(c){
  const {w,d}=load(c.page,c.path,"",false); const dlg=d.querySelector("[data-modal]");
  const a=d.querySelector(`[data-dialog-open="#${c.id}"]`), b=d.querySelector(`[data-dialog-open="#${c.second}"]`);
  a.dispatchEvent(new w.MouseEvent("click",{bubbles:true})); const t1=d.querySelector("[data-modal-title]").textContent;
  b.dispatchEvent(new w.MouseEvent("click",{bubbles:true}));
  ok(w.location.hash==="#"+encodeURIComponent(c.second)&&d.querySelector("[data-modal-body]").children.length>0,"a second dialog replaces the first and its fragment");
  ok(d.querySelector("[data-modal-body]").querySelectorAll("table").length<=2,"the first dialog's content is gone");
  d.querySelector("[data-modal-close]").click(); await tick(w);
  ok(w.location.hash===""&&!dlg.hasAttribute("open"),"closing it clears the fragment");
}}
// a confirmation never touches the address
{ const c=cases.find(x=>x.confirm); if(c){
  const {w,d}=load(c.page,c.path,"",false); const dlg=d.querySelector("[data-modal]");
  const f=[...d.querySelectorAll("form[data-confirm]")][0], btn=f.querySelector("button");
  const ev=new w.Event("submit",{cancelable:true,bubbles:true}); ev.submitter=btn; f.dispatchEvent(ev);
  ok(dlg.hasAttribute("open")&&w.location.hash==="","a confirmation adds no fragment");
  d.querySelector("[data-modal-cancel]").click(); await tick(w);
  ok(!dlg.hasAttribute("open")&&w.location.hash==="","cancelling it leaves the address alone");
}}
console.log(fails?("FAILED "+fails):"ALL OK"); process.exit(fails?1:0);
})();
