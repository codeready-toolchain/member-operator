package template

import (
	"fmt"

	templatev1 "github.com/openshift/api/template/v1"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/runtime"
)

// DecodeTemplate decodes bytes to Template object
func DecodeTemplate(decoder runtime.Decoder, tmplContent []byte) (*templatev1.Template, error) {
	obj, _, err := decoder.Decode(tmplContent, nil, nil)
	if err != nil {
		return nil, errs.Wrapf(err, "unable to decode template")
	}
	tmpl, ok := obj.(*templatev1.Template)
	if !ok {
		return nil, fmt.Errorf("unable to convert object type %T to Template, must be a v1.Template", obj)
	}
	return tmpl, nil
}
