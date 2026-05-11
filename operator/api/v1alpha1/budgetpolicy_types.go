package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BudgetPolicySpec defines shared spend guardrail configuration that can be
// referenced by multiple ModelDeployments via spec.budget.policyRef.
// An inline budget in ModelDeploymentSpec.Budget always takes precedence.
type BudgetPolicySpec struct {
	// DailyUsdCap is the maximum USD spend per calendar day (UTC) for any
	// deployment referencing this policy. Must be a positive Quantity.
	// +kubebuilder:validation:XValidation:rule="self != '0'",message="dailyUsdCap must be positive"
	DailyUsdCap resource.Quantity `json:"dailyUsdCap"`

	// AlertThresholdPercent triggers an email + GUI alert (without pausing)
	// when spend reaches this fraction of DailyUsdCap. Default 80%.
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=100
	AlertThresholdPercent int32 `json:"alertThresholdPercent,omitempty"`

	// PauseOnExceed controls whether the operator automatically scales
	// referencing deployments to 0 when DailyUsdCap is exceeded.
	// Set to false only for always-on production deployments where you accept
	// the overage risk and prefer an alert-only response.
	// +kubebuilder:default=true
	PauseOnExceed bool `json:"pauseOnExceed"`

	// AlertEmails is a list of email addresses to notify on threshold breach.
	// Emails are sent via the alert channel configured at Helm install time.
	// +optional
	AlertEmails []string `json:"alertEmails,omitempty"`

	// MonthlyUsdCap is an optional hard cap across all calendar days in a month.
	// When set, the operator pauses all referencing deployments for the remainder
	// of the month if cumulative monthly spend exceeds this value.
	// +optional
	MonthlyUsdCap *resource.Quantity `json:"monthlyUsdCap,omitempty"`
}

// BudgetPolicyStatus is the observed state of a BudgetPolicy.
type BudgetPolicyStatus struct {
	// AffectedDeployments lists the names of ModelDeployments currently governed
	// by this policy.
	// +optional
	AffectedDeployments []string `json:"affectedDeployments,omitempty"`

	// TotalTodayUsdSpend is the aggregated spend across all AffectedDeployments
	// for the current calendar day (UTC). Updated every minute.
	// +optional
	TotalTodayUsdSpend string `json:"totalTodayUsdSpend,omitempty"`

	// PausedDeployments lists deployments currently paused due to this policy.
	// +optional
	PausedDeployments []string `json:"pausedDeployments,omitempty"`

	// Conditions provides detailed status information.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// BudgetPolicy condition types.
const (
	// ConditionBudgetHealthy is true when no referencing deployment has hit its cap today.
	ConditionBudgetHealthy = "BudgetHealthy"

	// ConditionAlertThresholdReached is true when spend has crossed alertThresholdPercent.
	ConditionAlertThresholdReached = "AlertThresholdReached"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bp,scope=Namespaced,categories=openserve
// +kubebuilder:printcolumn:name="Daily Cap",type=string,JSONPath=`.spec.dailyUsdCap`
// +kubebuilder:printcolumn:name="Today Spend",type=string,JSONPath=`.status.totalTodayUsdSpend`
// +kubebuilder:printcolumn:name="Affected",type=string,JSONPath=`.status.affectedDeployments`
// +kubebuilder:printcolumn:name="Pause On Exceed",type=boolean,JSONPath=`.spec.pauseOnExceed`

// +kubebuilder:object:root=true

// BudgetPolicy is a reusable spend guardrail configuration that can be applied
// to one or more ModelDeployments. Individual deployments can override it with
// inline budget fields in ModelDeploymentSpec.
type BudgetPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BudgetPolicySpec   `json:"spec,omitempty"`
	Status BudgetPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// BudgetPolicyList contains a list of BudgetPolicy.
type BudgetPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BudgetPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BudgetPolicy{}, &BudgetPolicyList{})
}
