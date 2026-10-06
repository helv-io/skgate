// The update pill posts once and refreshes its row. The label stays "update", and a failure turns the pill red
// without adding a line. Usage: node update_pill.js <path to app.js>
const {JSDOM}=require("jsdom");const fs=require("fs");
const js=fs.readFileSync(process.argv[2],"utf8");
let fails=0; const ok=(c,m)=>{ if(!c){fails++;console.log("FAIL",m);} else console.log("ok  ",m); };
const wait=ms=>new Promise(r=>setTimeout(r,ms));
const html=`<!doctype html><body><table><tr id="up-repo"><td class="status">
<form method="post" action="/admin/upstreams/repo/process" data-update>
<input type="hidden" name="csrf" value="t"><input type="hidden" name="action" value="update">
<button type="submit" class="pill warn" title="a newer version is available">update</button>
</form></td><td id="neighbor">stay</td></tr></table></body>`;
function row(updating){
  return `<!doctype html><body><table><tr id="up-repo"><td class="status"><form data-update action="/admin/upstreams/repo/process"><button class="pill warn${updating?" running":""}"${updating?" disabled data-updating":""}>update</button></form></td><td id="neighbor">stay</td></tr></table></body>`;
}
(async()=>{
  { const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
    const w=dom.window, d=w.document; w.eval(js);
    let polls=0;
    w.fetch=(url,init)=>{
      if(init&&init.method==="POST") return Promise.resolve({ok:true,json:()=>Promise.resolve({toast:{k:"ok",m:"repo: update started"}})});
      polls++;
      return Promise.resolve({ok:true,text:()=>Promise.resolve(row(polls<2))});
    };
    d.querySelector("button").click();
    await wait(30);
    let btn=d.querySelector("#up-repo button");
    ok(btn.textContent==="update"&&btn.classList.contains("running")&&btn.hasAttribute("data-updating"),"the pill keeps its label and shows progress: "+btn.outerHTML);
    ok(d.querySelector("#neighbor").textContent==="stay","the cell beside it is unchanged");
    await wait(800);
    btn=d.querySelector("#up-repo button");
    ok(btn&&btn.textContent==="update"&&!btn.hasAttribute("data-updating"),"the row refreshes in place when the update finishes");
  }
  { const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
    const w=dom.window, d=w.document; w.eval(js);
    w.fetch=()=>Promise.resolve({ok:true,json:()=>Promise.resolve({toast:{k:"bad",m:"repo: update failed"}})});
    d.querySelector("button").click();
    await wait(30);
    const btn=d.querySelector("button");
    ok(btn.textContent==="update"&&btn.classList.contains("bad")&&!btn.disabled&&btn.title==="repo: update failed","a failure stays on the pill: "+btn.outerHTML);
    ok(d.querySelectorAll("#up-repo td").length===2,"no extra line was added");
  }
  console.log(fails?"FAILED":"ALL OK"); process.exit(fails?1:0);
})();
