"""S1 oracle: compare size predictions with the kernel's free-space delta.
Runs ONLY inside a throwaway APFS disk image mounted at argv[1]."""
import os, subprocess, sys, time, json

mnt, attrs = sys.argv[1], sys.argv[2]
parent = os.path.dirname(mnt)
# Safety: the mount must be its own filesystem, never the scratch dir's.
assert os.stat(mnt).st_dev != os.stat(parent).st_dev, "mount point is not a separate device"
fs = subprocess.run(["diskutil", "info", mnt], capture_output=True, text=True).stdout
assert "APFS" in fs, "not APFS"
MB = 1 << 20

def free():
    os.sync(); time.sleep(1.5); os.sync()
    v = os.statvfs(mnt)
    return v.f_bavail * v.f_frsize

def rnd(path, mb):
    subprocess.run(["dd", "if=/dev/urandom", f"of={path}", "bs=1m", f"count={mb}"], check=True, capture_output=True)

def clone(src, dst):
    subprocess.run(["cp", "-c", src, dst], check=True)

def info(paths):
    out = subprocess.run([attrs], input="\n".join(paths) + "\n", capture_output=True, text=True).stdout
    res = {}
    for line in out.strip().splitlines():
        f = line.split("\t")
        res[f[0]] = dict(blocks=int(f[1]), alloc=int(f[2]), priv=int(f[3]), cid=int(f[4]), rc=int(f[5]))
    return res

p = lambda n: os.path.join(mnt, n)
rows = []

def delete(label, names, group=False):
    paths = [p(n) for n in names]
    a = info(paths)
    pred_blocks = sum(a[x]["blocks"] for x in paths)
    pred_priv = sum(max(a[x]["priv"], 0) for x in paths)
    # clone-group aware: count a clone group's allocation once, only if every member is in the set
    groups = {}
    for x in paths:
        groups.setdefault(a[x]["cid"], []).append(x)
    pred_group = 0
    for cid, xs in groups.items():
        if len(xs) == a[xs[0]]["rc"] or a[xs[0]]["rc"] <= 1:
            pred_group += a[xs[0]]["alloc"] if len(xs) == 1 else a[xs[0]]["alloc"]
        else:
            pred_group += sum(max(a[x]["priv"], 0) for x in xs)
    before = free()
    for x in paths:
        os.remove(x)
    freed = free() - before
    rows.append(dict(label=label, freed=freed, st_blocks=pred_blocks, private=pred_priv, group=pred_group,
                     detail={os.path.basename(k): v for k, v in a.items()}))

# fixtures
rnd(p("u.bin"), 64)
rnd(p("a.bin"), 64); clone(p("a.bin"), p("a1.bin")); clone(p("a.bin"), p("a2.bin"))
rnd(p("h.bin"), 64); os.link(p("h.bin"), p("h2.bin"))
with open(p("sp.bin"), "wb") as f:
    f.write(os.urandom(4 * MB)); f.truncate(200 * MB)
rnd(p("d.bin"), 64); clone(p("d.bin"), p("d1.bin"))
with open(p("d1.bin"), "r+b") as f:
    f.seek(20 * MB); f.write(os.urandom(8 * MB))
rnd(p("b.bin"), 64); clone(p("b.bin"), p("b1.bin")); clone(p("b.bin"), p("b2.bin"))

delete("1 unique 64MB file", ["u.bin"])
delete("2 clone original (2 clones remain)", ["a.bin"])
delete("3 one of 2 remaining clones", ["a1.bin"])
delete("4 last clone", ["a2.bin"])
delete("5 hardlink 1 of 2", ["h.bin"])
delete("6 last hardlink", ["h2.bin"])
delete("7 sparse 200MB (4MB written)", ["sp.bin"])
delete("8 diverged clone (8MB rewritten)", ["d1.bin"])
delete("9 original after diverged clone gone", ["d.bin"])
delete("10 whole clone group at once (b,b1,b2)", ["b.bin", "b1.bin", "b2.bin"])

print(f"{'scenario':45s} {'freed':>9s} {'st_blocks':>10s} {'private':>9s} {'group':>9s}")
for r in rows:
    print(f"{r['label']:45s} {r['freed']/MB:8.1f}M {r['st_blocks']/MB:9.1f}M {r['private']/MB:8.1f}M {r['group']/MB:8.1f}M")
json.dump(rows, open(os.path.join(parent, "oracle_result.json"), "w"), indent=1)
