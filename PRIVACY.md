# Privacy

macdoc runs on your Mac and is designed to keep your data there.

## What macdoc does not do

- It sends nothing to any server. The macdoc code makes no network calls.
- It has no telemetry, analytics, crash reporting or accounts.
- It doesn't read the contents of your files. It records names, sizes and
  dates, and reads a file only where a recipe needs a small known file (for
  example a version number).

## What it stores, and where

macdoc keeps its own state in `~/Library/Application Support/macdoc/`:

- a scan database (sizes and paths of folders, not file contents);
- sealed cleanup plans;
- the audit log of every change it made;
- the settings file (`config.json`), which you can edit with `macdoc config`.

Nothing leaves that folder unless you copy it yourself.

## What you should know

macdoc runs some tools you already have, such as `brew`, `npm`, `docker` or
`xcrun`, to read their state or to clean their caches. Those tools may use the
network for their own purposes, for example to update an index. That is their
behaviour, not macdoc's, and macdoc can't control it.

When you connect macdoc to Claude through `macdoc mcp`, Claude can read the
scan summaries and propose cleanup plans that macdoc returns to you. What
Claude does with that information is governed by Claude's own terms.
