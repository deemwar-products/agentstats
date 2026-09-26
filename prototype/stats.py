import subprocess,glob,os,re,json,collections,sys
S=sys.argv[1]
repos=glob.glob(os.path.expanduser('~/data-exporter-local/raw/github-code/*/*/*.git'))
gh=len(repos); repos+=glob.glob(S+'/gitlab/*.git')
ME={'muthuishere@gmail.com','muthuisheremobile@gmail.com','admin@deemwar.com','io@deemwar.com','oss@deemwar.com'}
LANG={'rb':'Ruby','erb':'Ruby','rake':'Ruby','js':'JavaScript','jsx':'JavaScript','mjs':'JavaScript','cjs':'JavaScript','ts':'TypeScript','tsx':'TypeScript','java':'Java','kt':'Kotlin','go':'Go','py':'Python','rs':'Rust','swift':'Swift','cs':'C#','c':'C','h':'C','cpp':'C++','cc':'C++','php':'PHP','dart':'Dart','scala':'Scala','groovy':'Groovy','gradle':'Groovy','sh':'Shell','bash':'Shell','zsh':'Shell','ps1':'PowerShell','html':'HTML','htm':'HTML','css':'CSS','scss':'CSS','sass':'CSS','less':'CSS','vue':'Vue','svelte':'Svelte','sql':'SQL','md':'Markdown','mdx':'Markdown','yml':'YAML','yaml':'YAML','lua':'Lua','ex':'Elixir','exs':'Elixir','clj':'Clojure','hs':'Haskell','r':'R','m':'Objective-C','vim':'Vim','tf':'Terraform','proto':'Protobuf','toml':'TOML','json':'JSON','xml':'XML','ipynb':'Notebook'}
SKIP=re.compile(r'(^|/)(node_modules|vendor|dist|build|target|\.next|coverage|bower_components|out|__pycache__|Pods|\.gradle|venv|\.venv)/|(package-lock\.json|yarn\.lock|pnpm-lock\.yaml|go\.sum|Gemfile\.lock|Cargo\.lock|poetry\.lock|composer\.lock|bun\.lockb?)$|\.min\.(js|css)$|\.(map|snap|svg|lock|csv|tsv|txt|log|pb\.go)$|_pb2\.py$|\.g\.dart$|generated|(^|/)[a-z]*aot/|vendor')
NOTCODE={'JSON','XML','Markdown','YAML','TOML','Notebook'}
DROP=[]; seen=set(); RM=collections.Counter(); C=collections.defaultdict(lambda: collections.Counter())
yc=collections.Counter(); yl=collections.Counter(); ml=collections.Counter(); lang=collections.defaultdict(collections.Counter)
first=None; big=[]
for r in repos:
    out=subprocess.run(['git','--git-dir',r,'log','--all','--no-merges','--numstat','--format=@@%H|%ae|%ad','--date=format:%Y-%m'],capture_output=True,text=True,errors='ignore').stdout
    cur=None; buf=[]; nf=0; H=None
    def flush():
        global buf,nf
        if cur and buf:
            tot=sum(a for _,_,a in buf)
            if nf>300 or tot>25000: DROP.append((cur,r.split('/')[-1],H[:8],nf,tot))
            else:
                for m,L,a in buf:
                    y=m[:4]; yl[y]+=a; ml[m]+=a; lang[y][L]+=a; RM[(m,r.split('/')[-1])]+=a
        buf=[]; nf=0
    for line in out.splitlines():
        if line.startswith('@@'):
            flush()
            h,e,d=line[2:].split('|',2)
            cur=None
            if e.lower() in ME and h not in seen:
                seen.add(h); H=h; cur=d; y=d[:4]; yc[y]+=1; cl=0
        elif cur and line:
            p=line.split('\t')
            if len(p)<3 or p[0]=='-': continue
            f=p[2]; nf+=1
            if '=>' in f: continue
            if SKIP.search(f): continue
            ext=f.rsplit('.',1)[-1].lower() if '.' in f.split('/')[-1] else ''
            L=LANG.get(ext)
            if not L or L in NOTCODE: continue
            a=int(p[0])
            if a>5000: continue  # bulk imports / pasted blobs
            buf.append((cur,L,a))
    flush()
json.dump([[k[0],k[1],v] for k,v in RM.most_common(400)],open(S+'/repomonth.json','w'))
res={'dropped':sorted(DROP,key=lambda x:-x[4]),'repos_github':gh,'repos_gitlab':len(repos)-gh,'commits_by_year':dict(sorted(yc.items())),'lines_by_year':dict(sorted(yl.items())),'top_months':ml.most_common(6),'lang_by_year':{y:dict(c.most_common()) for y,c in sorted(lang.items())}}
json.dump(res,open(S+'/stats.json','w'),indent=1)
print(json.dumps({k:res[k] for k in ['repos_github','repos_gitlab','commits_by_year','lines_by_year','top_months']},indent=0))
