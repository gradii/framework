package grammars

import "github.com/gradii/framework/errors"

var (
	errJsonSelectorNotSupported      = errors.QueryJsonSelectorNotSupported
	errJsonContainsNotSupported      = errors.QueryJsonContainsNotSupported
	errJsonContainsKeyNotSupported   = errors.QueryJsonContainsKeyNotSupported
	errJsonLengthNotSupported        = errors.QueryJsonLengthNotSupported
	errJsonOverlapsNotSupported      = errors.QueryJsonOverlapsNotSupported
	errFullTextNotSupported          = errors.QueryFullTextNotSupported
	errUpsertNotSupported            = errors.QueryUpsertNotSupported
	errInsertIgnoreNotSupported      = errors.QueryInsertIgnoreNotSupported
	errLateralJoinNotSupported       = errors.QueryLateralJoinNotSupported
	errCaseSensitiveLikeNotSupported = errors.QueryCaseSensitiveLikeNotSupported
)

// removeLeadingBoolean strips the first leading connector. Mirrors Grammar::removeLeadingBoolean().
func removeLeadingBoolean(value string) string {
	return leadingBoolean.ReplaceAllString(value, "")
}

// RemoveLeadingBoolean strips the connector from a compiled condition group.
func RemoveLeadingBoolean(value string) string { return removeLeadingBoolean(value) }
