// Every form control of the admin pages has an accessible name: a wrapping <label>, a label[for], aria-label or
// aria-labelledby. Dialog content (<template>) and the row templates of the pairs lists are checked too.
// Usage: node labels.js <dir with the rendered pages>
const {JSDOM}=require("jsdom");const fs=require("fs");
const dir=process.argv[2];
let bad=0, total=0;
function named(el, root){
  if((el.getAttribute("aria-label")||"").trim()) return true;
  if(el.getAttribute("aria-labelledby")) return true;
  if(el.id && root.querySelector('label[for="'+el.id+'"]')) return true;
  const lab=el.closest("label");
  if(lab){ const c=lab.cloneNode(true); c.querySelectorAll("input,select,textarea,button").forEach(x=>x.remove()); if(c.textContent.trim()) return true; }
  return false;
}
function scan(root, where){
  root.querySelectorAll('input:not([type=hidden]):not([type=submit]):not([type=button]), select, textarea').forEach(el=>{
    total++;
    if(!named(el, root)){ bad++; console.log("FAIL", where, "<"+el.tagName.toLowerCase()+' name="'+(el.getAttribute("name")||"")+'"> has no name'); }
  });
  root.querySelectorAll("template").forEach(t=>scan(t.content, where+" > template#"+(t.id||t.getAttribute("data-pairs-template")!==null&&"row")));
}
for(const f of fs.readdirSync(dir).filter(x=>x.endsWith(".html"))){
  const dom=new JSDOM(fs.readFileSync(dir+"/"+f,"utf8"));
  scan(dom.window.document, f);
}
console.log(bad?("FAILED "+bad+" of "+total):("ALL OK "+total+" controls"));
process.exit(bad?1:0);
