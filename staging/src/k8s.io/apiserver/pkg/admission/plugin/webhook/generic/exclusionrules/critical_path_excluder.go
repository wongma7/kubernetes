package exclusionrules

import (
	"encoding/json"
	v1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiserver/pkg/admission"
	"k8s.io/klog/v2"
	"os"
)

// Enables you to pass a config file to kube-api-server
const ADMISSION_WEBHOOK_EXCLUSION_ENV_VAR = "EKS_PATCH_EXCLUSION_RULES_FILE"

type CriticalPathExcluder struct {
	exclusionRules []ExclusionRule
}

type ExclusionRule struct {
	// APIGroup is the API groups the resources belong to.
	// Required.
	APIGroup string `json:"apiGroup,omitempty"`

	// APIVersions is the API versions the resources belong to.
	// Required.
	APIVersion string `json:"apiVersion,omitempty"`

	// Name is a list of object names this rule applies to.
	// '*' for name only allowed for Leases in kube-node-lease namespace otherwise rule is ignored
	// Required.
	Name []string `json:"name,omitempty"`

	// Kind to exclude.
	Kind string `json:"kind,omitempty"`

	// Namespace is the namespaces this rule applies to.
	Namespace string `json:"namespace,omitempty"`

	// Scope specifies the scope of this rule.
	// Valid values are "Cluster", "Namespaced"
	// "Cluster" means that only cluster-scoped resources will match this rule.
	// Namespace API objects are cluster-scoped.
	// "Namespaced" means that only namespaced resources will match this rule.
	// Namespace field required for "Namespaced" scope otherwise namespace field disallowed
	Scope *v1.ScopeType `json:"scope,omitempty"`
}

func NewCriticalPathExcluder() CriticalPathExcluder {
	exclusionRulesFromFile := readFile()
	filteredExclusionRules := filterValidRules(exclusionRulesFromFile)
	return CriticalPathExcluder{
		exclusionRules: filteredExclusionRules,
	}
}

func readFile() []ExclusionRule {
	//Default values for backwards compatability for eks-d
	namespace := v1.NamespacedScope
	defaultExclusion := []ExclusionRule{
		{
			APIGroup:   "coordination.k8s.io",
			APIVersion: "v1",
			Kind:       "Lease",
			Namespace:  "kube-system",
			Name:       []string{"kube-controller-manager", "kube-scheduler"},
			Scope:      &namespace,
		},
		{
			APIGroup:   "",
			APIVersion: "v1",
			Kind:       "Endpoints",
			Namespace:  "kube-system",
			Name:       []string{"kube-controller-manager", "kube-scheduler"},
			Scope:      &namespace,
		},
	}

	if fileLocation, ok := os.LookupEnv(ADMISSION_WEBHOOK_EXCLUSION_ENV_VAR); ok {
		file, err := os.ReadFile(fileLocation)
		if err != nil {
			klog.Errorf("Error reading %v file: %v", ADMISSION_WEBHOOK_EXCLUSION_ENV_VAR, err)
			return defaultExclusion
		}
		data := []ExclusionRule{}
		err = json.Unmarshal(file, &data)
		if err != nil {
			klog.Errorf("Error converting %v file to exclusion rules: %v", ADMISSION_WEBHOOK_EXCLUSION_ENV_VAR, err)
			return defaultExclusion
		}
		klog.Infof("Successfully found and loaded %v exclusion rules", len(data))
		return data
	}
	klog.Info("No environment variable set, using default exclusion rules")
	return defaultExclusion
}

func filterValidRules(inputExclusionRules []ExclusionRule) []ExclusionRule {
	// * only allowed for name if targeting leases in kube-node-lease
	// * not allowed for Scope, APIVersion, APIGroup, Namespace or Kind
	filteredRules := []ExclusionRule{}
	for _, rule := range inputExclusionRules {
		if rule.Scope == nil {
			klog.Errorf("Invalid webhook admission exclusion rule, scope not set")
			continue
		}

		// No wildcards
		if *rule.Scope == v1.AllScopes || rule.APIGroup == "*" || rule.APIVersion == "*" || rule.Namespace == "*" || rule.Kind == "*" {
			klog.Errorf("Invalid webhook admission exclusion rule, wildcard not allowed, skipping rule")
			continue
		}
		if contains(rule.Name, "*") && isDisallowedNameWildcard(rule) {
			klog.Errorf("Invalid webhook admission exclusion rule, wildcard only allowed for name for Lease in kube-node-lease, skipping rule")
			continue
		}
		// No namespace if cluster scoped
		if *rule.Scope == v1.ClusterScope && rule.Namespace != "" {
			klog.Errorf("Invalid webhook admission exclusion rule, cannot set namespace with Cluster Scope")
			continue
		}
		// Required namespace if Namespaced scope
		if *rule.Scope == v1.NamespacedScope && rule.Namespace == "" {
			klog.Errorf("Invalid webhook admission exclusion rule, must set namespace with Namespaced Scope")
			continue
		}
		filteredRules = append(filteredRules, rule)
	}
	return filteredRules
}

func isDisallowedNameWildcard(rule ExclusionRule) bool {
	return !(rule.APIGroup == "coordination.k8s.io" && rule.APIVersion == "v1" && rule.Kind == "Lease" && rule.Namespace == "kube-node-lease")
}

func contains(s []string, str string) bool {
	for _, v := range s {
		if v == str {
			return true
		}
	}

	return false
}

func (excludor CriticalPathExcluder) ShouldSkipWebhookDueToExclusionRules(attr admission.Attributes) bool {
	for _, r := range excludor.exclusionRules {
		m := Matcher{ExclusionRule: r, Attr: attr}
		if m.Matches() {
			return true
		}
	}
	return false
}
