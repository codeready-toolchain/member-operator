package nstemplateset

import (
	"context"
	"fmt"
	"time"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	"github.com/codeready-toolchain/member-operator/templates"
	"github.com/go-logr/logr"
	authv1 "github.com/openshift/api/authorization/v1"
	projectv1 "github.com/openshift/api/project/v1"
	templatev1 "github.com/openshift/api/template/v1"
	authv1client "github.com/openshift/client-go/authorization/clientset/versioned/typed/authorization/v1"
	projectv1client "github.com/openshift/client-go/project/clientset/versioned/typed/project/v1"
	templatev1client "github.com/openshift/client-go/template/clientset/versioned/typed/template/v1"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	result, err := r.ensureTemplates(reqLogger, nsTeplSet)
	return result, err
}

func (r *ReconcileNSTemplateSet) ensureTemplates(logger logr.Logger, nsTeplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	tmpl := templates.GetTemplate(nsTeplSet.Spec.TierName).Templates[0]
	tmplContent := templates.GetTemplateContent(tmpl.TemplateFile)
	// fmt.Println("VN: content=" + string(tmplContent))

	var codecs = serializer.NewCodecFactory(r.scheme)
	decode := codecs.UniversalDeserializer().Decode
	// TODO any use of middle return param
	obj, _, err := decode(tmplContent, nil, nil)
	fmt.Println("VN: obj=", obj)
	if err != nil {
		fmt.Println("VN: err1=", err)
		return reconcile.Result{}, nil
	}
	template := obj.(*templatev1.Template)
	fmt.Println("VN: len=", len(template.Objects))
	fmt.Println("VN: bytes=", string(template.Objects[0].Raw))
	fmt.Println("VN: obj.obj=", template.Objects[0].Object)

	// r.createTemplate(template)
	r.createResources(template)

	return reconcile.Result{}, nil
}

func (r *ReconcileNSTemplateSet) createResources(template *templatev1.Template) {
	resObj := template.Objects[0]
	fmt.Println("VN: obj.raw=", resObj.Raw)
	// fmt.Println("VN: obj.obj=", resObj.Object)

	// client := authv1client.New(r.templateClient.RESTClient())
	// payload := []byte(`{"apiVersion":"authorization.openshift.io/v1","kind":"RoleBinding","metadata":{"labels":{"app":"fabric8-tenant-user","provider":"fabric8","version":"123abc"},"name":"user-edit","namespace":"toolchain-member-operator"},"roleRef":{"name":"edit"},"subjects":[{"kind":"User","name":"system:admin"}],"userNames":["system:admin"]}`)
	payload := resObj.Raw

	processed := &authv1.RoleBinding{}
	err := r.authClient.RESTClient().Post().
		Namespace("toolchain-member-operator").
		Resource("rolebindings").
		Body(payload).
		Do().
		Into(processed)
	fmt.Println("VN: create, err=", err)
}

func (r *ReconcileNSTemplateSet) createTemplate(template *templatev1.Template) {
	processed := &templatev1.Template{}
	fmt.Println("VN: template process")
	err := r.templateClient.RESTClient().Post().
		Namespace("toolchain-member-operator").
		Resource("templates").
		Body(template).
		Do().
		Into(processed)
	// return processed, fmt.Errorf("could not process template: %v", err)
	fmt.Println("VN: template process, err=", err)

	fmt.Println("VN: template create")
	created := &templatev1.Template{}
	err = r.templateClient.RESTClient().Post().
		Namespace("toolchain-member-operator").
		Resource("processedtemplates").
		Body(processed).
		Do().
		Into(created)
	fmt.Println("VN: template create, err=", err)
}

func (r *ReconcileNSTemplateSet) ensureNamespaces(logger logr.Logger, nsTeplSet *toolchainv1alpha1.NSTemplateSet) (reconcile.Result, error) {
	// TODO create multiple namespaces
	template := templates.GetTemplate(nsTeplSet.Spec.TierName).Templates[0]
	name := fmt.Sprintf("%s-%s", nsTeplSet.Name, template.Name)
	project := &projectv1.Project{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: name}, project); err != nil {
		if errors.IsNotFound(err) {
			projectReq := newProjectReqeust(name)
			if err := r.client.Create(context.TODO(), projectReq); err != nil {
				if errors.IsAlreadyExists(err) {
					// possible when one Create ProjectRequest already in progress and we tried to make another, requeue to complete first Create ProjectReqeust
					return reconcile.Result{Requeue: true, RequeueAfter: defaultRequeueAfter}, nil
				}
				return reconcile.Result{}, errs.Wrapf(err, "failed to create project '%s'", name)
			}
			logger.Info("project created", "name", name)
			// requeue to check if Project is created (please note what we created above is ProjectReqeust)
			return reconcile.Result{Requeue: true, RequeueAfter: defaultRequeueAfter}, nil
		}
		return reconcile.Result{}, errs.Wrapf(err, "failed to get project '%s'", name)
	}

	if len(project.ObjectMeta.OwnerReferences) <= 0 {
		if err := controllerutil.SetControllerReference(nsTeplSet, project, r.scheme); err != nil {
			return reconcile.Result{}, err
		}
		if err := r.client.Update(context.TODO(), project); err != nil {
			return reconcile.Result{}, err
		}
		logger.Info("project upldated with owner", "name", name)
		return reconcile.Result{}, nil
	}

	if project.Status.Phase != corev1.NamespaceActive {
		// In case project getting deleted (delete in progress), GET project will return project with status terminating.
		// Here, requeue to later recrete project after deletion
		return reconcile.Result{Requeue: true, RequeueAfter: defaultRequeueAfter}, nil
	}
	logger.Info("project is active", "name", name)
	return reconcile.Result{}, nil
}

func newProjectReqeust(name string) *projectv1.ProjectRequest {
	projectReq := &projectv1.ProjectRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}
	return projectReq
}
