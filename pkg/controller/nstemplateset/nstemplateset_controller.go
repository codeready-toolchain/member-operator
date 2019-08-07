package nstemplateset

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	"github.com/codeready-toolchain/member-operator/pkg/templates"
	"github.com/go-logr/logr"
	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	authv1client "github.com/openshift/client-go/authorization/clientset/versioned/typed/authorization/v1"
	projectv1client "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	"github.com/openshift/library-go/pkg/template/generator"
	"github.com/openshift/library-go/pkg/template/templateprocessing"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_nstemplateset")
var defaultRequeueAfter = time.Duration(time.Second * 5)

func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	templateClient, err := templatev1client.NewForConfig(mgr.GetConfig())
	if err != nil {
		fmt.Println("VN: tmplClient, err=", err)
	}
	authClient, err := authv1client.NewForConfig(mgr.GetConfig())
	if err != nil {
		fmt.Println("VN: authClient, err=", err)
	}
	projectClient, err := projectv1client.NewForConfig(mgr.GetConfig())
	if err != nil {
		fmt.Println("VN: projectClient, err=", err)
	}

	return &ReconcileNSTemplateSet{client: mgr.GetClient(), scheme: mgr.GetScheme(), templateClient: templateClient, authClient: authClient, projectClient: projectClient}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("nstemplateset-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// add watch for primary resource
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
	if err != nil {
		return err
	}

	// add watch for secondary resource
	h := &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &toolchainv1alpha1.NSTemplateSet{},
	}
	if err := c.Watch(&source.Kind{Type: &projectv1.Project{}}, h); err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileNSTemplateSet{}

type ReconcileNSTemplateSet struct {
	client         client.Client
	scheme         *runtime.Scheme
	templateClient *templatev1client.TemplateV1Client
	authClient     *authv1client.AuthorizationV1Client
	projectClient  *projectv1client.ProjectV1Client
}

// TODO set NSTemplateSet.Status appropriately
func (r *ReconcileNSTemplateSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling NSTemplateSet")

	// Fetch the NSTemplateSet instance
	nsTeplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: config.GetOperatorNamespace(), Name: request.Name}, nsTeplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// result, err := r.ensureNamespaces(reqLogger, nsTeplSet)
	result, err := r.applyTemplates(reqLogger, nsTeplSet)
	return result, err
}

// applyTemplate applies the given template
func (r *ReconcileNSTemplateSet) applyTemplates(reqLogger logr.Logger, nsTeplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	// TODO: retrieve those values from the reconciliation context
	projectName := "foo"
	username := "developer"
	tmplFile := templates.GetTemplate(nsTeplSet.Spec.TierName).Templates[0].TemplateFile
	tmplContent := templates.GetTemplateContent(tmplFile)

	// infos, err := f.NewBuilder().
	// 	WithScheme(scheme, scheme.PrioritizedVersionsAllGroups()...).
	// 	LocalParam(true).
	// 	Stream(r, filename).
	// 	Do().
	// 	Infos()
	// if err != nil {
	// 	return fmt.Errorf("failed to read input object (not a Template?): %v", err)
	// }
	codecs := serializer.NewCodecFactory(r.scheme)
	decode := codecs.UniversalDeserializer().Decode
	// TODO any use of middle return param
	obj, _, err := decode(tmplContent, nil, nil)
	if err != nil {
		return reconcile.Result{}, err
	}
	tmpl := obj.(*templatev1.Template)
	// if len(infos) > 1 {
	// 	// in order to run validation on the input given to us by a user, we only support the processing
	// 	// of one template in a list. For instance, we want to be able to fail when a user does not give
	// 	// a parameter that the template wants or when they give a parameter the template doesn't need,
	// 	// as this may indicate that they have mis-used `oc process`. This is much less complicated when
	// 	// we process at most one template.
	// 	log.Warnf("%d input templates found, but only the first will be processed\n", len(infos))
	// }
	// obj, ok := infos[0].Object.(*templatev1.Template)
	// if !ok {
	// 	return errs.Errorf("template is not valid")
	// }
	if errs := injectUserVars(map[string]string{
		"PROJECT_NAME":    projectName,
		"ADMIN_USER_NAME": username,
	}, tmpl, false); errs != nil {
		return reconcile.Result{}, err
	}
	tmpl, err = processTemplateLocally(tmpl, r.scheme)
	if err != nil {
		return reconcile.Result{}, err
	}
	// return nil
	return r.createResources(tmpl.Objects, projectName)
}

// injectUserVars injects user specified variables into the Template
func injectUserVars(values map[string]string, t *templatev1.Template, ignoreUnknownParameters bool) error {
	for param, val := range values {
		v := templateprocessing.GetParameterByName(t, param)
		if v != nil {
			v.Value = val
			v.Generate = ""
		} else if !ignoreUnknownParameters {
			return fmt.Errorf("unknown parameter name %q\n", param)
		}
	}
	return nil
}

// processTemplateLocally applies the same logic that a remote call would make but makes no
// connection to the server.
func processTemplateLocally(tpl *templatev1.Template, scheme *runtime.Scheme) (*templatev1.Template, error) {
	processor := templateprocessing.NewProcessor(map[string]generator.Generator{
		"expression": generator.NewExpressionValueGenerator(rand.New(rand.NewSource(time.Now().UnixNano()))),
	})
	if errs := processor.Process(tpl); len(errs) > 0 {
		return nil, errs[0] // TODO: use errors.NewAggregate later
	}
	var externalResultObj templatev1.Template
	if err := scheme.Convert(tpl, &externalResultObj, nil); err != nil {
		return nil, fmt.Errorf("unable to convert template to external template object: %v", err)
	}
	return &externalResultObj, nil
}

func (r *ReconcileNSTemplateSet) createResources(objs []runtime.RawExtension, namespace string) (reconcile.Result, error) {
	for _, obj := range objs {
		if obj.Object == nil {
			log.Info("template object is nil")
			continue
		}
		gvk := obj.Object.GetObjectKind().GroupVersionKind()
		log.Info("processing object of group/kind '%v/%v'", gvk.Version, gvk.Kind)
		switch gvk.Kind {
		case "ProjectRequest":
			if err := r.client.Create(context.TODO(), obj.Object); err != nil {
				// projectClient, err := projectv1client.NewForConfig(config)
				// if err != nil {
				return reconcile.Result{}, errs.Wrapf(err, "failed to init the project client")
			}
			// processed := &projectv1.ProjectRequest{}
			// err = projectClient.RESTClient().Post().
			// 	Resource("projectrequests").
			// 	Body(obj.Object).
			// 	Do().
			// 	Into(processed)
			// err = projectClient.ProjectRequests().Create(processed)
			// if err != nil {
			// 	return errs.Wrapf(err, "failed to create the Project resource")
			// }
			// case "RoleBinding":
			// 	authClient, err := authv1client.NewForConfig(config)
			// 	if err != nil {
			// 		return errs.Wrapf(err, "failed to init the auth client")
			// 	}
			// 	processed := &authv1.RoleBinding{}
			// 	err = authClient.RESTClient().Post().
			// 		Namespace(namespace).
			// 		Resource("rolebindings").
			// 		Body(obj.Object).
			// 		Do().
			// 		Into(processed)
			// 	if err != nil {
			// 		return errs.Wrapf(err, "failed to create the RoleBinding resource")
			// 	}
		}
	}
	return reconcile.Result{}, nil
}

// func (r *ReconcileNSTemplateSet) ensureTemplates(logger logr.Logger, nsTeplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
// 	tmpl := templates.GetTemplate(nsTeplSet.Spec.TierName).Templates[0]
// 	tmplContent := templates.GetTemplateContent(tmpl.TemplateFile)
// 	// fmt.Println("VN: content=" + string(tmplContent))

// 	var codecs = serializer.NewCodecFactory(r.scheme)
// 	decode := codecs.UniversalDeserializer().Decode
// 	// TODO any use of middle return param
// 	obj, _, err := decode(tmplContent, nil, nil)
// 	fmt.Println("VN: obj=", obj)
// 	if err != nil {
// 		fmt.Println("VN: err1=", err)
// 		return reconcile.Result{}, nil
// 	}
// 	template := obj.(*templatev1.Template)
// 	fmt.Println("VN: len=", len(template.Objects))
// 	fmt.Println("VN: bytes=", string(template.Objects[0].Raw))
// 	fmt.Println("VN: obj.obj=", template.Objects[0].Object)

// 	// r.createTemplate(template)
// 	r.createResources(template)

// 	return reconcile.Result{}, nil
// }

// func (r *ReconcileNSTemplateSet) createResources(template *templatev1.Template) {
// 	resObj := template.Objects[0]
// 	fmt.Println("VN: obj.raw=", resObj.Raw)
// 	// fmt.Println("VN: obj.obj=", resObj.Object)

// 	// client := authv1client.New(r.templateClient.RESTClient())
// 	// payload := []byte(`{"apiVersion":"authorization.openshift.io/v1","kind":"RoleBinding","metadata":{"labels":{"app":"fabric8-tenant-user","provider":"fabric8","version":"123abc"},"name":"user-edit","namespace":"toolchain-member-operator"},"roleRef":{"name":"edit"},"subjects":[{"kind":"User","name":"system:admin"}],"userNames":["system:admin"]}`)
// 	payload := resObj.Raw

// 	processed := &authv1.RoleBinding{}
// 	err := r.authClient.RESTClient().Post().
// 		Namespace("toolchain-member-operator").
// 		Resource("rolebindings").
// 		Body(payload).
// 		Do().
// 		Into(processed)
// 	fmt.Println("VN: create, err=", err)
// }

// func (r *ReconcileNSTemplateSet) createTemplate(template *templatev1.Template) {
// 	processed := &templatev1.Template{}
// 	fmt.Println("VN: template process")
// 	err := r.templateClient.RESTClient().Post().
// 		Namespace("toolchain-member-operator").
// 		Resource("templates").
// 		Body(template).
// 		Do().
// 		Into(processed)
// 	// return processed, fmt.Errorf("could not process template: %v", err)
// 	fmt.Println("VN: template process, err=", err)

// 	fmt.Println("VN: template create")
// 	created := &templatev1.Template{}
// 	err = r.templateClient.RESTClient().Post().
// 		Namespace("toolchain-member-operator").
// 		Resource("processedtemplates").
// 		Body(processed).
// 		Do().
// 		Into(created)
// 	fmt.Println("VN: template create, err=", err)
// }

// func (r *ReconcileNSTemplateSet) ensureNamespaces(logger logr.Logger, nsTeplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
// 	// TODO create multiple namespaces
// 	template := templates.GetTemplate(nsTeplSet.Spec.TierName).Templates[0]
// 	name := fmt.Sprintf("%s-%s", nsTeplSet.Name, template.Name)
// 	project := &projectv1.Project{}
// 	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: name}, project); err != nil {
// 		if errors.IsNotFound(err) {
// 			projectReq := newProjectReqeust(name)
// 			if err := r.client.Create(context.TODO(), projectReq); err != nil {
// 				if errors.IsAlreadyExists(err) {
// 					// possible when one Create ProjectRequest already in progress and we tried to make another, requeue to complete first Create ProjectReqeust
// 					return reconcile.Result{Requeue: true, RequeueAfter: defaultRequeueAfter}, nil
// 				}
// 				return reconcile.Result{}, errs.Wrapf(err, "failed to create project '%s'", name)
// 			}
// 			logger.Info("project created", "name", name)
// 			// requeue to check if Project is created (please note what we created above is ProjectReqeust)
// 			return reconcile.Result{Requeue: true, RequeueAfter: defaultRequeueAfter}, nil
// 		}
// 		return reconcile.Result{}, errs.Wrapf(err, "failed to get project '%s'", name)
// 	}

// 	if len(project.ObjectMeta.OwnerReferences) <= 0 {
// 		if err := controllerutil.SetControllerReference(nsTeplSet, project, r.scheme); err != nil {
// 			return reconcile.Result{}, err
// 		}
// 		if err := r.client.Update(context.TODO(), project); err != nil {
// 			return reconcile.Result{}, err
// 		}
// 		logger.Info("project upldated with owner", "name", name)
// 		return reconcile.Result{}, nil
// 	}

// 	if project.Status.Phase != corev1.NamespaceActive {
// 		// In case project getting deleted (delete in progress), GET project will return project with status terminating.
// 		// Here, requeue to later recrete project after deletion
// 		return reconcile.Result{Requeue: true, RequeueAfter: defaultRequeueAfter}, nil
// 	}
// 	logger.Info("project is active", "name", name)
// 	return reconcile.Result{}, nil
// }

// func newProjectReqeust(name string) *projectv1.ProjectRequest {
// 	projectReq := &projectv1.ProjectRequest{
// 		ObjectMeta: metav1.ObjectMeta{
// 			Name: name,
// 		},
// 	}
// 	return projectReq
// }
