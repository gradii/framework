package grammars

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRawSQLUsesDefaultEscapingAndPreservesLiteralQuestionMarks(t *testing.T) {
	grammar := NewBaseGrammar("")
	assert.Equal(t, `select 'O''Reilly', '?', ??, NULL, ?`,
		grammar.SubstituteBindingsIntoRawSQL(`select ?, '?', ??, ?, ?`, []any{"O'Reilly", nil}))
}

func TestCustomOperatorsCanBeRegisteredWhileCompiling(t *testing.T) {
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				CustomOperators([]any{"@@@", "@@>"})
				_ = NewPostgresGrammar("").GetOperators()
			}
		})
	}
	workers.Wait()
	operators := NewPostgresGrammar("").GetOperators()
	assert.Contains(t, operators, "@@@")
	assert.Contains(t, operators, "@@>")
}
