package db

import "iter"

// RowIterator releases its resources when the consumer stops or the query fails.
type RowIterator = iter.Seq2[Row, error]
