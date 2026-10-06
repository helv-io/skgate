// Runs the Suggest progress script (app.js) in jsdom against the rendered add-upstream page with a scripted stream.
// Usage: node suggest_timeout.js <upstreams.html> <path to app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const html=fs.readFileSync(process.argv[2],"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
const js=fs.readFileSync(process.argv[3],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
const wait=ms=>new Promise(r=>setTimeout(r,ms));
async function run(lines, hang, reject){
  const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
  const w=dom.window, d=w.document;
  const toasts=[]; 
  w.eval(js);
  w.skgateToast=(k,m)=>toasts.push([k,m]);
  w.TextDecoder=require("util").TextDecoder;
  const enc=new (require("util").TextEncoder)();
  w.fetch=()=>reject?Promise.reject(new TypeError("Failed to fetch")):Promise.resolve({ok:true,headers:{get:()=>"application/x-ndjson"},body:{getReader(){
    let i=0; return {read(){ return i<lines.length ? Promise.resolve({done:false,value:enc.encode(lines[i++]+"\n")}) : (hang ? new Promise(()=>{}) : Promise.resolve({done:true})); }};
  }}});
  const form=d.querySelector("[data-suggest]").closest("form");
  form.elements.source.value="https://github.com/acme/thing";
  form.elements.source.dispatchEvent(new w.Event("input",{bubbles:true}));
  d.querySelector("[data-suggest]:not([disabled])").click();
  await wait(60);
  const out=d.querySelector("[data-suggest-out]");
  return {d,out,pill:out.querySelector("[data-suggest-pill]"),status:d.querySelector("[data-si-status]"),btn:d.querySelector("[data-suggest]"),toasts};
}
(async()=>{
  let r=await run([
    JSON.stringify({stage:"model",label:"Asking the model"}),
    JSON.stringify({stage:"model",label:"Asking the model",chars:1500}),
    JSON.stringify({error:"timed out while asking the model: no data for 45s; lower the reasoning",timeout:{kind:"idle",stage:"model",where:"asking the model",secs:45,effort:"low"}})]);
  ok(r.status&&r.status.classList.contains("bad")&&/Timed out while asking the model/.test(r.status.textContent),"status: "+(r.status&&r.status.textContent));
  ok(r.btn.textContent==="Suggest configuration"&&!r.btn.disabled&&!r.btn.classList.contains("running"),"the button keeps its label and works again");
  const act=r.out.querySelector("[data-suggest-action]");
  ok(act&&act.textContent==="Lower reasoning"&&act.getAttribute("data-dialog-open")==="#helper-model","Lower reasoning button opens the helper dialog");
  ok(/No data for 45s/.test(r.out.textContent)&&/reasoning \(now: low\)/.test(r.out.textContent),"explanation names the reasoning");
  ok(r.toasts.length===0,"a timeout is not also a toast");

  r=await run([
    JSON.stringify({stage:"fetch",label:"Fetching repo"}),
    JSON.stringify({error:"timed out while fetching the repo after 300s",timeout:{kind:"cap",stage:"fetch",where:"fetching the repo",secs:300}})]);
  ok(r.status&&/^Timed out while fetching the repo/.test(r.status.textContent)&&r.status.classList.contains("bad"),"fetch timeout: "+(r.status&&r.status.textContent));
  ok(!r.out.querySelector("[data-suggest-action]"),"no reasoning button when the model was not the slow part");

  r=await run([
    JSON.stringify({stage:"model",label:"Asking the model"}),
    JSON.stringify({stage:"model",label:"Asking the model",chars:2500})], true);
  ok(r.status&&/Asking the model/.test(r.status.textContent)&&/2\.5k chars/.test(r.status.textContent)&&!r.status.classList.contains("bad"),"stage line while streaming: "+(r.status&&r.status.textContent));
  ok(r.btn.disabled&&r.btn.classList.contains("running")&&r.btn.textContent==="Suggest configuration","the button keeps its label, stays disabled, and shows the bar");
  ok(!r.out.querySelector("[data-suggest-action]"),"no button while running");

  // one error, said once: in the panel, with no pill and no toast
  r=await run([JSON.stringify({error:"the repository could not be cloned"})]);
  ok(r.status&&r.status.classList.contains("bad")&&r.status.textContent==="the repository could not be cloned","the error is the status line: "+(r.status&&r.status.textContent));
  ok(r.out.hidden,"the result panel stays closed");
  ok(r.toasts.length===0,"no toast for an error already on the line");
  ok(!r.btn.disabled,"the button works again after a failure");
  ok(r.status.parentElement===r.btn.parentElement,"the status line sits beside the button");

  r=await run([],false,true);
  ok(r.status&&r.status.textContent.includes("Couldn't reach skgate. Check your connection and try again.")&&r.status.classList.contains("bad"),"network failure: "+(r.status&&r.status.textContent));
  ok(!r.btn.disabled,"the button works again after a network failure");
  ok(r.toasts.length===0,"no toast for a network failure");

  // Suggest needs a source and nothing else
  { const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
    const w=dom.window, d=w.document; w.eval(js);
    const form=d.querySelector("[data-suggest]").closest("form"), b=d.querySelector("[data-suggest]");
    const type=v=>{form.elements.source.value=v;form.elements.source.dispatchEvent(new w.Event("input",{bubbles:true}));};
    const shown=n=>{const e=form.querySelector(n);return e&&!e.hidden;};
    ok(b.disabled&&b.title==="Enter a source first","disabled with a reason while the source is empty");
    ok(form.elements.alias.value==="","the alias is empty");
    type("   "); ok(b.disabled,"blanks are not a source");
    type("npm:thing"); ok(!b.disabled&&!b.title,"enabled once there is a source, with the alias still empty");
    ok(!shown('[data-show-for="git"]')&&!shown('[data-hide-for="package"]'),"a package hides the token, the ref and the install command");
    type("https://github.com/acme/thing");
    ok(shown('[data-show-for="git"]')&&shown('[data-hide-for="package"]'),"a repository shows the token, the ref and the install command");
    ok([...form.querySelectorAll('[data-show-for="git"]')].every(e=>!e.hidden)&&form.querySelectorAll('[data-show-for="git"]').length>=2,"token and ref are both shown");
    type("@scope/pkg@1.2.3"); ok(!shown('[data-show-for="git"]'),"a scoped package is a package");
    type("git@github.com:acme/thing.git"); ok(shown('[data-show-for="git"]'),"an ssh address is a repository");
    type("github.com/acme/thing"); ok(shown('[data-show-for="git"]'),"a bare host path is a repository");
    type("npm:thing"); form.elements.install.value="npm ci"; form.elements.install.dispatchEvent(new w.Event("input",{bubbles:true}));
    ok(shown('[data-hide-for="package"]'),"an install command that is set stays visible");
    type(""); ok(b.disabled&&!shown('[data-show-for="git"]'),"clearing the source disables Suggest again");
  }
  console.log(fails?"FAILED":"ALL OK"); process.exit(fails?1:0);
})();
