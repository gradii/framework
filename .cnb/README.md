# CNB test modules

The CNB pipelines run the root modules of the framework and all four drivers in
an explicit Go workspace. The integration pipeline adds `framework/tests` to
that workspace and runs its full suite, including the ported database-fixture
lifecycle cases. Driver cases run against the new runtime APIs. No old GORM
implementation or compatibility module is retained.

The current repository always uses the checkout supplied by CNB. For each other
repository, configure these CNB pipeline environment variables:

| Repository | Clone URL | Compatible branch, tag, or commit |
| --- | --- | --- |
| framework | `GRADII_FRAMEWORK_URL` | `GRADII_FRAMEWORK_REF` |
| mysql | `GRADII_MYSQL_URL` | `GRADII_MYSQL_REF` |
| postgres | `GRADII_POSTGRES_URL` | `GRADII_POSTGRES_REF` |
| sqlite | `GRADII_SQLITE_URL` | `GRADII_SQLITE_REF` |
| sqlserver | `GRADII_SQLSERVER_URL` | `GRADII_SQLSERVER_REF` |

The framework URL defaults to its existing remote,
`https://cnb.cool/gradii/framework`. Driver URLs and all companion refs must be
configured explicitly. The candidate `cnb.cool/gradii/{driver}` repositories
were unavailable when these pipelines were added, and the original
`goravel/{driver}` repositories do not contain the new module namespaces. Use
refs that contain the coordinated migration; pin commit IDs for reproducible
runs. Each checkout is validated against its expected `github.com/gradii/...`
module name before testing. Authentication uses the runner's Git configuration;
URLs should not contain credentials.

`bash .cnb/ci.sh unit` tests all five root modules.
`bash .cnb/ci.sh database` tests the framework database packages.
`bash .cnb/ci.sh integration` tests `framework/tests`.
Upstream driver cases are ported to the new runtime tests; fixture lifecycle
cases run in `framework/tests`. No archived GORM implementation is tested.

The integration suite uses the existing CNB `docker` service on the `cnb:arch:amd64` runner. Their tests start
their own database containers and connect through the shared daemon's published
loopback ports. The script checks Docker availability and fails if the service
is unavailable. SQLite tests use local database files. Every suite
executes normally, with `-count=1`; none is replaced by a compilation-only check.

The helper creates an isolated temporary workspace and removes it on exit. It
does not depend on a developer's ignored `go.work`, rewrite module declarations,
or run `go mod tidy`. Each repository carries the same `ci.sh` so its CNB
checkout can prepare the workspace without fetching an unpinned helper script.

## Local development before publication

The Gradii modules do not need to be published to develop or test them together.
From the framework checkout, create an ignored local workspace using the sibling
checkouts and the independent integration module:

```sh
go work init . ./tests ../mysql ../postgres ../sqlite ../sqlserver
go work edit -replace=github.com/gradii/framework@v1.18.0=.
go work edit -replace=github.com/gradii/mysql@v1.18.0=../mysql
go work edit -replace=github.com/gradii/postgres@v1.18.0=../postgres
go work edit -replace=github.com/gradii/sqlite@v1.18.0=../sqlite
go work edit -replace=github.com/gradii/sqlserver@v1.18.0=../sqlserver
go test ./...
(cd tests && go test ./...)
```

If a workspace already exists, use `go work use` instead of `go work init`.
Version-specific workspace replacements prevent Go from looking up unreleased
module versions while retaining portable module declarations. CNB generates
these replacements from the checked-out modules’ actual requirements.

The current implementation retains all 468 original framework test files and
their original test/benchmark/example functions. The driver test cases have been ported to the new APIs. Runtime tests use the
new dialects, and no old GORM source or compatibility module is retained.

The new driver runtime packages contain no GORM dependencies. Fully obfuscated
ORM field/relationship access is a separate mapping concern; this refactor does
not restore automatic table naming from Go type names.
