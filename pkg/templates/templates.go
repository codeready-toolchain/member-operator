package templates

func init() {
	// Todo implement a cache, to store all the templates from host cluster and template tier and invalidate/sync it when needed.
	initTemplates()
}

func initTemplates() {
	templates = make(map[string]TemplateType)
	templates["basic"] = TemplateType{
		Templates: []Template{
			{
				Type:         "basic",
				Revision:     "abc1234",
				TemplateFile: "basic-user-template.yml",
			},
		},
	}
}

func GetTemplateContent(tmplName string) ([]byte, error) {
	return Asset("pkg/templates/" + tmplName)
}

func GetTemplate(typeName string) (TemplateType, bool) {
	t, found := templates[typeName]
	return t, found
}

var templates map[string]TemplateType

type TemplateType struct {
	Templates []Template
}

type Template struct {
	Type         string
	Revision     string
	TemplateFile string
}
