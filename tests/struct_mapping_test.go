package tests

func (s *QueryTestSuite) TestStructTagMapping() {
	type ORMProjection struct {
		ID          uint   `gorm:"primaryKey" db:"other_id"`
		DisplayName string `gorm:"column:name;size:100" db:"other_name"`
	}
	type DBProjection struct {
		ID          uint   `db:"id" gorm:"column:other_id"`
		DisplayName string `db:"name" gorm:"column:other_name"`
	}
	for driver, query := range s.queries {
		s.Run(driver, func() {
			alice := User{Name: "Alice"}
			bob := User{Name: "Bob"}
			s.Require().NoError(query.Query().Create(&alice))
			s.Require().NoError(query.Query().Create(&bob))
			expectedORM := []ORMProjection{{ID: alice.ID, DisplayName: "Alice"}, {ID: bob.ID, DisplayName: "Bob"}}
			expectedDB := []DBProjection{{ID: alice.ID, DisplayName: "Alice"}, {ID: bob.ID, DisplayName: "Bob"}}

			var models []ORMProjection
			s.Require().NoError(query.Query().Table("users").OrderBy("id").Find(&models))
			s.Equal(expectedORM, models)
			var model ORMProjection
			s.Require().NoError(query.Query().Table("users").Where("id", alice.ID).First(&model))
			s.Equal(expectedORM[0], model)

			models = nil
			for row := range query.Query().Table("users").OrderBy("id").Cursor() {
				var model ORMProjection
				s.Require().NoError(row.Scan(&model))
				models = append(models, model)
			}
			s.Equal(expectedORM, models)
			_, err := query.Query().Table("users").OrderBy("id").CursorPaginate(2, "", &models)
			s.Require().NoError(err)
			s.Equal(expectedORM, models)

			var records []DBProjection
			s.Require().NoError(query.DB().Table("users").OrderBy("id").Get(&records))
			s.Equal(expectedDB, records)
			var record DBProjection
			s.Require().NoError(query.DB().Table("users").Where("id", alice.ID).First(&record))
			s.Equal(expectedDB[0], record)
			_, err = query.DB().Table("users").OrderBy("id").CursorPaginate(2, "", &records)
			s.Require().NoError(err)
			s.Equal(expectedDB, records)
		})
	}
}
