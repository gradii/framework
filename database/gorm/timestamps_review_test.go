package gorm

import (
	"reflect"
	"testing"
	"time"

	"github.com/gradii/framework/support/carbon"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type timestampUnitModel[T any] struct {
	ID        int    `gorm:"column:id;primaryKey"`
	Label     string `gorm:"column:label"`
	CreatedAt T      `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt T      `gorm:"column:updated_at;autoUpdateTime"`
}

func (timestampUnitModel[T]) TableName() string { return "review_timestamps" }

type timestampUnitCase struct {
	name  string
	model func(*time.Time) any
	value func(time.Time) any
}

func timestampUnitShape[T any](name string, convert func(time.Time) T) timestampUnitCase {
	return timestampUnitCase{name: name, value: func(value time.Time) any { return convert(value) }, model: func(value *time.Time) any {
		model := &timestampUnitModel[T]{ID: 1, Label: "timestamp"}
		if value != nil {
			model.CreatedAt, model.UpdatedAt = convert(*value), convert(*value)
		}
		return model
	}}
}

func timestampUnitCases() []timestampUnitCase {
	return []timestampUnitCase{
		timestampUnitShape("carbon.DateTime", func(t time.Time) carbon.DateTime { return *carbon.NewDateTime(carbon.FromStdTime(t)) }),
		timestampUnitShape("pointer_carbon.DateTime", func(t time.Time) *carbon.DateTime { return carbon.NewDateTime(carbon.FromStdTime(t)) }),
		timestampUnitShape("time.Time", func(t time.Time) time.Time { return t }),
		timestampUnitShape("pointer_time.Time", func(t time.Time) *time.Time { return &t }),
	}
}

func timestampUnitTime(t *testing.T, value any) time.Time {
	t.Helper()
	if value == nil || reflect.ValueOf(value).IsZero() {
		return time.Time{}
	}
	switch value := value.(type) {
	case time.Time:
		return value
	case *time.Time:
		return *value
	case carbon.DateTime:
		return value.StdTime()
	case *carbon.DateTime:
		return value.StdTime()
	default:
		t.Fatalf("unexpected timestamp binding %T", value)
		return time.Time{}
	}
}

func assertTimestampUnitNow(t *testing.T, value any, started time.Time) {
	t.Helper()
	actual := timestampUnitTime(t, value)
	assert.False(t, actual.IsZero(), "automatic timestamp must be populated")
	assert.True(t, !actual.Before(started.Add(-time.Second)) && !actual.After(time.Now().Add(time.Second)), "timestamp %s is outside operation window", actual)
}

func TestTimestampShapesReviewAssignments(t *testing.T) {
	manual := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	for _, tc := range timestampUnitCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("CreateAutomatic", func(t *testing.T) {
				model := tc.model(nil)
				query := compiledQuery(t, model, Conditions{})
				started := time.Now()
				records, err := query.insertRecords(model)
				require.NoError(t, err)
				require.Len(t, records, 1)
				for _, column := range []string{"created_at", "updated_at"} {
					assertTimestampUnitNow(t, records[0][column], started)
				}
			})
			t.Run("CreateManual", func(t *testing.T) {
				model := tc.model(&manual)
				records, err := compiledQuery(t, model, Conditions{}).insertRecords(model)
				require.NoError(t, err)
				for _, column := range []string{"created_at", "updated_at"} {
					assert.True(t, manual.Equal(timestampUnitTime(t, records[0][column])), column)
				}
			})
			t.Run("SaveAutomatic", func(t *testing.T) {
				model := tc.model(&manual)
				field := reflect.ValueOf(model).Elem().FieldByName("UpdatedAt")
				field.Set(reflect.Zero(field.Type()))
				started := time.Now()
				assignments, err := compiledQuery(t, model, Conditions{}).saveAssignments(model)
				require.NoError(t, err)
				assert.True(t, manual.Equal(timestampUnitTime(t, assignments["created_at"])))
				assertTimestampUnitNow(t, assignments["updated_at"], started)
			})
			t.Run("UpdateAutomaticAndManual", func(t *testing.T) {
				query := compiledQuery(t, tc.model(&manual), Conditions{})
				started := time.Now()
				assignments, err := query.updateAssignments(map[string]any{"label": "updated"})
				require.NoError(t, err)
				assertTimestampUnitNow(t, assignments["updated_at"], started)
				assignments, err = query.updateAssignments(map[string]any{"updated_at": tc.value(manual)})
				require.NoError(t, err)
				assert.True(t, manual.Equal(timestampUnitTime(t, assignments["updated_at"])))
			})
			t.Run("BulkAutomaticValues", func(t *testing.T) {
				model := tc.model(nil)
				typ := reflect.TypeOf(model).Elem()
				batch := reflect.MakeSlice(reflect.SliceOf(typ), 2, 2)
				for i := range 2 {
					batch.Index(i).Set(reflect.ValueOf(tc.model(nil)).Elem())
				}
				started := time.Now()
				records, err := compiledQuery(t, model, Conditions{}).insertRecords(batch.Interface())
				require.NoError(t, err)
				require.Len(t, records, 2)
				for _, record := range records {
					assertTimestampUnitNow(t, record["created_at"], started)
					assertTimestampUnitNow(t, record["updated_at"], started)
				}
			})
		})
	}
}
