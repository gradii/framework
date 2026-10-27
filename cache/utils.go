package cache

import (
	"github.com/gradii/framework/contracts/config"
)

func prefix(config config.Config) string {
	p := config.GetString("cache.prefix")
	if p == "" {
		return ""
	}

	return p + ":"
}
