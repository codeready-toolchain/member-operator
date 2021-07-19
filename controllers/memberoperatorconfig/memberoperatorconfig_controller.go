package memberoperatorconfig

import (
	"context"
	"os"

	"github.com/go-logr/logr"
	errs "github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	toolchainv1alpha1 "github.com/codeready-toolchain/api/api/v1alpha1"
	"github.com/codeready-toolchain/member-operator/pkg/autoscaler"
	"github.com/codeready-toolchain/member-operator/pkg/webhook/deploy"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&toolchainv1alpha1.MemberOperatorConfig{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&source.Kind{Type: &corev1.Secret{}},
			handler.EnqueueRequestsFromMapFunc(MapSecretToMemberOperatorConfig()),
			builder.WithPredicates(&predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Reconciler reconciles a MemberOperatorConfig object
type Reconciler struct {
	Client client.Client
	Log    logr.Logger
}

//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=memberoperatorconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=memberoperatorconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=toolchain.dev.openshift.com,resources=memberoperatorconfigs/finalizers,verbs=update

// Reconcile reads that state of the cluster for a MemberOperatorConfig object and makes changes based on the state read
// and what is in the MemberOperatorConfig.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *Reconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	reqLogger := log.FromContext(ctx)
	reqLogger.Info("Reconciling MemberOperatorConfig")

	crtConfig, err := loadLatest(r.Client, request.Namespace)
	if err != nil {
		return reconcile.Result{}, err
	}

	if crtConfig.Autoscaler().Deploy() {
		reqLogger.Info("(Re)Deploying autoscaling buffer")
		if err := autoscaler.Deploy(r.Client, r.Client.Scheme(), request.Namespace, crtConfig.Autoscaler().BufferMemory(), crtConfig.Autoscaler().BufferReplicas()); err != nil {
			return reconcile.Result{}, logAndWrapErr(reqLogger, err, "cannot deploy autoscaling buffer")
		}
		reqLogger.Info("(Re)Deployed autoscaling buffer")
	} else {
		deleted, err := autoscaler.Delete(r.Client, r.Client.Scheme(), request.Namespace)
		if err != nil {
			return reconcile.Result{}, logAndWrapErr(reqLogger, err, "cannot delete previously deployed autoscaling buffer")
		}
		if deleted {
			reqLogger.Info("Deleted previously deployed autoscaling buffer")
		} else {
			reqLogger.Info("Skipping deployment of autoscaling buffer")
		}
	}

	// By default the users' pods webhook will be deployed, however in some cases (eg. e2e tests) there can be multiple member operators
	// installed in the same cluster. In those cases only 1 webhook is needed because the MutatingWebhookConfiguration is a cluster-scoped resource and naming can conflict.
	if crtConfig.Webhook().Deploy() {
		webhookImage := os.Getenv("MEMBER_OPERATOR_WEBHOOK_IMAGE")
		reqLogger.Info("(Re)Deploying users' pods webhook")
		if err := deploy.Webhook(r.Client, r.Client.Scheme(), request.Namespace, webhookImage); err != nil {
			return reconcile.Result{}, logAndWrapErr(reqLogger, err, "cannot deploy mutating users' pods webhook")
		}
		reqLogger.Info("(Re)Deployed users' pods webhook")
	} else {
		reqLogger.Info("Skipping deployment of users' pods webhook")
	}

	return reconcile.Result{}, nil
}

func logAndWrapErr(logger logr.Logger, err error, msg string) error {
	logger.Error(err, msg)
	return errs.Wrap(err, msg)
}
