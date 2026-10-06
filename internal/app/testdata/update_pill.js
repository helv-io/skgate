// The update pill posts once, to its form's action attribute (not form.action, which the hidden "action" input
// shadows), and refreshes its row. The label stays "update". A failure turns the pill red without adding a line
// and shows its reason as a toast, so it is seen without hover. Usage: node update_pill.js <path to app.js>
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
    let polls=0, posted=[];
    w.fetch=(url,init)=>{
      if(init&&init.method==="POST") posted.push(String(url));
      if(init&&init.method==="POST") return Promise.resolve({ok:true,json:()=>Promise.resolve({toast:{k:"ok",m:"repo: update started"}})});
      polls++;
      return Promise.resolve({ok:true,text:()=>Promise.resolve(row(polls<2))});
    };
    d.querySelector("button").click();
    await wait(30);
    let btn=d.querySelector("#up-repo button");
    ok(btn.textContent==="update"&&btn.classList.contains("running")&&btn.hasAttribute("data-updating"),"the pill keeps its label and shows progress: "+btn.outerHTML);
    ok(posted.length===1&&posted[0]==="/admin/upstreams/repo/process","it posts to the row's process route: "+JSON.stringify(posted));
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
    const t=[...d.querySelectorAll(".toast")].map(x=>x.textContent.trim());
    ok(t.includes("repo: update failed"),"the failure is also a toast: "+JSON.stringify(t));
  }
  { const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
    const w=dom.window, d=w.document; w.eval(js);
    w.fetch=()=>Promise.resolve({ok:false,status:404,json:()=>Promise.reject(new Error("not json"))});
    d.querySelector("button").click();
    await wait(30);
    const btn=d.querySelector("button");
    ok(btn.classList.contains("bad")&&!btn.disabled&&/status 404/.test(btn.title),"an HTTP error names its status on the pill: "+btn.outerHTML);
    const t=[...d.querySelectorAll(".toast")].map(x=>x.textContent.trim());
    ok(t.some(x=>/status 404/.test(x)),"and as a toast: "+JSON.stringify(t));
  }
  { const dom=new JSDOM(html,{runScripts:"outside-only",pretendToBeVisual:true,url:"http://localhost/admin/upstreams"});
    const w=dom.window, d=w.document; w.eval(js);
    let polls=0;
    w.fetch=(url,init)=>{
      if(init&&init.method==="POST") return Promise.resolve({ok:true,json:()=>Promise.resolve({toast:{k:"ok",m:"repo: update started"}})});
      polls++;
      const h=polls<2?row(true):row(false).replace('class="pill warn"','class="pill bad" title="git fetch: repository not found"');
      return Promise.resolve({ok:true,text:()=>Promise.resolve(h)});
    };
    d.querySelector("button").click();
    await wait(900);
    const btn=d.querySelector("#up-repo button");
    ok(btn&&btn.classList.contains("bad"),"an update that fails on the server comes back red: "+(btn&&btn.outerHTML));
    const t=[...d.querySelectorAll(".toast")].map(x=>x.textContent.trim());
    ok(t.includes("git fetch: repository not found"),"and its reason is a toast: "+JSON.stringify(t));
  }
  console.log(fails?"FAILED":"ALL OK"); process.exit(fails?1:0);
})();
