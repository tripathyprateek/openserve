package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// APIKeyRole defines the permission level of an API key.
// +kubebuilder:validation:Enum=admin;developer;partner;viewer
type APIKeyRole string

const (
	APIKeyRoleAdmin     APIKeyRole = "admin"
	APIKeyRoleDeveloper APIKeyRole = "developer"
	APIKeyRolePartner   APIKeyRole = "partner"
	APIKeyRoleViewer    APIKeyRole = "viewer"
)

// APIKeySpec defines the desired state of an API key.
type APIKeySpec struct {
	// DisplayName is the human-readable label shown in the GUI.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName"`

	// Role determines what the key holder can do.
	// admin: full access including key management.
	// developer: create/call deployments; no key management.
	// partner: call specific deployments via inference endpoint only.
	// viewer: read-only access to deployments and usage dashboards.
	// +kubebuilder:default=developer
	Role APIKeyRole `json:"role"`

	// AllowedDeployments lists the ModelDeployment names this key may call.
	// Empty slice means all current and future deployments in this namespace.
	// For partner keys, always set an explicit list.
	// +optional
	AllowedDeployments []string `json:"allowedDeployments,omitempty"`

	// RateLimit defines per-key rate limits enforced at the gateway.
	// +kubebuilder:default={"requestsPerMinute": 60, "tokensPerMinute": 100000}
	RateLimit KeyRateLimit `json:"rateLimit"`

	// IPAllowlist is an optional list of CIDR ranges from which this key may be used.
	// Empty means unrestricted. Use for partner keys where the partner's egress IP is known.
	// +optional
	IPAllowlist []string `json:"ipAllowlist,omitempty"`

	// ExpiresAt is the RFC 3339 timestamp after which the key is automatically revoked.
	// Required for partner keys; optional for internal keys.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// Description is a free-text note shown in the audit log and GUI.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	Description string `json:"description,omitempty"`
}

// KeyRateLimit defines rate limits for a single API key.
type KeyRateLimit struct {
	// RequestsPerMinute is the maximum number of inference requests per minute.
	// +kubebuilder:default=60
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	RequestsPerMinute int32 `json:"requestsPerMinute"`

	// TokensPerMinute is the maximum number of combined input+output tokens per minute.
	// +kubebuilder:default=100000
	// +kubebuilder:validation:Minimum=1000
	// +kubebuilder:validation:Maximum=10000000
	TokensPerMinute int32 `json:"tokensPerMinute"`
}

// APIKeyStatus is the observed state of an API key.
type APIKeyStatus struct {
	// KeyID is the stable, non-secret identifier for this key (used in audit logs).
	// Format: "kid_<12 random chars>". Never contains the raw key value.
	// +optional
	KeyID string `json:"keyId,omitempty"`

	// Prefix is the first 12 characters of the raw key (e.g. "openserve_li"),
	// shown in the GUI for identification without exposing the secret.
	// +optional
	Prefix string `json:"prefix,omitempty"`

	// LastUsedAt is the timestamp of the most recent successful request using this key.
	// Updated on a best-effort basis (may lag by up to 60s).
	// +optional
	LastUsedAt *metav1.Time `json:"lastUsedAt,omitempty"`

	// Active is false when the key has been revoked or has expired.
	// +optional
	Active bool `json:"active,omitempty"`

	// Conditions provides detailed status information.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ak,scope=Namespaced,categories=openserve
// +kubebuilder:printcolumn:name="Display Name",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Role",type=string,JSONPath=`.spec.role`
// +kubebuilder:printcolumn:name="Key ID",type=string,JSONPath=`.status.keyId`
// +kubebuilder:printcolumn:name="Active",type=boolean,JSONPath=`.status.active`
// +kubebuilder:printcolumn:name="Last Used",type=date,JSONPath=`.status.lastUsedAt`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.spec.expiresAt`

// +kubebuilder:object:root=true

// APIKey represents a scoped API key that grants access to openserve inference endpoints.
// The raw key value is generated on creation, shown once via the control-api, and never
// stored — only its Argon2id hash is persisted in Postgres and referenced by this CR.
type APIKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   APIKeySpec   `json:"spec,omitempty"`
	Status APIKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// APIKeyList contains a list of APIKey.
type APIKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []APIKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&APIKey{}, &APIKeyList{})
}
