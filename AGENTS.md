# Working on Merino

Read [CONTRIBUTING.md](CONTRIBUTING.md) for the architecture, the dev loop,
and the traps. This file is the short list of rules that are easy to break by
being helpful.

## Two standing rules

**1. Never push to `main`.** It is protected server-side, so an attempt will
be rejected — but do not try. Branch, push the branch, open a PR:

```bash
git checkout -b fix/some-thing
git push -u origin fix/some-thing
gh pr create --fill
```

Squash or rebase merges only. Write the PR title as a Conventional Commit,
because squash uses it as the commit subject and `git-cliff` parses it into
the changelog.

**2. Never bump the version without the maintainer explicitly agreeing.**
Not as cleanup, not "while I'm here", not because a change looks releasable.
A version bump decides what a release *means* and when users are asked to
take it. Land the work; propose the release separately and wait for a yes.

This covers `frontend/package.json`, its lockfile, both `Info.plist` files,
any tag, and `CHANGELOG.md` release headings.

## Before you hand work back

- `just gate` green.
- Say what you ran and what you observed. "Tests pass" is not verification.
- Say what you could not verify. An honest gap beats a confident claim that
  does not hold — this repo has been bitten by the second one.
