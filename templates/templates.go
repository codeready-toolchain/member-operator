package templates

import "fmt"

// TODO stop gap solution to get template content, once NSTemplateTier template is available, this should be removed.
// Remove `templates` directory all togather.  Also remove `make generate` target
// Remove gitignore entry - templates/bindata.go
func GetTemplateContent(tmplName string) ([]byte, error) {
	return Asset(fmt.Sprintf("templates/%s.yaml", tmplName))
}
