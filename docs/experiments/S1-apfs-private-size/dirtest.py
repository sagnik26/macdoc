import os, subprocess, sys, time, shutil
mnt, attrs = sys.argv[1], sys.argv[2]
parent = os.path.dirname(mnt)
assert os.stat(mnt).st_dev != os.stat(parent).st_dev, "not a separate device"
assert "APFS" in subprocess.run(["diskutil","info",mnt],capture_output=True,text=True).stdout
MB = 1 << 20
def free():
    os.sync(); time.sleep(1.5); os.sync(); v = os.statvfs(mnt); return v.f_bavail * v.f_frsize
def rnd(p, mb): subprocess.run(["dd","if=/dev/urandom",f"of={p}","bs=1m",f"count={mb}"],check=True,capture_output=True)
def clone(a, b): subprocess.run(["cp","-c",a,b],check=True)
def info(paths):
    out = subprocess.run([attrs],input="\n".join(paths)+"\n",capture_output=True,text=True).stdout
    r = {}
    for l in out.strip().splitlines():
        f = l.split("\t")
        if len(f) >= 6: r[f[0]] = (int(f[1]), int(f[2]), int(f[3]))
    return r
p = lambda *a: os.path.join(mnt, *a)
def tree_sums(d):
    files = [os.path.join(r, f) for r, _, fs in os.walk(d) for f in fs]
    a = info(files)
    return sum(v[0] for v in a.values()), sum(max(v[2], 0) for v in a.values())

def scenario(name, build):
    d = p(name); os.mkdir(d); build(d)
    blocks, priv = tree_sums(d)
    before = free(); shutil.rmtree(d); freed = free() - before
    print(f"{name:52s} freed {freed/MB:6.1f}M | st_blocks {blocks/MB:6.1f}M | sum of private sizes {priv/MB:6.1f}M")

os.makedirs(p("outside"))
rnd(p("outside","x.bin"), 32)

def s1(d):                                   # unique file + a clone of a file that stays outside
    os.makedirs(p("s1","sub")); rnd(p("s1","sub","u.bin"), 32); clone(p("outside","x.bin"), p("s1","c1.bin"))
scenario("s1", s1)
rnd(p("outside","y.bin"), 32)
def s2(d):                                   # a clone group that lives entirely inside the directory
    rnd(p("s2","g.bin"), 32); clone(p("s2","g.bin"), p("s2","g1.bin")); clone(p("s2","g.bin"), p("s2","g2.bin"))
scenario("s2", s2)
def s3(d):                                   # clones of one outside file, several inside the directory
    clone(p("outside","y.bin"), p("s3","a.bin")); clone(p("outside","y.bin"), p("s3","b.bin"))
scenario("s3", s3)
def s4(d):                                   # the whole thing: unique + group inside + clone of outside
    rnd(p("s4","u.bin"), 16); rnd(p("s4","g.bin"), 16); clone(p("s4","g.bin"), p("s4","g1.bin")); clone(p("outside","x.bin"), p("s4","c.bin"))
scenario("s4", s4)
