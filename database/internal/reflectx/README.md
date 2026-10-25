# Database field mapping

Copied from `github.com/upper/db/v4/internal/reflectx` at commit `6ccd8f5`.
The original Jason Moiron MIT license is retained in `LICENSE`, along with the
upstream mapper tests.

Each mapper caches column names, tag options, and field index paths by
`reflect.Type`. DB and ORM use separate mapper instances for `db` and `gorm`.
Factories use a separate mapper for Go field names, including nested maps and
collections in definitions and overrides. Cached metadata contains no destination
instances or query state.

Goravel adaptations:

- Normalize tags before selecting a column name, so ORM annotations such as
  `column:full_name;size:100` work and non-column directives keep the default name.
- Preserve database scalar types, including `sql.Scanner`, `driver.Valuer`, and
  timestamps, instead of descending into their implementation fields.
- Stop recursive field paths, traverse multiple pointer levels, and retain
  exported fields reached through private embeds. Unwritable fields report errors.
- Use `any` and the framework's named errors.

`../scan` adapts upper-db's SQL result mapping to both `sql.Rows.Scan` and
materialized cursor rows, preserving the framework's scalar, JSON, timestamp,
and custom scanner conversions. Its upper-db license is retained separately.
