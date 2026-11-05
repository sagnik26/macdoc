# Security policy

macdoc deletes files, so a safety bug can lose someone's data. We take reports
seriously.

## Reporting a vulnerability

Please report it privately through GitHub's "Report a vulnerability" button on
the repository's Security tab, or by contacting the maintainer (sagnik26) on
GitHub. Don't open a public issue.

Include:
- the macdoc version (`macdoc version`);
- the steps to reproduce, with paths replaced by placeholders;
- what you expected and what happened.

## Scope

In scope: anything that lets macdoc touch a protected path, act on a path the
user didn't approve, run a command outside the recipes, or skip the typed
confirmation.

Out of scope: the external tools macdoc runs (for example `brew` or `docker`)
and their own bugs; report those to their maintainers.
