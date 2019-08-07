package templates

import (
	"fmt"
	"html/template"
	"io"

	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var log = logf.Log.WithName("template")

var tmpls = template.New("")

func init() {
	initTemplates()
	// loadTemplates()
}

func initTemplates() {
	templates = make(map[string]TemplateType)
	templates["basic"] = TemplateType{
		Name: "basic",
		Templates: []Template{
			Template{
				Name:         "user",
				Revision:     "abc1234",
				TemplateFile: "templates/basic-user-template.yml",
			},
		},
	}
}

func loadTemplates() {
	for _, path := range AssetNames() {
		fmt.Printf("loading template %s\n", path)
		content, err := Asset(path)
		if err != nil {
			log.Error(err, "Failed to parse template, path: %s, err: %v", path, err)
		}
		tmpls.New(path).Parse(string(content))
	}
}

func ProcessTemplate(w io.Writer, tmplName string, param interface{}) error {
	return tmpls.ExecuteTemplate(w, tmplName, param)
}

func GetTemplateContent(tmplName string) []byte {
	content, err := Asset(tmplName)
	fmt.Println("VN: get tmpl, err=", err)
	return content
}

func GetTemplate(typeName string) TemplateType {
	return templates[typeName]
}

var templates map[string]TemplateType

type TemplateType struct {
	Name      string
	Templates []Template
}

type Template struct {
	Name         string
	Revision     string
	TemplateFile string
}
