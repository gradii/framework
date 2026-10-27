package main

import "strings"

type Stubs struct{}

func (s Stubs) ViewFacade(pkg string) string {
	content := `package DummyPackage

import (
	"github.com/gradii/framework/contracts/view"
)

func View() view.View {
	return App().MakeView()
}
`

	return strings.ReplaceAll(content, "DummyPackage", pkg)
}
