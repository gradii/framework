package gorm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandomOrderFunction(t *testing.T) {
	assert.Equal(t, "RAND()", randomOrderFunction("mysql"))
	assert.Equal(t, "NEWID()", randomOrderFunction("sqlserver"))
	assert.Equal(t, "RANDOM()", randomOrderFunction("postgres"))
	assert.Equal(t, "RANDOM()", randomOrderFunction("sqlite"))
}
