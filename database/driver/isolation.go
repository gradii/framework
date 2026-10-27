package driver

import (
	"database/sql"
	"strings"

	"github.com/gradii/framework/contracts/config"
	"github.com/gradii/framework/errors"
)

// TransactionOptions returns the options a transaction on this connection is opened with, or nil
// when the connection asks for the driver's own default.
//
// The level comes from database.connections.<name>.isolation_level and is named the way Laravel
// names it — "read committed", "repeatable read", "serializable", "read uncommitted" — so an
// application migrating from Laravel keeps its spelling. Case and the separator are not
// significant: "READ-COMMITTED" and "read_committed" both mean the same level.
//
// A value that names no level is reported rather than ignored: silently opening a transaction at a
// weaker level than the application asked for is the kind of failure that only shows up under
// concurrency.
func TransactionOptions(config config.Config, connection string) (*sql.TxOptions, error) {
	if config == nil || connection == "" {
		return nil, nil
	}

	raw, ok := config.Get("database.connections." + connection + ".isolation_level").(string)
	if !ok {
		return nil, nil
	}

	switch normalizeIsolationLevel(raw) {
	case "":
		return nil, nil
	case "default":
		return &sql.TxOptions{Isolation: sql.LevelDefault}, nil
	case "readuncommitted":
		return &sql.TxOptions{Isolation: sql.LevelReadUncommitted}, nil
	case "readcommitted":
		return &sql.TxOptions{Isolation: sql.LevelReadCommitted}, nil
	case "repeatableread":
		return &sql.TxOptions{Isolation: sql.LevelRepeatableRead}, nil
	case "serializable":
		return &sql.TxOptions{Isolation: sql.LevelSerializable}, nil
	default:
		return nil, errors.DatabaseIsolationLevelInvalid.Args(raw)
	}
}

// normalizeIsolationLevel folds the spellings of a level into one token: lowercased, with spaces,
// hyphens and underscores removed.
func normalizeIsolationLevel(level string) string {
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(strings.ToLower(strings.TrimSpace(level)))
}
