import re,sys
p='internal/prelude/prelude.bork'
s=open(p).read()
lines=s.split('\n')
fwd=re.compile(r'^fn \((\w+): ([^)]*?)\) (\w+)(\[[^\]]*\])?\((.*)\)(.*)\{ (\w+)\((.*)\) \}$')
names=[]
for i,l in enumerate(lines):
    m=fwd.match(l)
    if m and m.group(3)==m.group(7):
        names.append((i,m.group(3)))
def split_top(t):
    out=[];depth=0;cur=''
    for ch in t:
        if ch in '([{': depth+=1
        if ch in ')]}': depth-=1
        if ch==',' and depth==0:
            out.append(cur);cur=''
        else: cur+=ch
    if cur.strip(): out.append(cur)
    return [x.strip() for x in out]
def find_def(name):
    # top-level free def
    for i,l in enumerate(lines):
        if re.match(r'^fn '+name+r'[\[(]', l):
            # comment start
            j=i
            while j>0 and lines[j-1].startswith('//'): j-=1
            # end
            if l.rstrip().endswith('}') and l.count('{')==l.count('}'):
                k=i
            else:
                k=i
                while lines[k]!='}': k+=1
            return j,i,k
    raise Exception('no def '+name)
def convert_header(h):
    m=re.match(r'^fn (\w+)(\[[^\]]*\])?\(',h)
    name,tps=m.group(1),m.group(2) or ''
    rest=h[m.end():]
    depth=1;k=0
    while depth:
        if rest[k] in '([{': depth+=1
        if rest[k] in ')]}': depth-=1
        k+=1
    params=split_top(rest[:k-1])
    after=rest[k:]
    return 'fn ('+params[0]+') '+name+tps+'('+', '.join(params[1:])+')'+after
moves=[]
for (mi,name) in names:
    j,i,k=find_def(name)
    block=lines[j:k+1]
    block[i-j]=convert_header(block[i-j])
    moves.append((mi,(j,k),block))
# apply: replace method lines with blocks, delete defs
rm=set()
for mi,(j,k),block in moves:
    rm.update(range(j,k+1))
out=[]
repl={mi:block for mi,_,block in moves}
for idx,l in enumerate(lines):
    if idx in repl:
        out.extend(repl[idx]); continue
    if idx in rm: continue
    out.append(l)
s='\n'.join(out)
s=re.sub(r'\n{3,}','\n\n',s)
open(p,'w').write(s)
print(' '.join(n for _,n in names))
