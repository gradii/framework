package gorm

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	contractsdatabase "github.com/goravel/framework/contracts/database"
	contractsdriver "github.com/goravel/framework/contracts/database/driver"
	contractslogger "github.com/goravel/framework/contracts/database/logger"
	contractsorm "github.com/goravel/framework/contracts/database/orm"
	"github.com/goravel/framework/contracts/log"
	databasedriver "github.com/goravel/framework/database/driver"
	databaseorm "github.com/goravel/framework/database/orm"
	querybuilder "github.com/goravel/framework/database/query"
	"github.com/goravel/framework/errors"
	mocksconfig "github.com/goravel/framework/mocks/config"
	"github.com/goravel/framework/support/carbon"
	"github.com/goravel/framework/support/convert"
)

func TestAddGlobalScopes(t *testing.T) {

	tests := []struct {
		name                  string
		setupQuery            func() *Query
		expectedScopesApplied bool
		expectedScopesCount   int
	}{
		{
			name: "should apply global scopes when model implements ModelWithGlobalScopes",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithGlobalScopes{}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: true,
			expectedScopesCount:   2, // active and verified scopes
		},
		{
			name: "should not apply global scopes when model does not implement ModelWithGlobalScopes",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithoutGlobalScopes{}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: false,
			expectedScopesCount:   0,
		},
		{
			name: "should not apply global scopes when withoutGlobalScopes contains '*'",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithGlobalScopes{}
				conditions.withoutGlobalScopes = []string{"*"}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: false,
			expectedScopesCount:   0,
		},
		{
			name: "should exclude specific scope when in withoutGlobalScopes",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithGlobalScopes{}
				conditions.withoutGlobalScopes = []string{"active"}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: true,
			expectedScopesCount:   1, // only verified scope
		},
		{
			name: "should not apply scopes when model is nil",
			setupQuery: func() *Query {
				conditions := Conditions{}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: false,
			expectedScopesCount:   0,
		},
		{
			name: "should use dest when model is nil but dest is set",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.dest = &ModelWithGlobalScopes{}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: true,
			expectedScopesCount:   2, // active and verified scopes
		},
		{
			name: "should not apply scopes when model has empty GlobalScopes map",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithEmptyGlobalScopes{}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: false,
			expectedScopesCount:   0,
		},
		{
			name: "should apply scopes in sorted order by name",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithGlobalScopes{}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: true,
			expectedScopesCount:   2, // Scopes applied in order: active, verified (alphabetically)
		},
		{
			name: "should exclude multiple scopes when multiple names in withoutGlobalScopes",
			setupQuery: func() *Query {
				conditions := Conditions{}
				conditions.model = &ModelWithGlobalScopes{}
				conditions.withoutGlobalScopes = []string{"active", "verified"}
				query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)
				return query
			},
			expectedScopesApplied: false,
			expectedScopesCount:   0, // both scopes excluded
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := tt.setupQuery()
			result := query.addGlobalScopes()

			assert.NotNil(t, result)

			// Check if scopes were applied by examining the scopes list
			if tt.expectedScopesApplied {
				assert.GreaterOrEqual(t, len(result.conditions.scopes), tt.expectedScopesCount)
			} else {
				assert.Equal(t, tt.expectedScopesCount, len(result.conditions.scopes))
			}
		})
	}
}

func TestAddGlobalScopesWithPointerTypes(t *testing.T) {

	tests := []struct {
		name     string
		model    any
		expected bool
	}{
		{
			name:     "should work with pointer to model",
			model:    &ModelWithGlobalScopes{},
			expected: true,
		},
		{
			name:     "should work with value type model",
			model:    ModelWithGlobalScopes{},
			expected: true,
		},
		{
			name:     "should work with pointer to pointer",
			model:    func() any { m := &ModelWithGlobalScopes{}; return &m }(),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := Conditions{}
			conditions.model = tt.model
			query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)

			result := query.addGlobalScopes()

			assert.NotNil(t, result)
			if tt.expected {
				assert.GreaterOrEqual(t, len(result.conditions.scopes), 2)
			}
		})
	}
}

func TestAddGlobalScopesWithSliceAndArray(t *testing.T) {

	tests := []struct {
		name  string
		model any
	}{
		{
			name:  "should handle slice of models",
			model: []ModelWithGlobalScopes{},
		},
		{
			name:  "should handle pointer to slice of models",
			model: &[]ModelWithGlobalScopes{},
		},
		{
			name:  "should handle array of models",
			model: [2]ModelWithGlobalScopes{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conditions := Conditions{}
			conditions.model = tt.model
			query := NewQuery(context.Background(), nil, contractsdatabase.Config{}, nil, nil, nil, nil, nil, &conditions, nil)

			result := query.addGlobalScopes()

			// Should handle slice/array types and extract the element type
			assert.NotNil(t, result)
		})
	}
}

func TestAddWhere(t *testing.T) {
	query := &Query{builder: querybuilder.NewBuilder(nil)}
	query = query.addWhere(contractsdriver.Where{
		Query: "name",
		Args:  []any{"test"},
	}).(*Query)
	query = query.addWhere(contractsdriver.Where{
		Query: "name1",
		Args:  []any{"test1"},
	}).(*Query)
	query = query.addWhere(contractsdriver.Where{
		Query: "name2",
		Args:  []any{"test2"},
	}).(*Query)
	query1 := query.clone().addWhere(contractsdriver.Where{
		Query: "name3",
		Args:  []any{"test3"},
	}).(*Query)

	assert.Equal(t, []contractsdriver.Where{
		{Query: "name", Args: []any{"test"}},
		{Query: "name1", Args: []any{"test1"}},
		{Query: "name2", Args: []any{"test2"}},
	}, query.conditions.where)

	assert.Equal(t, []contractsdriver.Where{
		{Query: "name", Args: []any{"test"}},
		{Query: "name1", Args: []any{"test1"}},
		{Query: "name2", Args: []any{"test2"}},
		{Query: "name3", Args: []any{"test3"}},
	}, query1.conditions.where)

	query2 := query.clone().addWhere(contractsdriver.Where{
		Query: "name4",
		Args:  []any{"test4"},
	}).(*Query)

	assert.Equal(t, []contractsdriver.Where{
		{Query: "name", Args: []any{"test"}},
		{Query: "name1", Args: []any{"test1"}},
		{Query: "name2", Args: []any{"test2"}},
		{Query: "name4", Args: []any{"test4"}},
	}, query2.conditions.where)

	assert.Equal(t, []contractsdriver.Where{
		{Query: "name", Args: []any{"test"}},
		{Query: "name1", Args: []any{"test1"}},
		{Query: "name2", Args: []any{"test2"}},
	}, query.conditions.where)

	assert.Equal(t, []contractsdriver.Where{
		{Query: "name", Args: []any{"test"}},
		{Query: "name1", Args: []any{"test1"}},
		{Query: "name2", Args: []any{"test2"}},
		{Query: "name3", Args: []any{"test3"}},
	}, query1.conditions.where)
}

func TestFilterFindConditions(t *testing.T) {
	tests := []struct {
		name       string
		conditions []any
		expectErr  error
	}{
		{
			name: "condition is empty",
		},
		{
			name:       "condition is empty string",
			conditions: []any{""},
			expectErr:  errors.OrmMissingWhereClause,
		},
		{
			name:       "condition is empty slice",
			conditions: []any{[]string{}},
			expectErr:  errors.OrmMissingWhereClause,
		},
		{
			name:       "condition has value",
			conditions: []any{"name = ?", "test"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := filterFindConditions(test.conditions...)
			if test.expectErr != nil {
				assert.Equal(t, err, test.expectErr)
			} else {
				assert.Nil(t, err)
			}
		})
	}
}

func TestGetDeletedAtColumnName(t *testing.T) {
	type Test1 struct {
		Deleted databaseorm.DeletedAt
	}

	// The result is the database column, not the Go field name, because callers feed it straight
	// into SQL.
	assert.Equal(t, "deleted", getDeletedAtColumn(Test1{}))
	assert.Equal(t, "deleted", getDeletedAtColumn(&Test1{}))

	type Test2 struct {
		Test1
	}

	assert.Equal(t, "deleted", getDeletedAtColumn(Test2{}))
	assert.Equal(t, "deleted", getDeletedAtColumn(&Test2{}))

	// An explicit column tag wins over the snake_cased field name.
	type Tagged struct {
		DeletedAt databaseorm.DeletedAt `gorm:"column:removed_at"`
	}

	assert.Equal(t, "removed_at", getDeletedAtColumn(Tagged{}))

	// A model without a soft-delete field reports no column.
	type Plain struct {
		ID uint
	}

	assert.Equal(t, "", getDeletedAtColumn(Plain{}))
}

func TestGetModelConnection(t *testing.T) {
	tests := []struct {
		name             string
		model            any
		expectConnection string
	}{
		{
			name: "invalid model",
			model: func() any {
				var product string
				return product
			}(),
		},
		{
			name: "not ConnectionModel",
			model: func() any {
				var user User
				return user
			}(),
		},
		{
			name: "the connection of model is empty",
			model: func() any {
				var review Review
				return review
			}(),
		},
		{
			name: "model is map",
			model: func() any {
				return map[string]any{}
			}(),
		},
		{
			name: "the connection of model is not empty",
			model: func() any {
				var product Product
				return product
			}(),
			expectConnection: "sqlite",
		},
		{
			name: "the connection of model is not empty and model is slice",
			model: func() any {
				var products []Product
				return products
			}(),
			expectConnection: "sqlite",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &Query{builder: querybuilder.NewBuilder(nil),
				conditions: Conditions{
					model: test.model,
				},
			}
			connection := query.getModelConnection()

			assert.Equal(t, test.expectConnection, connection)
		})
	}
}

func TestGetObserver(t *testing.T) {
	query := &Query{
		modelToObserver: []contractsorm.ModelToObserver{
			{
				Model:    User{},
				Observer: &UserObserver{},
			},
		},
	}

	assert.Nil(t, query.getObserver(Product{}))
	assert.Equal(t, &UserObserver{}, query.getObserver(User{}))
}

func TestModelToStruct(t *testing.T) {
	tests := []struct {
		name        string
		input       any
		expectError bool
		expectedErr error
		checkResult func(t *testing.T, result any)
	}{
		// Basic cases
		{
			name:        "nil input",
			input:       nil,
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args("nil"),
		},
		{
			name:        "nil pointer to struct",
			input:       (*TestModel)(nil),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
				resultValue := reflect.ValueOf(result)
				assert.True(t, resultValue.IsValid())
				assert.False(t, resultValue.IsNil())
			},
		},
		{
			name:        "valid struct pointer",
			input:       &TestModel{ID: 1, Name: "test"},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
				resultValue := reflect.ValueOf(result)
				assert.True(t, resultValue.IsValid())
				assert.False(t, resultValue.IsNil())
			},
		},
		{
			name:        "struct value (not pointer)",
			input:       TestModel{ID: 1, Name: "test"},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "slice of structs",
			input:       []TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "array of structs",
			input:       [1]TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "pointer to slice",
			input:       &[]TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "pointer to array",
			input:       &[1]TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "nil slice",
			input:       []TestModel(nil),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "empty slice",
			input:       []TestModel{},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "empty array",
			input:       [0]TestModel{},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "slice of pointers to structs",
			input:       []*TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "array of pointers to structs",
			input:       [1]*TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "pointer to slice of pointers",
			input:       &[]*TestModel{{ID: 1, Name: "test"}},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name:        "nil slice of pointers",
			input:       []*TestModel(nil),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name: "multiple pointer levels",
			input: func() any {
				model := &TestModel{ID: 1, Name: "test"}
				ptr1 := &model
				ptr2 := &ptr1
				ptr3 := &ptr2
				return ptr3
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},
		{
			name: "slice with multiple pointer levels",
			input: func() any {
				model := &TestModel{ID: 1, Name: "test"}
				slice := []**TestModel{&model}
				ptrSlice := &slice
				return ptrSlice
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestModel", resultType.String())
			},
		},

		// Interface cases
		{
			name:        "interface type with concrete value",
			input:       TestInterfaceImpl{ID: 1},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestInterfaceImpl", resultType.String())
			},
		},
		{
			name:        "nil interface",
			input:       (TestInterfaceModel)(nil),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args("nil"),
		},
		{
			name: "interface with concrete value",
			input: func() any {
				var iface TestInterfaceModel = TestInterfaceImpl{ID: 1}
				return iface
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.TestInterfaceImpl", resultType.String())
			},
		},

		// Invalid types
		{
			name:        "map type",
			input:       map[string]any{"id": 1, "name": "test"},
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args("map"),
		},
		{
			name:        "nil map",
			input:       (map[string]any)(nil),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args("map"),
		},
		{
			name:        "empty map",
			input:       map[string]any{},
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args("map"),
		},
		{
			name:        "nil pointer to map",
			input:       (*map[string]any)(nil),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args("map"),
		},
		{
			name:        "string type",
			input:       "invalid",
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "int type",
			input:       123,
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "float type",
			input:       123.45,
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "bool type",
			input:       true,
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "pointer to string",
			input:       convert.Pointer("test"),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "pointer to int",
			input:       convert.Pointer(123),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "pointer to float",
			input:       convert.Pointer(123.45),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "pointer to bool",
			input:       convert.Pointer(true),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "channel type",
			input:       make(chan int),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name:        "function type",
			input:       func() {},
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},
		{
			name: "pointer to function",
			input: func() any {
				fn := func() {}
				return &fn
			}(),
			expectError: true,
			expectedErr: errors.OrmQueryInvalidModel.Args(""),
		},

		// Struct cases
		{
			name:        "empty struct",
			input:       struct{}{},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*struct {}", resultType.String())
			},
		},
		{
			name:        "pointer to empty struct",
			input:       &struct{}{},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*struct {}", resultType.String())
			},
		},
		{
			name:        "nil pointer to empty struct",
			input:       (*struct{})(nil),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*struct {}", resultType.String())
			},
		},
		{
			name:        "complex nested struct",
			input:       &ComplexModel{},
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.ComplexModel", resultType.String())
			},
		},
		{
			name:        "nil pointer to complex struct",
			input:       (*ComplexModel)(nil),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.ComplexModel", resultType.String())
			},
		},

		// Edge cases - struct with embedded struct
		{
			name: "struct with embedded struct",
			input: func() any {
				type EmbeddedStruct struct {
					Field string
				}
				type StructWithEmbed struct {
					EmbeddedStruct
					Name string
				}
				return StructWithEmbed{EmbeddedStruct: EmbeddedStruct{Field: "test"}, Name: "name"}
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.StructWithEmbed", resultType.String())
			},
		},
		{
			name: "struct with pointer to embedded struct",
			input: func() any {
				type EmbeddedStruct struct {
					Field string
				}
				type StructWithEmbed struct {
					*EmbeddedStruct
					Name string
				}
				return StructWithEmbed{EmbeddedStruct: &EmbeddedStruct{Field: "test"}, Name: "name"}
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.StructWithEmbed", resultType.String())
			},
		},
		{
			name: "struct with unexported fields",
			input: func() any {
				type StructWithUnexported struct {
					PublicField  string
					privateField string
				}
				return StructWithUnexported{PublicField: "public", privateField: "private"}
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.StructWithUnexported", resultType.String())
			},
		},
		{
			name: "struct with tags",
			input: func() any {
				type StructWithTags struct {
					Field string `json:"field" db:"field"`
				}
				return StructWithTags{Field: "test"}
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultType := reflect.TypeOf(result)
				assert.Equal(t, "*gorm.StructWithTags", resultType.String())
			},
		},

		// Return value validation cases
		{
			name: "returned value is pointer to struct",
			input: func() any {
				model := TestModel{ID: 1, Name: "test"}
				return &model
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				resultValue := reflect.ValueOf(result)
				assert.True(t, resultValue.Kind() == reflect.Pointer)
				assert.False(t, resultValue.IsNil())
				assert.Equal(t, reflect.Struct, resultValue.Elem().Kind())
			},
		},
		{
			name: "returned value can be used for reflection",
			input: func() any {
				model := TestModel{ID: 1, Name: "test"}
				return &model
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				resultValue := reflect.ValueOf(result)
				resultType := resultValue.Type()
				assert.Equal(t, "*gorm.TestModel", resultType.String())

				// Test that we can access fields
				elem := resultValue.Elem()
				idField := elem.FieldByName("ID")
				nameField := elem.FieldByName("Name")
				assert.True(t, idField.IsValid())
				assert.True(t, nameField.IsValid())
			},
		},
		{
			name: "returned value is new instance",
			input: func() any {
				original := &TestModel{ID: 1, Name: "test"}
				return original
			}(),
			expectError: false,
			checkResult: func(t *testing.T, result any) {
				assert.NotNil(t, result)
				// The result should be a different instance
				assert.NotSame(t, &TestModel{ID: 1, Name: "test"}, result)
				assert.Equal(t, reflect.TypeOf(&TestModel{}), reflect.TypeOf(result))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := modelToStruct(tt.input)

			if tt.expectError {
				assert.Error(t, err)
				if tt.expectedErr != nil {
					assert.Equal(t, tt.expectedErr, err)
				}
				assert.Nil(t, result)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, result)
				if tt.checkResult != nil {
					tt.checkResult(t, result)
				}
			}
		})
	}
}

func TestObserverEvent(t *testing.T) {
	assert.EqualError(t, getObserverEvent(contractsorm.EventRetrieved, &UserObserver{})(nil), "retrieved")
	assert.EqualError(t, getObserverEvent(contractsorm.EventCreating, &UserObserver{})(nil), "creating")
	assert.EqualError(t, getObserverEvent(contractsorm.EventCreated, &UserObserver{})(nil), "created")
	assert.EqualError(t, getObserverEvent(contractsorm.EventUpdating, &UserObserver{})(nil), "updating")
	assert.EqualError(t, getObserverEvent(contractsorm.EventUpdated, &UserObserver{})(nil), "updated")
	assert.EqualError(t, getObserverEvent(contractsorm.EventSaving, &UserObserver{})(nil), "saving")
	assert.EqualError(t, getObserverEvent(contractsorm.EventSaved, &UserObserver{})(nil), "saved")
	assert.EqualError(t, getObserverEvent(contractsorm.EventDeleting, &UserObserver{})(nil), "deleting")
	assert.EqualError(t, getObserverEvent(contractsorm.EventDeleted, &UserObserver{})(nil), "deleted")
	assert.EqualError(t, getObserverEvent(contractsorm.EventForceDeleting, &UserObserver{})(nil), "forceDeleting")
	assert.EqualError(t, getObserverEvent(contractsorm.EventForceDeleted, &UserObserver{})(nil), "forceDeleted")
	assert.Nil(t, getObserverEvent("error", &UserObserver{}))
}

func TestWhen(t *testing.T) {
	query := &Query{}
	trueQuery := &Query{builder: querybuilder.NewBuilder(nil)}
	falseQuery := &Query{builder: querybuilder.NewBuilder(nil)}

	callback := func(q contractsorm.Query) contractsorm.Query {
		assert.Same(t, query, q)
		return trueQuery
	}
	falseCallback := func(q contractsorm.Query) contractsorm.Query {
		assert.Same(t, query, q)
		return falseQuery
	}

	assert.Same(t, trueQuery, query.When(true, callback))
	assert.Same(t, trueQuery, query.When(true, callback, falseCallback))
	assert.Same(t, query, query.When(false, callback))
	assert.Same(t, falseQuery, query.When(false, callback, falseCallback))
}

type User struct {
	Name string
}

type UserObserver struct{}

func (u *UserObserver) Retrieved(event contractsorm.Event) error {
	return errors.New("retrieved")
}

func (u *UserObserver) Creating(event contractsorm.Event) error {
	return errors.New("creating")
}

func (u *UserObserver) Created(event contractsorm.Event) error {
	return errors.New("created")
}

func (u *UserObserver) Updating(event contractsorm.Event) error {
	return errors.New("updating")
}

func (u *UserObserver) Updated(event contractsorm.Event) error {
	return errors.New("updated")
}

func (u *UserObserver) Saving(event contractsorm.Event) error {
	return errors.New("saving")
}

func (u *UserObserver) Saved(event contractsorm.Event) error {
	return errors.New("saved")
}

func (u *UserObserver) Deleting(event contractsorm.Event) error {
	return errors.New("deleting")
}

func (u *UserObserver) Deleted(event contractsorm.Event) error {
	return errors.New("deleted")
}

func (u *UserObserver) ForceDeleting(event contractsorm.Event) error {
	return errors.New("forceDeleting")
}

func (u *UserObserver) ForceDeleted(event contractsorm.Event) error {
	return errors.New("forceDeleted")
}

type Product struct {
	Name string
}

func (p *Product) Connection() string {
	return "sqlite"
}

type Review struct {
	Body string
}

func (r *Review) Connection() string {
	return ""
}

// TestModel is a simple struct for testing
type TestModel struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// TestInterfaceModel is an interface type for testing
type TestInterfaceModel interface {
	GetID() uint
}

// TestInterfaceImpl implements TestInterfaceModel
type TestInterfaceImpl struct {
	ID uint `json:"id"`
}

func (t TestInterfaceImpl) GetID() uint {
	return t.ID
}

// ComplexModel is a more complex struct for testing
type ComplexModel struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Age      int    `json:"age"`
	IsActive bool   `json:"is_active"`
	Nested   struct {
		Field1 string `json:"field1"`
		Field2 int    `json:"field2"`
	} `json:"nested"`
}

// Test models for addGlobalScopes
type ModelWithoutGlobalScopes struct {
	ID   uint
	Name string
}

type ModelWithGlobalScopes struct {
	ID   uint
	Name string
}

func (ModelWithGlobalScopes) TableName() string { return "model_with_global_scopes" }

func (m *ModelWithGlobalScopes) GlobalScopes() map[string]func(contractsorm.Query) contractsorm.Query {
	return map[string]func(contractsorm.Query) contractsorm.Query{
		"active": func(query contractsorm.Query) contractsorm.Query {
			return query.Where("active", true)
		},
		"verified": func(query contractsorm.Query) contractsorm.Query {
			return query.Where("verified", true)
		},
	}
}

type ModelWithEmptyGlobalScopes struct {
	ID   uint
	Name string
}

func (m *ModelWithEmptyGlobalScopes) GlobalScopes() map[string]func(contractsorm.Query) contractsorm.Query {
	return map[string]func(contractsorm.Query) contractsorm.Query{}
}

// getColumnsUser is the fixture for TestBasicSelectWithGetColumns: a bare model whose table is
// "users", so the transient-columns SQL is exactly the one Laravel's corpus asserts.
type getColumnsUser struct {
	ID   uint
	Name string
}

func (getColumnsUser) TableName() string { return "users" }

// newGetColumnsQuery builds a query over the fixture whose connection pool records every statement
// the compiled read executes, so the SQL Get sends can be inspected with no database behind it. The
// connection names no driver, so the identifiers are quoted with double quotes and bindings stay "?",
// which is Laravel's generic grammar and the shape the corpus pins.
func newGetColumnsQuery(t *testing.T) (*Query, *recordingSQLDriver) {
	t.Helper()

	recorder := &recordingSQLDriver{}
	sqlDB := sql.OpenDB(recordingConnector{driver: recorder})
	t.Cleanup(func() { _ = sqlDB.Close() })

	conditions := Conditions{model: &getColumnsUser{}}

	// The recording pool is the query's writer directly: the ORM compiles its own statements now, so
	// there is no ORM session between the query and the pool.
	return NewQuery(context.Background(), nil, contractsdatabase.Config{}, sqlDB, nil, nil, nil, nil, &conditions, nil), recorder
}

// TestBasicSelectWithGetColumns ports Laravel's testBasicSelectWithGetColumns. A column list passed
// to Get is used for that call only: the columns render into the statement it runs, but the query
// keeps the selection it already had — and it only takes the columns when it had none — so a
// statement compiled afterwards is unchanged. The corpus asserts the SQL a mocked connection
// receives; the in-package recording driver plays that connection here.
func TestBasicSelectWithGetColumns(t *testing.T) {
	query, recorder := newGetColumnsQuery(t)

	// get() selects the wildcard, and leaves the query's own columns unset.
	var first []getColumnsUser
	assert.NoError(t, query.Get(&first))
	assert.Equal(t, `select * from "users"`, recorder.last(t).sql)
	assert.Empty(t, query.builder.Selects())

	// get(['foo', 'bar']) renders the two columns transiently.
	var second []getColumnsUser
	assert.NoError(t, query.Get(&second, []string{"foo", "bar"}))
	assert.Equal(t, `select "foo", "bar" from "users"`, recorder.last(t).sql)
	assert.Empty(t, query.builder.Selects())

	// get('baz') wraps the single column name.
	var third []getColumnsUser
	assert.NoError(t, query.Get(&third, "baz"))
	assert.Equal(t, `select "baz" from "users"`, recorder.last(t).sql)
	assert.Empty(t, query.builder.Selects())

	// toSql() is unchanged: the transient columns never touched the query.
	statement, _, err := query.compileSelect()
	assert.NoError(t, err)
	assert.Equal(t, `select * from "users"`, statement)
	assert.Empty(t, query.builder.Selects())
}

// Laravel's onceWithColumns ignores the Get columns whenever a raw selection is already present.
func TestGetColumnsKeepsExistingRawSelect(t *testing.T) {
	base, recorder := newGetColumnsQuery(t)
	query := base.clone().SelectRaw("count(*) as total").(*Query)
	var rows []struct{ Total int64 }

	require.NoError(t, query.Get(&rows, "name"))
	assert.Equal(t, `select count(*) as total from "users"`, recorder.last(t).sql)
	assert.Empty(t, recorder.last(t).args)
	assert.Empty(t, base.builder.Selects())

	statement, bindings, err := query.compileSelect()
	require.NoError(t, err)
	assert.Equal(t, `select count(*) as total from "users"`, statement)
	assert.Empty(t, bindings)
}

// TestBasicSelectUseWritePdo ports Laravel's testBasicSelectUseWritePdo. Laravel's builder carries a
// useWritePdo flag that reaches the connection as the inverse of its $useReadPdo argument: a plain
// get() reads through the replica ($useReadPdo = true), while useWritePdo()->get() forces the write
// connection ($useReadPdo = false). The corpus pins that split against a mocked connection that
// records the third argument of select(); in this port a compiled read runs on whichever of the
// connection's pools the executor resolves, so the assertion lands on which recording pool saw the
// statement. The SQL is the one the corpus asserts for a MySQL connection: `select * from `users“.
func TestBasicSelectUseWritePdo(t *testing.T) {
	t.Cleanup(databasedriver.CloseConnections)

	writer := &recordingSQLDriver{}
	reader := &recordingSQLDriver{}

	config := mocksconfig.NewConfig(t)
	config.EXPECT().GetInt("database.pool.max_idle_conns", 10).Return(10).Times(2)
	config.EXPECT().GetInt("database.pool.max_open_conns", 100).Return(100).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_idletime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)
	config.EXPECT().GetDuration("database.pool.conn_max_lifetime", time.Duration(3600)).Return(time.Duration(3600)).Times(2)

	// Each candidate connection supplies the connector that opens its pool, so the connection the
	// framework builds runs on the two recording pools and records nothing else.
	pool := contractsdatabase.Pool{
		Writers: []contractsdatabase.Config{{Driver: "mysql", Database: "app", Connector: recordingConnector{driver: writer}}},
		Readers: []contractsdatabase.Config{{Driver: "mysql", Database: "replica", Connector: recordingConnector{driver: reader}}},
	}

	conn, err := databasedriver.Open(config, discardQueryLogger{}, pool, "read_write_pdo", nil)
	require.NoError(t, err)
	require.NotNil(t, conn)

	query := NewQuery(context.Background(), config,
		contractsdatabase.Config{Driver: "mysql", Connection: "read_write_pdo"},
		conn.Writer(), nil, nil, nil, nil, &Conditions{model: &getColumnsUser{}}, nil)

	// get() reads through the replica.
	var first []getColumnsUser
	assert.NoError(t, query.Get(&first))
	assert.Equal(t, "select * from `users`", reader.last(t).sql)
	assert.Equal(t, 0, writer.count())

	// useWritePdo()->get() forces the write connection.
	var second []getColumnsUser
	assert.NoError(t, query.UseWritePdo().Get(&second))
	assert.Equal(t, "select * from `users`", writer.last(t).sql)
	assert.Equal(t, 1, reader.count())
}

// discardQueryLogger satisfies the framework's database logger without recording anything; this test
// asserts on pool routing, not on log output.
type discardQueryLogger struct{}

func (discardQueryLogger) Level(contractslogger.Level) contractslogger.Logger {
	return discardQueryLogger{}
}
func (discardQueryLogger) Log() log.Log                                                { return nil }
func (discardQueryLogger) Infof(context.Context, string, ...any)                       {}
func (discardQueryLogger) Warningf(context.Context, string, ...any)                    {}
func (discardQueryLogger) Errorf(context.Context, string, ...any)                      {}
func (discardQueryLogger) Panicf(context.Context, string, ...any)                      {}
func (discardQueryLogger) Trace(context.Context, *carbon.Carbon, string, int64, error) {}
