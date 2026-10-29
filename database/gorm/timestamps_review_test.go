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
				created := reflect.ValueOf(model).Elem().FieldByName("CreatedAt")
				updated := reflect.ValueOf(model).Elem().FieldByName("UpdatedAt")
				if created.Kind() == reflect.Pointer && !created.IsNil() && !updated.IsNil() {
					assert.NotEqual(t, created.Pointer(), updated.Pointer(), "automatic timestamps must own independent pointer storage")
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
			t.Run("CreateUnaddressableValue", func(t *testing.T) {
				model := reflect.ValueOf(tc.model(nil)).Elem().Interface()
				started := time.Now()
				records, err := compiledQuery(t, model, Conditions{}).insertRecords(model)
				require.NoError(t, err)
				require.Len(t, records, 1)
				assertTimestampUnitNow(t, records[0]["created_at"], started)
				assertTimestampUnitNow(t, records[0]["updated_at"], started)
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
			t.Run("ExplicitMapNullVersusUnsetStruct", func(t *testing.T) {
				query := compiledQuery(t, tc.model(&manual), Conditions{})
				for _, prepare := range []func(any) (map[string]any, error){query.updateAssignments, query.saveAssignments} {
					assignments, err := prepare(map[string]any{"label": "explicit null", "updated_at": nil})
					require.NoError(t, err)
					require.Contains(t, assignments, "updated_at")
					assert.Nil(t, assignments["updated_at"], "a map explicitly requests SQL NULL")
				}
				started := time.Now()
				assignments, err := query.updateAssignments(tc.model(nil))
				require.NoError(t, err)
				assertTimestampUnitNow(t, assignments["updated_at"], started)
			})
			t.Run("UpdateSelectAndOmit", func(t *testing.T) {
				query := compiledQuery(t, tc.model(nil), Conditions{})
				started := time.Now()
				assignments, err := query.Select("label").(*Query).updateAssignments(map[string]any{"label": "selected"})
				require.NoError(t, err)
				assertTimestampUnitNow(t, assignments["updated_at"], started)
				omitted := query.Omit("updated_at").(*Query)
				for _, prepare := range []func(any) (map[string]any, error){omitted.updateAssignments, omitted.saveAssignments} {
					assignments, err := prepare(tc.model(nil))
					require.NoError(t, err)
					assert.NotContains(t, assignments, "updated_at")
				}
				records, err := query.Omit("created_at", "updated_at").(*Query).insertRecords(tc.model(nil))
				require.NoError(t, err)
				require.Len(t, records, 1)
				assert.NotContains(t, records[0], "created_at")
				assert.NotContains(t, records[0], "updated_at")
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
				for i := range batch.Len() {
					assertTimestampUnitNow(t, batch.Index(i).FieldByName("CreatedAt").Interface(), started)
					assertTimestampUnitNow(t, batch.Index(i).FieldByName("UpdatedAt").Interface(), started)
				}
			})
			t.Run("BulkManualValues", func(t *testing.T) {
				model := tc.model(&manual)
				batch := reflect.MakeSlice(reflect.SliceOf(reflect.TypeOf(model).Elem()), 2, 2)
				for i := range batch.Len() {
					batch.Index(i).Set(reflect.ValueOf(tc.model(&manual)).Elem())
				}
				records, err := compiledQuery(t, model, Conditions{}).insertRecords(batch.Interface())
				require.NoError(t, err)
				require.Len(t, records, 2)
				for i, record := range records {
					for _, pair := range [][2]string{{"created_at", "CreatedAt"}, {"updated_at", "UpdatedAt"}} {
						assert.True(t, manual.Equal(timestampUnitTime(t, record[pair[0]])), pair[0])
						assert.True(t, manual.Equal(timestampUnitTime(t, batch.Index(i).FieldByName(pair[1]).Interface())), pair[1])
					}
				}
			})
		})
	}
}

type timestampMixedUnitModel[C, U any] struct {
	ID        int    `gorm:"column:id;primaryKey"`
	Label     string `gorm:"column:label"`
	CreatedAt C      `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt U      `gorm:"column:updated_at;autoUpdateTime"`
}

func (timestampMixedUnitModel[C, U]) TableName() string { return "review_timestamps" }

func TestTimestampShapesReviewMixedFieldTypes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model any
	}{
		{"standard_created_carbon_updated", &timestampMixedUnitModel[time.Time, *carbon.DateTime]{ID: 1}},
		{"carbon_created_standard_updated", &timestampMixedUnitModel[carbon.DateTime, *time.Time]{ID: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := compiledQuery(t, tc.model, Conditions{})
			started := time.Now()
			records, err := query.insertRecords(tc.model)
			require.NoError(t, err)
			require.Len(t, records, 1)
			assertTimestampUnitNow(t, records[0]["created_at"], started)
			assertTimestampUnitNow(t, records[0]["updated_at"], started)
			model := reflect.ValueOf(tc.model).Elem()
			assertTimestampUnitNow(t, model.FieldByName("CreatedAt").Interface(), started)
			assertTimestampUnitNow(t, model.FieldByName("UpdatedAt").Interface(), started)
			assignments, err := query.updateAssignments(map[string]any{"label": "mixed update"})
			require.NoError(t, err)
			assertTimestampUnitNow(t, assignments["updated_at"], started)
		})
	}
}

func TestTimestampShapesReviewNonNilZeroWrappers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model any
	}{
		{"standard_pointer", &timestampUnitModel[*time.Time]{CreatedAt: &time.Time{}, UpdatedAt: &time.Time{}}},
		{"carbon_pointer_empty_wrapper", &timestampUnitModel[*carbon.DateTime]{CreatedAt: &carbon.DateTime{}, UpdatedAt: &carbon.DateTime{}}},
		{"carbon_value_zero_instant", &timestampUnitModel[carbon.DateTime]{CreatedAt: *carbon.NewDateTime(carbon.ZeroValue()), UpdatedAt: *carbon.NewDateTime(carbon.ZeroValue())}},
		{"carbon_pointer_zero_instant", &timestampUnitModel[*carbon.DateTime]{CreatedAt: carbon.NewDateTime(carbon.ZeroValue()), UpdatedAt: carbon.NewDateTime(carbon.ZeroValue())}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			records, err := compiledQuery(t, tc.model, Conditions{}).insertRecords(tc.model)
			require.NoError(t, err)
			require.Len(t, records, 1)
			assertTimestampUnitNow(t, records[0]["created_at"], started)
			assertTimestampUnitNow(t, records[0]["updated_at"], started)
		})
	}
}

func TestTimestampShapesReviewInvalidCarbonIsPreserved(t *testing.T) {
	invalid := carbon.NewDateTime(carbon.Parse("not a valid timestamp"))
	require.Error(t, invalid.Error)
	for _, tc := range []struct {
		name  string
		model any
	}{
		{"value", &timestampUnitModel[carbon.DateTime]{CreatedAt: *invalid, UpdatedAt: *invalid}},
		{"pointer", &timestampUnitModel[*carbon.DateTime]{CreatedAt: invalid, UpdatedAt: invalid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, err := compiledQuery(t, tc.model, Conditions{}).insertRecords(tc.model)
			require.NoError(t, err)
			require.Len(t, records, 1)
			for _, column := range []string{"created_at", "updated_at"} {
				value := reflect.Indirect(reflect.ValueOf(records[0][column])).Interface().(carbon.DateTime)
				assert.Same(t, invalid.Carbon, value.Carbon, "invalid manual Carbon must not be replaced by an automatic value")
				assert.ErrorIs(t, value.Error, invalid.Error)
			}
		})
	}
}

func TestTimestampShapesReviewExplicitTableDTO(t *testing.T) {
	dto := &struct {
		ID        int              `gorm:"column:id;primaryKey"`
		Label     string           `gorm:"column:label"`
		CreatedAt time.Time        `gorm:"column:created_at;autoCreateTime"`
		UpdatedAt *carbon.DateTime `gorm:"column:updated_at;autoUpdateTime"`
	}{ID: 1, Label: "dto"}
	query := compiledQuery(t, dto, Conditions{}).Table("review_timestamps").(*Query)
	started := time.Now()
	records, err := query.insertRecords(dto)
	require.NoError(t, err)
	require.Len(t, records, 1)
	assertTimestampUnitNow(t, dto.CreatedAt, started)
	assertTimestampUnitNow(t, dto.UpdatedAt, started)
	created := dto.CreatedAt
	assignments, err := query.updateAssignments(map[string]any{"label": "updated DTO"})
	require.NoError(t, err)
	require.NoError(t, query.assignUpdatedValues(assignments))
	assert.Equal(t, "updated DTO", dto.Label)
	assert.True(t, dto.CreatedAt.Equal(created))
	assertTimestampUnitNow(t, dto.UpdatedAt, started)
	dto.UpdatedAt = nil
	assignments, err = query.saveAssignments(dto)
	require.NoError(t, err)
	require.NoError(t, query.assignSavedTimestamps(dto, assignments))
	assertTimestampUnitNow(t, dto.UpdatedAt, started)
}

type rejectingTimestamp struct{}

func (*rejectingTimestamp) Scan(any) error { return assert.AnError }

type timestampRejectingModel struct {
	ID        int                `gorm:"column:id;primaryKey"`
	CreatedAt rejectingTimestamp `gorm:"column:created_at;autoCreateTime"`
}

func (timestampRejectingModel) TableName() string { return "review_timestamps" }

func TestTimestampShapesReviewAutomaticAssignmentError(t *testing.T) {
	model := &timestampRejectingModel{ID: 1}
	query, recorder := newPostgresKeyQuery(t, model)
	err := query.Create(model)
	require.ErrorIs(t, err, assert.AnError)
	assert.Zero(t, recorder.count(), "failed timestamp conversion must prevent executing the insert")
}

func TestTimestampUnixCompatibility(t *testing.T) {
	model := &shortcutUser{ID: 1}
	query := compiledQuery(t, model, Conditions{})
	started := time.Now().Unix()
	_, err := query.insertRecords(model)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, model.CreatedAt, started)
	assert.Equal(t, model.CreatedAt, model.UpdatedAt)
	model.UpdatedAt = 0
	assignments, err := query.saveAssignments(model)
	require.NoError(t, err)
	stamp, ok := assignments["updated_at"].(int64)
	require.True(t, ok)
	assert.GreaterOrEqual(t, stamp, started)
}

func TestTimestampManualSharedPointersAreReplaced(t *testing.T) {
	manual := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	next := manual.AddDate(1, 0, 0)
	for _, tc := range timestampUnitCases() {
		if reflect.TypeOf(tc.value(manual)).Kind() != reflect.Pointer {
			continue
		}
		for _, source := range []struct {
			name  string
			value any
		}{{"value", next}, {"pointer", &next}, {"SQL text", "2002-02-03 04:05:06"}} {
			t.Run(tc.name+"/"+source.name, func(t *testing.T) {
				shared := tc.value(manual)
				model := tc.model(nil)
				fields := reflect.ValueOf(model).Elem()
				fields.FieldByName("CreatedAt").Set(reflect.ValueOf(shared))
				fields.FieldByName("UpdatedAt").Set(reflect.ValueOf(shared))
				q := compiledQuery(t, model, Conditions{})
				require.NoError(t, q.assignUpdatedValues(map[string]any{"updated_at": source.value}))
				assert.True(t, manual.Equal(timestampUnitTime(t, shared)), "caller-owned time must not change")
				assert.True(t, manual.Equal(timestampUnitTime(t, fields.FieldByName("CreatedAt").Interface())))
				assert.True(t, next.Equal(timestampUnitTime(t, fields.FieldByName("UpdatedAt").Interface())))
				assert.NotEqual(t, fields.FieldByName("CreatedAt").Pointer(), fields.FieldByName("UpdatedAt").Pointer())
			})
		}
	}
}
