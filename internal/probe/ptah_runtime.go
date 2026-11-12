package probe

import (
	"context"
	"io/fs"
	"sync"

	"ptah.run/core/goschema"
	"ptah.run/core/schemamodel"
	"ptah.run/engine"
	"ptah.run/engine/builtin"
	"ptah.run/migration/lint"
)

var bundledRuntime = sync.OnceValues(func() (*engine.Runtime, error) { return builtin.New() })

func parseGoDir(dir string) (*schemamodel.Database, error) {
	runtime, err := bundledRuntime()
	if err != nil {
		return nil, err
	}
	return goschema.ParseDir(runtime.Annotations(), dir)
}

func lintFS(fsys fs.FS, opts lint.Options) ([]lint.Finding, error) {
	runtime, err := bundledRuntime()
	if err != nil {
		return nil, err
	}
	return lint.LintFS(context.Background(), runtime, fsys, opts)
}
