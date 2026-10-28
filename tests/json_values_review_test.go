package tests

import (
	"database/sql/driver"
	"encoding/json"

	contractsschema "github.com/gradii/framework/contracts/database/schema"
	"github.com/gradii/framework/database/types"
	"github.com/stretchr/testify/assert"
)

// Both value and pointer fields below use a pointer-receiver Valuer, independent
// of their Go names and of GORM's serializer tag.
type reviewJSONPayload struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func (v *reviewJSONPayload) Value() (driver.Value, error) {
	if v != nil && v.Name == "fail Value" {
		return nil, assert.AnError
	}
	return json.Marshal(v)
}

func (v *reviewJSONPayload) Scan(source any) error {
	var document types.JSON
	if err := document.Scan(source); err != nil {
		return err
	}
	var decoded reviewJSONPayload
	if document != nil {
		if err := json.Unmarshal(document, &decoded); err != nil {
			return err
		}
	}
	*v = decoded
	return nil
}

type reviewJSONRecord struct {
	ID           int                `gorm:"column:id;primaryKey"`
	Raw          types.JSON         `gorm:"column:raw;type:json"`
	Object       types.JSONMap      `gorm:"column:object;type:json"`
	Payload      *reviewJSONPayload `gorm:"column:payload;type:json;serializer:json"`
	ValuePayload reviewJSONPayload  `gorm:"column:value_payload;type:json"`
}

func (reviewJSONRecord) TableName() string { return "review_json_values" }

func (s *QueryTestSuite) TestStandardJSONValues() {
	for name, fixture := range s.queries {
		s.Run(name, func() {
			schema := newSchema(fixture, s.queries)
			s.Require().NoError(schema.DropIfExists("review_json_values"))
			s.Require().NoError(schema.Create("review_json_values", func(table contractsschema.Blueprint) {
				table.Integer("id")
				table.Primary("id")
				for _, column := range []string{"raw", "object", "payload", "value_payload"} {
					table.Jsonb(column).Nullable()
				}
			}))
			s.T().Cleanup(func() { s.NoError(schema.DropIfExists("review_json_values")) })
			record := reviewJSONRecord{ID: 1, Raw: types.JSON(`{"large":12345678901234567890}`), Object: types.JSONMap{"name": "你好🌏"}, Payload: &reviewJSONPayload{Name: "O'Reilly", Age: 7}, ValuePayload: reviewJSONPayload{Name: "value field", Age: 8}}
			s.Require().NoError(fixture.Query().Create(&record))
			var loaded reviewJSONRecord
			s.Require().NoError(fixture.Query().Where("id", 1).First(&loaded))
			s.JSONEq(string(record.Raw), string(loaded.Raw))
			s.Equal(record.Object, loaded.Object)
			s.Equal(record.Payload, loaded.Payload)
			s.Equal(record.ValuePayload, loaded.ValuePayload)
			loaded.Payload = &reviewJSONPayload{Name: "saved", Age: 9}
			loaded.ValuePayload.Name = "saved value"
			s.Require().NoError(fixture.Query().Save(&loaded))
			_, err := fixture.Query().Model(&reviewJSONRecord{}).Where("id", 1).Update("object", types.JSONMap{"replaced": true})
			s.Require().NoError(err)
			_, err = fixture.Query().Model(&reviewJSONRecord{}).Where("id", 1).Update("raw->nested", &reviewJSONPayload{Name: "path", Age: 10})
			s.Require().NoError(err)
			s.Require().NoError(fixture.Query().Where("id", 1).First(&loaded))
			s.Equal(types.JSONMap{"replaced": true}, loaded.Object)
			s.Equal("saved", loaded.Payload.Name)
			s.Equal("saved value", loaded.ValuePayload.Name)
			_, err = fixture.Query().Model(&reviewJSONRecord{}).Where("id", 1).Update(map[string]any{
				"payload":       &reviewJSONPayload{Name: "updated pointer", Age: 11},
				"value_payload": reviewJSONPayload{Name: "updated value", Age: 12},
			})
			s.Require().NoError(err)
			s.Require().NoError(fixture.Query().Where("id", 1).First(&loaded))
			s.Equal("updated pointer", loaded.Payload.Name)
			s.Equal("updated value", loaded.ValuePayload.Name)
			var document map[string]json.RawMessage
			s.Require().NoError(json.Unmarshal(loaded.Raw, &document))
			s.JSONEq(`{"name":"path","age":10}`, string(document["nested"]))
			_, err = fixture.Query().Model(&reviewJSONRecord{}).Where("id", 1).Update(map[string]any{
				"raw->number": types.JSON(`9007199254740993`),
				"raw->flag":   types.JSON(`true`),
				"raw->labels": types.JSON(`["admin"]`),
			})
			s.Require().NoError(err)
			s.Require().NoError(fixture.Query().Where("id", 1).First(&loaded))
			s.Require().NoError(json.Unmarshal(loaded.Raw, &document))
			s.Equal("9007199254740993", string(document["number"]))
			s.Equal("true", string(document["flag"]))
			s.JSONEq(`["admin"]`, string(document["labels"]))
			count, err := fixture.Query().Model(&reviewJSONRecord{}).WhereJsonContains("raw->labels", types.JSON(`"admin"`)).Count()
			s.Require().NoError(err)
			s.EqualValues(1, count)
			_, err = fixture.Query().Model(&reviewJSONRecord{}).Where("id", 1).Update(map[string]any{"raw": types.JSON(`null`), "object": types.JSONMap(nil), "payload": nil})
			s.Require().NoError(err)
			s.Require().NoError(fixture.Query().Where("id", 1).First(&loaded))
			s.Equal(types.JSON(`null`), loaded.Raw)
			s.Nil(loaded.Object)
			s.Nil(loaded.Payload)
			var storage struct {
				ObjectNull int `gorm:"column:object_null"`
				RawNull    int `gorm:"column:raw_null"`
			}
			s.Require().NoError(fixture.Query().Raw("select case when object is null then 1 else 0 end as object_null, case when raw is null then 1 else 0 end as raw_null from review_json_values where id = ?", 1).Scan(&storage))
			s.Equal(1, storage.ObjectNull)
			s.Zero(storage.RawNull)
			bad := reviewJSONRecord{ID: 2, Payload: &reviewJSONPayload{Name: "fail Value"}}
			s.ErrorIs(fixture.Query().Create(&bad), assert.AnError)
			// Unaddressable models still expose pointer-receiver Value methods.
			plain := reviewJSONRecord{ID: 3, ValuePayload: reviewJSONPayload{Name: "by value"}}
			s.Require().NoError(fixture.Query().Create(plain))
			s.Require().NoError(fixture.Query().Where("id", 3).First(&loaded))
			s.Equal("by value", loaded.ValuePayload.Name)
			batch := []reviewJSONRecord{{ID: 4, Payload: &reviewJSONPayload{Name: "batch 1"}}, {ID: 5, Payload: &reviewJSONPayload{Name: "batch 2"}}}
			s.Require().NoError(fixture.Query().Create(&batch))
			tx, err := fixture.Query().BeginTransaction()
			s.Require().NoError(err)
			_, err = tx.Model(&reviewJSONRecord{}).Where("id", 4).Update("raw", types.JSON(`[]`))
			s.Require().NoError(err)
			s.Require().NoError(tx.Rollback())
			s.Require().NoError(fixture.Query().Where("id", 4).First(&loaded))
			s.Nil(loaded.Raw)
		})
	}
}
