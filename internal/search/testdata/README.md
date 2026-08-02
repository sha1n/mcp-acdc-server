# Golden query corpus

Fixtures for `golden_test.go`. They are ordinary Markdown so that the realism
that makes them useful stays reviewable; Go string literals would not.

`corpus/` reproduces repository documentation: **no frontmatter anywhere**,
headings of uneven depth, terminology recurring across files, and a README whose
sections restate what a `docs/` page covers at length.

`curated/` reproduces an authored catalog: frontmatter with `name`,
`description`, and `keywords`.

## Load-bearing properties

Several golden cases isolate one scoring field against another. Editing the
prose is fine; these specific properties are not decoration, and breaking one
turns a measurement into a tautology that still passes:

- `corpus/` must never gain frontmatter. `keywords` being unreachable there is
  the finding the harness exists to pin.
- `docs/rate-limiting/quotas.md` must not write "rate" or "limiting" in its body
  or headings. Only its path can retrieve it.
- `docs/internals/indexing.md` carries "checksums" as a heading and never in its
  body; `docs/reference/cli.md` carries it only in a body, three times. The pair
  isolates `heading_path` against `content`.
- `curated/docs/observability.md` declares `dashboards` as a keyword and never
  writes the word; `corpus/docs/troubleshooting.md` mentions one dashboard in
  passing. The pair isolates `keywords` against `content`.

Adding documents is safe. Removing or renaming one of the above needs the
matching golden case updated in the same change.
