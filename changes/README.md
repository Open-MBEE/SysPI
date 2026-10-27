# Changelog fragments

Each change adds one file here, `unreleased/<slug>.<section>.md`, where `<section>` is `added`,
`changed`, `fixed`, `removed`, `deprecated` or `security`. The body is the changelog list item(s)
only, starting with `- `. The release procedure folds the fragments into `CHANGELOG.md` and
deletes them, so concurrent pull requests never conflict on the changelog.
