package middleware

import (
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/goforj/web"
)

// StaticConfig configures static file serving.
type StaticConfig struct {
	Skipper    Skipper
	Root       string
	Index      string
	HTML5      bool
	Browse     bool
	IgnoreBase bool
	Filesystem http.FileSystem
}

// DefaultStaticConfig is the default static config.
var DefaultStaticConfig = StaticConfig{
	Skipper: DefaultSkipper,
	Index:   "index.html",
}

const staticIndexHTML = `
<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><title>{{ .Name }}</title></head>
<body>
<h1>{{ .Name }}</h1>
<ul>
{{ range .Files }}
<li><a href="{{ .Name }}">{{ .Name }}</a></li>
{{ end }}
</ul>
</body>
</html>
`

// Static serves static content from the provided root.
func Static(root string) web.Middleware {
	config := DefaultStaticConfig
	config.Root = root
	return StaticWithConfig(config)
}

// StaticWithConfig serves static content using config.
func StaticWithConfig(config StaticConfig) web.Middleware {
	if config.Root == "" {
		config.Root = "."
	}
	if config.Skipper == nil {
		config.Skipper = DefaultStaticConfig.Skipper
	}
	if config.Index == "" {
		config.Index = DefaultStaticConfig.Index
	}
	if config.Filesystem == nil {
		config.Filesystem = http.Dir(config.Root)
		config.Root = "."
	}

	tpl, err := template.New("index").Parse(staticIndexHTML)
	if err != nil {
		panic(fmt.Errorf("web: %w", err))
	}

	return func(next web.Handler) web.Handler {
		return func(r web.Context) error {
			if config.Skipper(r) {
				return next(r)
			}

			p := r.Request().URL.Path
			if strings.HasSuffix(r.Path(), "*") {
				p = r.Param("*")
			}
			p, err = url.PathUnescape(p)
			if err != nil {
				return err
			}
			name := path.Join(config.Root, path.Clean("/"+p))

			if config.IgnoreBase {
				routePath := path.Base(strings.TrimRight(r.Path(), "/*"))
				baseURLPath := path.Base(p)
				if baseURLPath == routePath {
					idx := strings.LastIndex(name, routePath)
					if idx >= 0 {
						name = name[:idx] + strings.Replace(name[idx:], routePath, "", 1)
					}
				}
			}

			file, err := config.Filesystem.Open(name)
			if err != nil {
				if !isIgnorableOpenFileError(err) {
					return err
				}
				if err = next(r); err == nil {
					return nil
				}
				if !config.HTML5 {
					return err
				}
				file, err = config.Filesystem.Open(path.Join(config.Root, config.Index))
				if err != nil {
					return err
				}
			}
			defer file.Close()

			info, err := file.Stat()
			if err != nil {
				return err
			}

			if info.IsDir() {
				index, err := config.Filesystem.Open(path.Join(name, config.Index))
				if err == nil {
					defer index.Close()
					indexInfo, statErr := index.Stat()
					if statErr != nil {
						return statErr
					}
					http.ServeContent(r.ResponseWriter(), r.Request(), indexInfo.Name(), indexInfo.ModTime(), index)
					return nil
				}
				if config.Browse {
					return listDir(tpl, name, file, r)
				}
				return next(r)
			}

			http.ServeContent(r.ResponseWriter(), r.Request(), info.Name(), info.ModTime(), file)
			return nil
		}
	}
}

func isIgnorableOpenFileError(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission)
}

func listDir(tpl *template.Template, name string, dir http.File, r web.Context) error {
	files, err := dir.Readdir(-1)
	if err != nil {
		return err
	}
	r.SetHeader("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Name  string
		Files []struct{ Name string }
	}{
		Name: name,
	}
	for _, file := range files {
		item := struct{ Name string }{Name: file.Name()}
		if file.IsDir() {
			item.Name += "/"
		}
		data.Files = append(data.Files, item)
	}
	return tpl.Execute(r.ResponseWriter(), data)
}
