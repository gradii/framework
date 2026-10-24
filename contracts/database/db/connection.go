package db

// WriteConnection pins connection-local SQL state until Close, including nested transactions.
type WriteConnection interface {
	Tx
	Close() error
}
