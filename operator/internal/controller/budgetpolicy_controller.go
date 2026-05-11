package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openservev1alpha1 "github.com/openserve/openserve/operator/api/v1alpha1"
	"github.com/openserve/openserve/operator/internal/budget"
)

// budgetReconcileInterval is how often the budget controller checks spend against caps.
// BigQuery billing export has ~1h latency; we query the metering table (near-realtime)
// for intra-day enforcement and BQ billing export for end-of-day reconciliation.
const budgetReconcileInterval = 5 * time.Minute

// BudgetPolicyReconciler reconciles BudgetPolicy objects and enforces spend caps
// across all ModelDeployments that reference a given policy.
//
// +kubebuilder:rbac:groups=openserve.io,resources=budgetpolicies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openserve.io,resources=budgetpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openserve.io,resources=modeldeployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=openserve.io,resources=modeldeployments/status,verbs=get;update;patch
type BudgetPolicyReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	Config       Config
	BudgetClient *budget.Client
}

func (r *BudgetPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var bp openservev1alpha1.BudgetPolicy
	if err := r.Get(ctx, req.NamespacedName, &bp); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	log.Info("reconciling BudgetPolicy", "dailyCap", bp.Spec.DailyUsdCap.String())

	results, err := r.BudgetClient.QueryTodaySpend(ctx)
	if err != nil {
		log.Error(err, "failed to query BigQuery spend")
		return ctrl.Result{RequeueAfter: budgetReconcileInterval}, nil
	}

	// Build a map of deploymentID → spend
	spendByDeployment := make(map[string]float64)
	for _, res := range results {
		spendByDeployment[res.DeploymentID] = res.EstUSDSpend
	}

	// List all ModelDeployments in the namespace
	var mdList openservev1alpha1.ModelDeploymentList
	if err := r.List(ctx, &mdList, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	capFloat := bp.Spec.DailyUsdCap.AsApproximateFloat64()
	alertThreshold := capFloat * float64(bp.Spec.AlertThresholdPercent) / 100.0
	var totalSpend float64
	var pausedDeployments, affectedDeployments []string

	for i := range mdList.Items {
		md := &mdList.Items[i]
		spend := spendByDeployment[md.Name]
		totalSpend += spend
		affectedDeployments = append(affectedDeployments, md.Name)

		if spend >= capFloat && bp.Spec.PauseOnExceed {
			if err := r.pauseDeployment(ctx, md); err != nil {
				log.Error(err, "failed to pause deployment", "deployment", md.Name)
			} else {
				pausedDeployments = append(pausedDeployments, md.Name)
			}
		}
	}

	// Update status
	bp.Status.TotalTodayUsdSpend = fmt.Sprintf("$%.4f", totalSpend)
	bp.Status.PausedDeployments = pausedDeployments
	bp.Status.AffectedDeployments = affectedDeployments

	alertReached := totalSpend >= alertThreshold
	apimeta.SetStatusCondition(&bp.Status.Conditions, metav1.Condition{
		Type:    openservev1alpha1.ConditionAlertThresholdReached,
		Status:  boolToConditionStatus(alertReached),
		Reason:  "SpendCheck",
		Message: fmt.Sprintf("total spend $%.4f vs cap $%.4f", totalSpend, capFloat),
	})

	// Re-queue every budgetReconcileInterval to keep spend checks continuous.
	return ctrl.Result{RequeueAfter: budgetReconcileInterval}, r.Status().Update(ctx, &bp)
}

// pauseDeployment scales a ModelDeployment to 0 and records the budget pause timestamp.
func (r *BudgetPolicyReconciler) pauseDeployment(ctx context.Context, md *openservev1alpha1.ModelDeployment) error {
	now := metav1.Now()
	md.Status.Phase = openservev1alpha1.DeploymentPhaseBudgetPaused
	md.Status.BudgetPausedAt = &now
	return r.Status().Update(ctx, md)
}

func (r *BudgetPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openservev1alpha1.BudgetPolicy{}).
		Complete(r)
}

// isExpired returns true if the given time is non-nil and in the past.
func isExpired(t *metav1.Time) bool {
	return t != nil && t.Time.Before(time.Now())
}

func boolToConditionStatus(b bool) metav1.ConditionStatus {
	if b {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}
