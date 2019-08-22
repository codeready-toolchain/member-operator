package nstemplateset

import (
	"context"
	"fmt"
	"github.com/go-logr/logr"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/pkg/apis/toolchain/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	projectv1 "github.com/openshift/api/project/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
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

	// Watch for changes to primary resource
	err = c.Watch(&source.Kind{Type: &toolchainv1alpha1.NSTemplateSet{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileNSTemplateSet{}

type ReconcileNSTemplateSet struct {
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a NSTemplateSet object and makes changes based on the state read
// and what is in the NSTemplateSet.Spec
func (r *ReconcileNSTemplateSet) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling NSTemplateSet")

	// Fetch the NSTemplateSet instance
	nsTmplSet := &toolchainv1alpha1.NSTemplateSet{}
	err := r.client.Get(context.TODO(), request.NamespacedName, nsTmplSet)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	err = r.ensureNamespaces(reqLogger, nsTmplSet)
	return reconcile.Result{}, err
}

func (r *ReconcileNSTemplateSet) ensureNamespaces(logger logr.Logger, nsTmplSet *toolchainv1alpha1.NSTemplateSet) error {
	name := nsTmplSet.GetName()
	labels := map[string]string{
		"owner": name,
	}
	opts := client.MatchingLabels(labels)
	projects := &projectv1.ProjectList{}
	if err := r.client.List(context.TODO(), opts, projects); err != nil {
		// TODO status handling
		return err
	}

	nsForProvision := findNsForProvision(projects.Items, nsTmplSet.Spec.Namespaces, name)
	for _, ns := range nsForProvision {
		// TODO call template processing for ns
		log.Info("provisionng namespace", "namespace", ns)
	}

	return nil
}

func findNsForProvision(projects []projectv1.Project, namespaces []toolchainv1alpha1.Namespace, userName string) []toolchainv1alpha1.Namespace {
	missing := make([]toolchainv1alpha1.Namespace, 0, 0)
	for _, ns := range namespaces {
		nsName := fmt.Sprintf("%s-%s", userName, ns.Type)
		found := findProject(projects, nsName, ns.Revision)
		if !found {
			missing = append(missing, ns)
		}
	}
	return missing
}

func findProject(projects []projectv1.Project, projectName, revision string) bool {
	for _, project := range projects {
		if project.GetName() == projectName && project.GetLabels()["revision"] == revision {
			return true
		}
	}
	return false
}
