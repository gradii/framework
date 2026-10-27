package binding

import (
	"github.com/gradii/framework/contracts/binding"
	"github.com/gradii/framework/support/collect"
)

func Dependencies(bindings ...string) []string {
	var deps []string
	for _, bind := range bindings {
		deps = append(deps, binding.Bindings[bind].Dependencies...)
	}

	return collect.Diff(collect.Unique(deps), bindings)
}
