package main

import "strings"

type Stubs struct{}

func (s Stubs) ValidationFacade(pkg string) string {
	content := `package DummyPackage

import (
	"github.com/gradii/framework/contracts/validation"
)

func Validation() validation.Validation {
	return App().MakeValidation()
}
`

	return strings.ReplaceAll(content, "DummyPackage", pkg)
}
