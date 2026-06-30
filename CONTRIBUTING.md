# Contributing

PRs are welcome. Trivial fixes can go straight to a PR; for anything involved, open an issue to
discuss it first.

Before opening a PR, run `make ci` (lint, tests, golden-scenario eval) and make sure it is green.

## ownership for the changes

By submitting a PR you certify the [Developer Certificate of Origin](https://developercertificate.org/):
you have the right to submit the change and you are the author of record, however it was produced.
Sign off your commits with `git commit -s`.

## AI-assisted contributions

AI tools are fine to use, but using AI does not lower the bar – it shifts responsibility entirely
onto you.

- Read, understand, and test every line before submitting. If you cannot explain it, do not submit it.
- No autonomous-agent PRs – a human must review the output. Do not use AI to write the PR
  description or issue comments, those must reflect your own understanding.
- AI fails in specific ways – check for hallucinated APIs, fake/unmaintained dependencies, wrong
  edge-case handling, and style drift (`make lint`) before submitting.

NOTE: AI slop (unreviewed, unverified, or machine-generated PRs) will be closed without explanation.
