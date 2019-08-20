package nstemplateset

import (
	"context"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/config"
	"github.com/codeready-toolchain/member-operator/pkg/templates"
	projectv1 "github.com/openshift/api/project/v1"
	"github.com/operator-framework/operator-sdk/pkg/predicate"
	errs "github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"github.com/go-logr/logr"
)

var log = logf.Log.WithName("controller_nstemplateset")

func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileNSTemplateSet{client: mgr.GetClient(), scheme: mgr.GetScheme()}
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
	client client.Client
	scheme *runtime.Scheme
}

func (r *ReconcileNSTemplateSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling NSTemplateSet")

	// Fetch the NSTemplateSet instance
	nsTeplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Namespace: config.GetOperatorNamespace(), Name: request.Name}, nsTeplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Info("no matching NSTemplateSet found")
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	log.Info("processing NSTemplateSet...")
	// TODO: use values from the request
	values := map[string]string{
		"PROJECT_NAME":    "foo",
		"ADMIN_USER_NAME": "developer",
	}
	if err := r.processAndApply(nsTeplSet.Spec.TierName, request.Namespace, reqLogger, values); err != nil {
		return reconcile.Result{}, err
	}

	// TODO set NSTemplateSet.Status appropriately
	return reconcile.Result{}, nil
}

func (r *ReconcileNSTemplateSet) processAndApply(tierName, ns string, reqLogger logr.Logger, values map[string]string) error {

	objs, err := templates.Process(r.scheme, reqLogger, tierName, values)
	if err != nil {
		return err
	}

	return r.applyResources(objs, ns)
}

func (r *ReconcileNSTemplateSet) applyResources(objs []runtime.RawExtension, namespace string) error {
	for _, obj := range objs {
		if obj.Object == nil {
			log.Info("template object is nil")
			continue
		}
		gvk := obj.Object.GetObjectKind().GroupVersionKind()
		log.Info("processing object", "version", gvk.Version, "kind", gvk.Kind)
		if err := r.client.Create(context.TODO(), obj.Object); err != nil {
			if errors.IsAlreadyExists(err) {
				// if client failed to create all resources(few created, few remaining) in the first run, then to avoid
				// continuous failing scenario due to resource already existing for later runs
				continue
			}
			log.Error(err, "unable to create resource", "type", gvk.Kind)
			return errs.Wrapf(err, "unable to create resource of type %s", gvk.Kind)
		}
	}
	return nil
}
