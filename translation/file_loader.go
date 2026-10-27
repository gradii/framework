package translation

import (
	"os"

	"github.com/gradii/framework/contracts/foundation"
	contractstranslation "github.com/gradii/framework/contracts/translation"
	"github.com/gradii/framework/errors"
	"github.com/gradii/framework/packages/paths"
	"github.com/gradii/framework/support/file"
)

type FileLoader struct {
	json  foundation.Json
	paths []string
}

func NewFileLoader(paths []string, json foundation.Json) contractstranslation.Loader {
	return &FileLoader{
		paths: paths,
		json:  json,
	}
}

func (f *FileLoader) Load(locale string, group string) (map[string]any, error) {
	for _, path := range f.paths {
		var val map[string]any
		fullPath := paths.Abs(path, locale, group+".json")
		if group == "*" {
			fullPath = paths.Abs(path, locale+".json")
		}

		if file.Exists(fullPath) {
			data, err := os.ReadFile(fullPath)
			if err != nil {
				return nil, err
			}
			if err = f.json.Unmarshal(data, &val); err != nil {
				return nil, err
			}

			return val, nil
		}
	}

	return nil, errors.LangFileNotExist
}
