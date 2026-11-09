import re,sys
log,pkg=sys.argv[1],sys.argv[2]
lines=open(log).read().splitlines()
buf=[];out=[]
for l in lines:
    if l.startswith(("PASS \"","SKIP \"")): buf=[]; continue
    if l.startswith("FAIL \""):
        out.append(buf); buf=[]; continue
    if l.startswith("-----------"): continue
    buf.append(l)
print(f"{len(out)} survivors in {log.split('/')[-1]}")
for i,b in enumerate(out,1):
    f=next((re.sub(r'.*internal/','internal/',x[4:].split('\t')[0]) for x in b if x.startswith('--- ')),'?')
    h=next((x for x in b if x.startswith('@@')),'')
    ch=[x for x in b if x[:1] in '+-' and not x.startswith(('---','+++'))]
    m=re.search(r'\+(\d+)',h)
    print(f"{i:2d} {f}:{m.group(1) if m else '?'}  "+" | ".join(c.strip()[:70] for c in ch[:3]))
