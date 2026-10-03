// The expiration field: typing asks the server what the text means (debounced, newest answer wins), the quick
// buttons fill the text in, and a failed check says so without blocking the form.
// Usage: node expiry.js <keys.html> <app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const html=fs.readFileSync(process.argv[2],"utf8").replace(/<script[^>]*src=[^>]*><\/script>/g,"");
const js=fs.readFileSync(process.argv[3],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/keys"});
const w=dom.window, d=w.document;
const calls=[]; let mode="ok"; const pend=[];
w.fetch=(u)=>{ calls.push(u); const q=decodeURIComponent(u.split("q=")[1]);
  if(mode==="fail") return Promise.reject(new Error("down"));
  const resp={ok:true,json:()=>Promise.resolve(q==="bad"?{ok:false,never:false,text:"not understood: bad. Try 30d"}:{ok:true,never:q==="",text:q===""?"Never expires":"Expires "+q})};
  if(mode==="slow-first" && calls.length===1) return new Promise(r=>pend.push(()=>r({ok:true,json:()=>Promise.resolve({ok:true,never:false,text:"Expires STALE"})})));
  return Promise.resolve(resp); };
w.eval(js);
const tick=(ms)=>new Promise(r=>setTimeout(r,ms));
const box=d.querySelector("form[action='/admin/keys/create'] [data-expiry]");
const input=box.querySelector("[data-expiry-input]"), prev=box.querySelector("[data-expiry-preview]");
const type=(v)=>{ input.value=v; input.dispatchEvent(new w.Event("input",{bubbles:true})); };
(async()=>{
  ok(prev.textContent==="Never expires"&&prev.classList.contains("muted"),"starts as Never expires");
  ok(input.getAttribute("autocomplete")==="off"&&input.getAttribute("autocapitalize")==="off"&&input.type==="text","a plain text field without autocorrect");
  type("3"); type("30"); type("30d"); await tick(80);
  ok(calls.length===0,"typing is debounced");
  await tick(150);
  ok(calls.length===1&&/q=30d$/.test(calls[0]),"one request for the final text: "+calls);
  await tick(20);
  ok(prev.textContent==="Expires 30d","the answer is shown");
  type("bad"); await tick(250);
  ok(prev.classList.contains("bad")&&!prev.classList.contains("muted")&&/Try 30d/.test(prev.textContent),"an unreadable text shows the hint in the error colour");
  type("1d"); await tick(250);
  ok(!prev.classList.contains("bad")&&prev.classList.contains("muted"),"a good text clears the error colour");
  // quick buttons
  const chips=[...box.querySelectorAll("[data-expiry-set]")].map(b=>b.textContent.trim());
  ok(chips.join()==="1 day,7 days,30 days,90 days,1 year,Never","quick buttons: "+chips);
  box.querySelector("[data-expiry-set='90d']").click(); await tick(20);
  ok(input.value==="90d"&&prev.textContent==="Expires 90d","a preset fills the field and shows its meaning at once");
  box.querySelector("[data-expiry-set='never']").click(); await tick(20);
  ok(input.value===""&&prev.textContent==="Never expires","Never empties the field");
  ok(box.querySelectorAll("button[type=button]").length===6,"presets never submit the form");
  // the pressed state follows the text: a preset is pressed while the field holds its value, nothing else is
  const pressed=()=>[...box.querySelectorAll("[data-expiry-set]")].filter(b=>b.getAttribute("aria-pressed")==="true").map(b=>b.getAttribute("data-expiry-set")).join();
  ok(pressed()==="never","an empty field starts with Never pressed: "+pressed());
  box.querySelector("[data-expiry-set='30d']").click(); await tick(20);
  ok(pressed()==="30d","a preset is pressed once clicked: "+pressed());
  type("next friday"); await tick(20);
  ok(pressed()==="","typing something else releases it: "+pressed());
  type("30D"); ok(pressed()==="30d","typing the value of a preset presses it: "+pressed());
  type(""); ok(pressed()==="never","an empty field is Never: "+pressed());
  // a slow answer for older text never overwrites a newer one
  mode="slow-first"; calls.length=0;
  type("7d"); await tick(200); type("2w"); await tick(200);
  pend.forEach(f=>f()); await tick(30);
  ok(prev.textContent==="Expires 2w","a late answer for older text is ignored: "+prev.textContent);
  // a failed check does not block anything
  mode="fail"; type("5d"); await tick(250);
  ok(!prev.classList.contains("bad")&&/server reads it when you save/.test(prev.textContent),"a failed check says the server will read it");
  console.log(fails?("FAILED "+fails):"ALL OK"); process.exit(fails?1:0);
})();
