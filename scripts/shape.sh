#!/usr/bin/env bash
# scanshape: print the SHAPE of macdoc's latest complete scan, with no paths.
#
# The numbers say how many nodes a real scan keeps, how deep they are and how
# big, which decides whether the performance work in plan.md (H1, H3, H4, H6)
# matters on a real Mac. It is safe to paste into an issue: it prints counts
# and sizes only, never a path or a name.
#
#   scripts/shape.sh                 # uses the default state database
#   scripts/shape.sh path/to/state.db
#
# It works on a COPY of the database (and its WAL files) in a temporary
# folder, so it never writes to macdoc's state.
#
# To also record the cost of a fresh scan, run it under time first:
#   /usr/bin/time -l macdoc doctor scan > /dev/null
# and add the "maximum resident set size" and "real" lines to what you share.
set -euo pipefail

db=${1:-"$HOME/Library/Application Support/macdoc/state.db"}
if [ ! -f "$db" ]; then
	echo "scanshape: no database at $db (run 'macdoc doctor scan' first)" >&2
	exit 1
fi
command -v sqlite3 >/dev/null || { echo "scanshape: sqlite3 is required" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp "$db" "$tmp/state.db"
for ext in -wal -shm; do
	[ -f "$db$ext" ] && cp "$db$ext" "$tmp/state.db$ext"
done

q() { sqlite3 -batch -noheader "$tmp/state.db" "$@"; }

id=$(q "SELECT id FROM scans WHERE status = 'complete' ORDER BY started_at DESC LIMIT 1;")
if [ -z "$id" ]; then
	echo "scanshape: no complete scan in the database" >&2
	exit 1
fi

echo "macdoc scan shape (no paths)"
echo "database size (MB):   $(( $(stat -f %z "$db") / 1048576 ))"
echo "scans stored:         $(q "SELECT COUNT(*) FROM scans;")"
echo "macdoc version:       $(q "SELECT macdoc_version FROM scans WHERE id = '$id';")"
echo "scan duration (s):    $(q "SELECT (finished_at - started_at) / 1000000000.0 FROM scans WHERE id = '$id';")"
echo "files measured:       $(q "SELECT total_files FROM scans WHERE id = '$id';")"
echo "bytes measured (GB):  $(q "SELECT ROUND(total_bytes / 1e9, 1) FROM scans WHERE id = '$id';")"
echo "unmeasured paths:     $(q "SELECT unmeasured_count FROM scans WHERE id = '$id';")"
echo "kept nodes:           $(q "SELECT COUNT(*) FROM nodes WHERE scan_id = '$id';")"
echo "  directories:        $(q "SELECT COUNT(*) FROM nodes WHERE scan_id = '$id' AND kind = 'dir';")"
echo "  files:              $(q "SELECT COUNT(*) FROM nodes WHERE scan_id = '$id' AND kind = 'file';")"
echo
echo "kept nodes by depth below /"
q ".mode column" "SELECT depth, COUNT(*) AS nodes FROM nodes WHERE scan_id = '$id' GROUP BY depth ORDER BY depth;" | sed 's/^/  /'
echo
echo "kept nodes by size"
q "SELECT CASE
     WHEN bytes < 1000000 THEN '1  under 1 MB'
     WHEN bytes < 10000000 THEN '2  1-10 MB'
     WHEN bytes < 100000000 THEN '3  10-100 MB'
     WHEN bytes < 1000000000 THEN '4  100 MB-1 GB'
     ELSE '5  1 GB or more' END AS bucket, COUNT(*)
   FROM nodes WHERE scan_id = '$id' GROUP BY bucket ORDER BY bucket;" | sed 's/|/  nodes: /; s/^/  /'
echo
# rtrim(p, replace(p, '/', '')) is p up to and including its last slash, so
# grouping by it groups nodes by parent folder without selecting any path.
echo "most kept children of one folder: $(q "SELECT COALESCE(MAX(c), 0) FROM (
	SELECT COUNT(*) AS c FROM nodes WHERE scan_id = '$id'
	GROUP BY rtrim(path_fold, replace(path_fold, '/', '')));")"
