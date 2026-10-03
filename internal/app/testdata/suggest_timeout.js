// Runs the Suggest progress script (app.js) in jsdom against the rendered add-upstream page with a scripted stream.
// Usage: node suggest_timeout.js <upstreams.html> <path to app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const html=fs.readFileSync(process.argv[2],"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
const js=fs.readFileSync(process.argv[3],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
const wait=ms=>new Promise(r=>setTimeout(r,ms));
async function run(lines, hang){
  const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
  const w=dom.window, d=w.document;
  const toasts=[]; 
  w.eval(js);
  w.skgateToast=(k,m)=>toasts.push([k,m]);
  w.TextDecoder=require("util").TextDecoder;
  const enc=new (require("util").TextEncoder)();
  w.fetch=()=>Promise.resolve({ok:true,headers:{get:()=>"application/x-ndjson"},body:{getReader(){
    let i=0; return {read(){ return i<lines.length ? Promise.resolve({done:false,value:enc.encode(lines[i++]+"\n")}) : (hang ? new Promise(()=>{}) : Promise.resolve({done:true})); }};
  }}});
  const form=d.querySelector("[data-suggest]").closest("form");
  form.elements.source.value="https://github.com/acme/thing";
  d.querySelector("[data-suggest]:not([disabled])").click();
  await wait(60);
  const out=d.querySelector("[data-suggest-out]");
  return {d,out,pill:out.querySelector("[data-suggest-pill]"),toasts};
}
(async()=>{
  let r=await run([
    JSON.stringify({stage:"model",label:"Asking the model"}),
    JSON.stringify({stage:"model",label:"Asking the model",chars:1500}),
    JSON.stringify({error:"timed out while asking the model: no data for 45s; lower the effort",timeout:{kind:"idle",stage:"model",where:"asking the model",secs:45,effort:"low"}})]);
  ok(/^Timed out while asking the model \u00b7 \d+s$/.test(r.pill.textContent),"pill: "+r.pill.textContent);
  ok(r.pill.classList.contains("bad"),"pill is in the bad state");
  const act=r.out.querySelector("[data-suggest-action]");
  ok(act&&act.textContent==="Lower effort"&&act.getAttribute("data-dialog-open")==="#helper-model","Lower effort button opens the helper dialog");
  ok(/No data for 45s/.test(r.out.textContent)&&/effort \(now: low\)/.test(r.out.textContent),"explanation names the effort");
  ok(r.toasts.length===1&&r.toasts[0][0]==="bad"&&/lower the effort/.test(r.toasts[0][1]),"toast carries the reason");

  r=await run([
    JSON.stringify({stage:"fetch",label:"Fetching repo"}),
    JSON.stringify({error:"timed out while fetching the repo after 300s",timeout:{kind:"cap",stage:"fetch",where:"fetching the repo",secs:300}})]);
  ok(/^Timed out while fetching the repo/.test(r.pill.textContent),"fetch timeout pill: "+r.pill.textContent);
  ok(!r.out.querySelector("[data-suggest-action]"),"no effort button when the model was not the slow part");

  r=await run([
    JSON.stringify({stage:"model",label:"Asking the model"}),
    JSON.stringify({stage:"model",label:"Asking the model",chars:2500})], true);
  ok(/^Asking the model \u00b7 2\.5k chars \u00b7 \d+s$/.test(r.pill.textContent),"activity shown while streaming: "+r.pill.textContent);
  ok(!r.out.querySelector("[data-suggest-action]"),"no button while running");
  console.log(fails?"FAILED":"ALL OK"); process.exit(fails?1:0);
})();
