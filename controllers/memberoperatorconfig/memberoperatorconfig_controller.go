package memberoperatorconfig

import (
	"context"
	"os"

	"github.com/go-logr/logr"
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

	crtConfig, err := loadLatest(r.Client)
	if err != nil {
		return reconcile.Result{}, err
	}

	if err := r.handleAutoscalerDeploy(reqLogger, crtConfig, request.Namespace); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.handleUserPodsWebhookDeploy(reqLogger, crtConfig, request.Namespace); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

func (r *Reconciler) handleAutoscalerDeploy(logger logr.Logger, cfg Configuration, namespace string) error {
	if cfg.Autoscaler().Deploy() {
		logger.Info("(Re)Deploying autoscaling buffer")
		if err := autoscaler.Deploy(r.Client, r.Client.Scheme(), namespace, cfg.Autoscaler().BufferMemory(), cfg.Autoscaler().BufferReplicas()); err != nil {
			logger.Error(err, "failed to deploy autoscaling buffer")
			return err
		}
		logger.Info("(Re)Deployed autoscaling buffer")
	} else {
		deleted, err := autoscaler.Delete(r.Client, r.Client.Scheme(), namespace)
		if err != nil {
			logger.Error(err, "failed to delete autoscaling buffer")
			return err
		}
		if deleted {
			logger.Info("Deleted previously deployed autoscaling buffer")
		} else {
			logger.Info("Skipping deployment of autoscaling buffer")
		}
	}
	return nil
}

func (r *Reconciler) handleUserPodsWebhookDeploy(logger logr.Logger, cfg Configuration, namespace string) error {
	// By default the users' pods webhook will be deployed, however in some cases (eg. e2e tests) there can be multiple member operators
	// installed in the same cluster. In those cases only 1 webhook is needed because the MutatingWebhookConfiguration is a cluster-scoped resource and naming can conflict.
	if cfg.Webhook().Deploy() {
		webhookImage := os.Getenv("MEMBER_OPERATOR_WEBHOOK_IMAGE")
		logger.Info("(Re)Deploying users' pods webhook")
		if err := deploy.Webhook(r.Client, r.Client.Scheme(), namespace, webhookImage); err != nil {
			logger.Error(err, "failed to deploy mutating users' pods webhook")
			return err
		}
		logger.Info("(Re)Deployed users' pods webhook")
	} else {
		logger.Info("Skipping deployment of users' pods webhook")
	}
	return nil
}
