package tests

import (
	contractsorm "github.com/gradii/framework/contracts/database/orm"
)

// The round-15b scope-composition fixes, checked against real rows on every available driver: the
// SQL pins live in database/gorm/scope_composition_positional_test.go; these confirm that the
// statement shapes they pin select the rows Laravel's statements select, bindings in order.

// TestWhereHasInsideAWhereClosure_Rows: a whereHas a where closure wrote is part of the nested
// group (Laravel: `where "name" like ? and (exists (...))`), so a user without books is not
// matched. The or-closure variant also checks that the closure's bindings and the existence
// callback's bindings land in call order (Laravel W7 shape: `("name" = ? or ("name" = ? and exists
// (... "name" = ? ...)))`).
func (s *QueriesRelationshipsTestSuite) TestWhereHasInsideAWhereClosure_Rows() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			alice := &User{Name: "rel_wcl_alice"}
			s.Nil(query.Query().Create(alice))
			s.Nil(query.Query().Relation(alice, "Books").Save(&Book{Name: "wcl_target"}))
			bob := &User{Name: "rel_wcl_bob"}
			s.Nil(query.Query().Create(bob))
			carol := &User{Name: "rel_wcl_carol"}
			s.Nil(query.Query().Create(carol))
			s.Nil(query.Query().Relation(carol, "Books").Save(&Book{Name: "wcl_other"}))

			var users []User
			s.Nil(query.Query().Where("name", "like", "rel_wcl_%").Where(func(q contractsorm.Query) contractsorm.Query {
				return q.WhereHas("Books")
			}).Get(&users))
			s.ElementsMatch([]string{"rel_wcl_alice", "rel_wcl_carol"}, namesOf(users))

			users = nil
			s.Nil(query.Query().Where("name", "like", "rel_wcl_%").Where(func(q contractsorm.Query) contractsorm.Query {
				return q.Where("name", "rel_wcl_bob").OrWhere(func(q contractsorm.Query) contractsorm.Query {
					return q.Where("name", "rel_wcl_alice").WhereHas("Books", func(b contractsorm.Query) contractsorm.Query {
						return b.Where("name", "wcl_target")
					})
				})
			}).Get(&users))
			s.ElementsMatch([]string{"rel_wcl_alice", "rel_wcl_bob"}, namesOf(users))

			users = nil
			s.Nil(query.Query().Where("name", "like", "rel_wcl_%").Where(func(q contractsorm.Query) contractsorm.Query {
				return q.WhereDoesntHave("Books")
			}).Get(&users))
			s.ElementsMatch([]string{"rel_wcl_bob"}, namesOf(users))

			// A where closure after a parent whereHas carries only its own clauses (Laravel D1/D2
			// shape: `exists (...) or ("name" = ?)`), so the or-branch matches the user without
			// books; an inherited exists inside the group would drop bob.
			users = nil
			s.Nil(query.Query().Where("name", "like", "rel_wcl_%").WhereHas("Books").OrWhere(func(q contractsorm.Query) contractsorm.Query {
				return q.Where("name", "rel_wcl_bob")
			}).Get(&users))
			s.ElementsMatch([]string{"rel_wcl_alice", "rel_wcl_bob", "rel_wcl_carol"}, namesOf(users))
		})
	}
}

// TestOnlyTrashedIsPositionalInsideWhereHas_Rows: onlyTrashed inside a whereHas closure writes its
// "is not null" at its call position, so it joins the closure's or-group (Laravel X5 shape:
// `exists (... and ("users"."deleted_at" is not null and "name" = ? or "name" = ?))`). The book of the
// live user named in the or-branch is therefore matched too; a trailing predicate would drop it.
func (s *QueriesRelationshipsTestSuite) TestOnlyTrashedIsPositionalInsideWhereHas_Rows() {
	for driver, query := range s.queries {
		s.Run(driver, func() {
			trashed := &User{Name: "rel_otp_trashed"}
			s.Nil(query.Query().Create(trashed))
			s.Nil(query.Query().Relation(trashed, "Books").Save(&Book{Name: "otp_b1"}))
			live := &User{Name: "rel_otp_live"}
			s.Nil(query.Query().Create(live))
			s.Nil(query.Query().Relation(live, "Books").Save(&Book{Name: "otp_b2"}))
			other := &User{Name: "rel_otp_other"}
			s.Nil(query.Query().Create(other))
			s.Nil(query.Query().Relation(other, "Books").Save(&Book{Name: "otp_b3"}))
			_, err := query.Query().Delete(trashed)
			s.Nil(err)

			var books []Book
			s.Nil(query.Query().Where("name", "like", "otp_%").WhereHas("User", func(q contractsorm.Query) contractsorm.Query {
				return q.OnlyTrashed().Where("name", "rel_otp_trashed").OrWhere("name", "rel_otp_live")
			}).Order("name").Get(&books))

			names := make([]string, 0, len(books))
			for _, book := range books {
				names = append(names, book.Name)
			}
			s.Equal([]string{"otp_b1", "otp_b2"}, names)
		})
	}
}
