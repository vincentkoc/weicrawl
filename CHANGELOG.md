# Changelog

## Unreleased

- Exclude recognized credential stores and tables before content import, and
  omit recognized legacy credential-table rows from all/raw exports without
  deleting retained history or changing explicit key workflows.

- Use published crawlkit v0.14.7 without a sibling checkout, with standalone CI.
  The minimum Go version is now 1.26.6.
- Reject overlapping decryption paths and require owned roots for automatic
  cleanup; preserve caller-owned roots with explicit retention or separate
  decryption, stage private outputs, and retain incomplete imports for recovery.
- Preserve archive files and SQLite sidecars when exporting, replace completed
  exports privately, and group Markdown by profile and chat identity.
- Open observational commands read-only without initializing or migrating
  missing or foreign archives; honor the explicit database path during init.
- Clean abandoned owned source-copy stages, honor copy cancellation, and
  propagate per-database import failures and last-successful sync freshness.
  Cleanup failures remain partial in persisted run history.
- Keep replacement and helper-produced key manifests private.
