/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Functional Source License, Version 1.1, ALv2 Future License
(FSL-1.1-ALv2, the "License"); you may not use this file except in compliance
with the License. You may obtain a copy of the License at LICENSE or
https://fsl.software/FSL-1.1-ALv2.template.md.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassAppIngressSpec defines external access for an app.
type GeassAppIngressSpec struct {
	// Host is the ingress hostname.
	// +optional
	Host string `json:"host,omitempty"`

	// Path is the HTTP path prefix.
	// +kubebuilder:default=/
	// +optional
	Path string `json:"path,omitempty"`

	// TLSEnabled requests TLS termination via cert-manager when true.
	// +optional
	TLSEnabled bool `json:"tlsEnabled,omitempty"`

	// DNSVerification records the desired DNS verification behavior.
	// +optional
	DNSVerification bool `json:"dnsVerification,omitempty"`
	// Rules allows multiple host/path routes. Host and Path remain the shorthand form.
	// +optional
	Rules []GeassAppIngressRule `json:"rules,omitempty"`
}

type GeassAppIngressRule struct {
	Host string `json:"host"`
	Path string `json:"path,omitempty"`
}

// GeassAppImageSource identifies an existing image artifact.
type GeassAppImageSource struct {
	Image string `json:"image"`
	// PullSecret references a Secret containing registry pull credentials.
	// +optional
	PullSecret *corev1.LocalObjectReference `json:"pullSecret,omitempty"`
}

// GeassAppGitSource identifies a GitHub source revision.
type GeassAppGitSource struct {
	// ConnectionRef identifies a project GitHub connection Secret.
	ConnectionRef corev1.LocalObjectReference `json:"connectionRef"`
	Repository    string                      `json:"repository"`
	Branch        string                      `json:"branch"`
	// Commit pins the source when supplied.
	// +optional
	Commit string `json:"commit,omitempty"`
	// Dockerfile is the path of the Dockerfile, relative to Context. A path that already starts with
	// Context is taken as relative to the repository root.
	// +kubebuilder:default=Dockerfile
	// +optional
	Dockerfile string `json:"dockerfile,omitempty"`
	// Context is the build context (the root directory of the build) relative to the repository root. A
	// leading slash means the repository root, and a path that leaves the repository is refused.
	// +kubebuilder:default=.
	// +optional
	Context string `json:"context,omitempty"`
	// WaitForCI requires successful checks for the selected commit.
	// +optional
	WaitForCI bool `json:"waitForCI,omitempty"`
	// WatchPatterns limits webhook-triggered builds to pushes that change a file they select. They are
	// gitignore-style, matched from the repository root, and a "!" pattern takes files back out. Empty means
	// every push to the branch builds.
	// +optional
	WatchPatterns []string `json:"watchPatterns,omitempty"`
}

// GeassAppFunctionSource runs inline source code on a managed language runtime.
type GeassAppFunctionSource struct {
	// Runtime selects the language runtime that executes Code.
	// +kubebuilder:validation:Enum=bun;node;python
	Runtime string `json:"runtime"`
	// Code is the program. It must serve HTTP on the port in the PORT environment variable.
	// +kubebuilder:validation:MaxLength=262144
	Code string `json:"code"`
	// Mode says what Code is. "http" (the default) is a program that serves HTTP itself. "event" is a
	// handler, a function that is given an event for each request and returns the response, with Geass
	// serving HTTP for it.
	// +kubebuilder:validation:Enum=http;event
	// +kubebuilder:default=http
	// +optional
	Mode string `json:"mode,omitempty"`
	// Triggers call the function on a schedule. Each run is an HTTP request to the function, which wakes
	// it first when it is asleep.
	// +kubebuilder:validation:MaxItems=8
	// +listType=map
	// +listMapKey=name
	// +optional
	Triggers []GeassFunctionTrigger `json:"triggers,omitempty"`
}

// GeassFunctionTrigger calls a function on a schedule.
type GeassFunctionTrigger struct {
	// Name identifies the trigger and names its CronJob.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`
	Name string `json:"name"`
	// Schedule is a cron expression in UTC, such as "*/15 * * * *" or "@daily".
	// +kubebuilder:validation:MaxLength=100
	Schedule string `json:"schedule"`
	// Path is the path that is called. It defaults to /.
	// +kubebuilder:validation:MaxLength=256
	// +kubebuilder:validation:Pattern=`^/.*$`
	// +optional
	Path string `json:"path,omitempty"`
	// Payload is the JSON body of the request. It defaults to {"trigger":"cron","name":"<name>"}.
	// +kubebuilder:validation:MaxLength=4096
	// +optional
	Payload string `json:"payload,omitempty"`
}

// GeassAppSource is a mutually exclusive image, Git, or function source.
type GeassAppSource struct {
	// +optional
	Image *GeassAppImageSource `json:"image,omitempty"`
	// +optional
	Git *GeassAppGitSource `json:"git,omitempty"`
	// +optional
	Function *GeassAppFunctionSource `json:"function,omitempty"`
}

// GeassAppRegistrySpec configures the destination image registry.
type GeassAppRegistrySpec struct {
	Repository    string                       `json:"repository,omitempty"`
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
}

// GeassAppBuildSettings configures source builds.
type GeassAppBuildSettings struct {
	Registry GeassAppRegistrySpec `json:"registry,omitempty"`
	Cache    bool                 `json:"cache,omitempty"`
	// ArgsFrom contains Secret references. Values are never copied to status or logs.
	ArgsFrom []corev1.LocalObjectReference `json:"argsFrom,omitempty"`
}

// GeassAppMetricsSpec configures Prometheus scraping.
type GeassAppMetricsSpec struct {
	// Enabled creates a ServiceMonitor when true.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Path is the metrics HTTP path.
	// +kubebuilder:default=/metrics
	// +optional
	Path string `json:"path,omitempty"`

	// Port is the metrics port name or number on the Service.
	// +kubebuilder:default=http
	// +optional
	Port string `json:"port,omitempty"`
}

type GeassAppLogsSpec struct {
	// ArchiveStoreRef selects the GeassObjectStore used for durable logs/history.
	// +optional
	ArchiveStoreRef *corev1.LocalObjectReference `json:"archiveStoreRef,omitempty"`
	// RetentionHours bounds archived application and deployment logs.
	// +kubebuilder:validation:Minimum=1
	// +optional
	RetentionHours int32 `json:"retentionHours,omitempty"`
}

// GeassAppBuildSpec configures how a source image or build system starts an app.
type GeassAppBuildSpec struct {
	// Command overrides the image entrypoint when set.
	// +optional
	Command []string `json:"command,omitempty"`
	// Args overrides image arguments when set.
	// +optional
	Args []string `json:"args,omitempty"`
	// WorkingDir sets the container working directory.
	// +optional
	WorkingDir string               `json:"workingDir,omitempty"`
	Registry   GeassAppRegistrySpec `json:"registry,omitempty"`
	Cache      bool                 `json:"cache,omitempty"`
	// ArgsFrom contains Secret references. Values are never copied to status or logs.
	ArgsFrom []corev1.LocalObjectReference `json:"argsFrom,omitempty"`
}

// GeassAppDeploySpec configures rollout and health behavior for an app.
type GeassAppDeploySpec struct {
	// Enabled controls whether Geass creates or maintains workload resources.
	// False creates a configurable draft without deploying it.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
	// RestartPolicy controls pod restart behavior. Kubernetes deployments support Always.
	// +kubebuilder:validation:Enum=Always
	// +kubebuilder:default=Always
	// +optional
	RestartPolicy corev1.RestartPolicy `json:"restartPolicy,omitempty"`
	// ReadinessProbe is the probe used to determine whether traffic is safe.
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`
	// LivenessProbe restarts an unhealthy container.
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`
	StartupProbe  *corev1.Probe `json:"startupProbe,omitempty"`
	// FailureThreshold is the number of failed rollout health observations before action.
	// +kubebuilder:validation:Minimum=1
	// +optional
	FailureThreshold int32 `json:"failureThreshold,omitempty"`
	// PauseOnFailure pauses further rollout processing when true.
	// +optional
	PauseOnFailure bool `json:"pauseOnFailure,omitempty"`
	// RollbackOnFailure requests rollback metadata to be recorded after a failed rollout.
	// +optional
	RollbackOnFailure bool `json:"rollbackOnFailure,omitempty"`
}

// GeassAppAutoscalingSpec configures a Kubernetes HorizontalPodAutoscaler.
type GeassAppAutoscalingSpec struct {
	// MinReplicas is the lower bound for autoscaling.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`
	// MaxReplicas is the upper bound for autoscaling.
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`
	// CPUUtilization is the target average CPU utilization percentage.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	TargetCPUUtilization *int32 `json:"targetCPUUtilization,omitempty"`
}

// GeassAppScaleToZeroSpec lets a service sleep while nothing uses it. When it has had no requests for IdleSeconds
// it is stopped, and the next request wakes it: the request is held for the few seconds it takes to start, and
// then answered. It needs the cluster's serverless add-on and a public host for the service, because it is the
// traffic from that host that wakes it.
type GeassAppScaleToZeroSpec struct {
	// Enabled turns scale to zero on.
	Enabled bool `json:"enabled"`
	// IdleSeconds is how long without a request before the service is stopped.
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=86400
	// +kubebuilder:default=300
	// +optional
	IdleSeconds int32 `json:"idleSeconds,omitempty"`
	// MaxReplicas is how many copies it may scale up to under load.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=3
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`
}

// GeassAppSpec defines the desired state of GeassApp.
type GeassAppSpec struct {
	// Project is the owning project.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// Environment is the project environment for this app.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Required
	Environment GeassEnvironment `json:"environment"`

	// Source selects exactly one image or Git source.
	// +kubebuilder:validation:Required
	Source GeassAppSource `json:"source"`

	// Replicas is the desired Deployment replica count.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Port is the primary container and Service port.
	// +kubebuilder:default=8080
	// +kubebuilder:validation:Minimum=1
	// +optional
	Port int32 `json:"port,omitempty"`

	// Ingress configures Traefik ingress exposure.
	// +optional
	Ingress GeassAppIngressSpec `json:"ingress,omitempty"`

	// Env is a list of environment variables injected into the container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom loads environment variables from referenced ConfigMaps and Secrets in the target namespace.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// SharedVariableRefs selects project shared variables to inject into this app.
	// When empty, all shared variables for the app environment remain available for backward compatibility.
	// +kubebuilder:validation:items:Pattern=`^[A-Z_][A-Z0-9_]*$`
	// +optional
	SharedVariableRefs []string `json:"sharedVariableRefs,omitempty"`

	// ConfigMapRefs lists existing ConfigMaps in the target namespace to mount under /config/refs/<name>.
	// +optional
	ConfigMapRefs []corev1.LocalObjectReference `json:"configMapRefs,omitempty"`

	// SecretRefs lists existing Secrets in the target namespace to mount under /secrets/refs/<name>.
	// +optional
	SecretRefs []corev1.LocalObjectReference `json:"secretRefs,omitempty"`

	// ConfigData is key/value data stored in an owned ConfigMap.
	// +optional
	ConfigData map[string]string `json:"configData,omitempty"`

	// SecretData is key/value data stored in an owned Secret.
	// +optional
	SecretData map[string]string `json:"secretData,omitempty"`

	// SecretRef points to the system-namespace Secret containing dashboard-managed app secrets.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// Metrics configures optional Prometheus scraping.
	// +optional
	Metrics GeassAppMetricsSpec `json:"metrics,omitempty"`
	// Logs configures durable log archival without exposing log contents in status.
	// +optional
	Logs GeassAppLogsSpec `json:"logs,omitempty"`

	// Build configures image command and working directory behavior.
	// +optional
	Build GeassAppBuildSpec `json:"build,omitempty"`

	// Deploy configures rollout, restart, and health behavior.
	// +optional
	Deploy GeassAppDeploySpec `json:"deploy,omitempty"`

	// Resources configures CPU and memory requests/limits for the app container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Autoscaling configures optional CPU-based horizontal pod autoscaling.
	// +optional
	Autoscaling *GeassAppAutoscalingSpec `json:"autoscaling,omitempty"`

	// RunOn asks for nodes with particular tags, on top of the project's region.
	// +optional
	RunOn GeassWorkloadPlacement `json:"runOn,omitempty"`

	// ScaleToZero lets the service sleep when it is idle and wakes it on the next request. It replaces
	// Replicas and Autoscaling as what decides how many copies run.
	// +optional
	ScaleToZero *GeassAppScaleToZeroSpec `json:"scaleToZero,omitempty"`
}

// GeassAppStatus defines the observed state of GeassApp.
type GeassAppStatus struct {
	// TargetNamespace is the namespace workloads were created in.
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// URL is the primary ingress URL when configured.
	// +optional
	URL string `json:"url,omitempty"`
	// ResolvedImage is the image the active deployment runs: for a Git source, the repository the build
	// pushed to at the digest it pushed (repository@sha256:...), which a node can pull.
	ResolvedImage string `json:"resolvedImage,omitempty"`
	// DNSName is the hostname users should point at the Traefik entrypoint.
	DNSName string `json:"dnsName,omitempty"`
	// TraefikTarget is the observed ingress target when available.
	TraefikTarget string `json:"traefikTarget,omitempty"`
	// ActiveBuild references the Git build whose image is deployed.
	ActiveBuild string `json:"activeBuild,omitempty"`
	// CurrentBuild references the Git build for the service's current settings. A build is named by a hash of
	// its settings, so a service that goes back to earlier settings uses an older build than the newest: this
	// is the one a push or a rebuild runs again, and it can be one that is not deployed yet.
	CurrentBuild        string `json:"currentBuild,omitempty"`
	DNSVerified         bool   `json:"dnsVerified,omitempty"`
	DNSMessage          string `json:"dnsMessage,omitempty"`
	HealthCheckFailures int32  `json:"healthCheckFailures,omitempty"`
	RolloutPaused       bool   `json:"rolloutPaused,omitempty"`

	// ReconcileRetry tracks provision retries before reconciliation gives up.
	// +optional
	ReconcileRetry GeassReconcileRetryStatus `json:"reconcileRetry,omitempty"`

	// Provision tracks deploy attempts and errors for this resource.
	// +optional
	Provision GeassProvisionStatus `json:"provision,omitempty"`

	// conditions represent the current state of the GeassApp resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// GeassApp is the Schema for the geassapps API.
type GeassApp struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassAppSpec `json:"spec"`

	// +optional
	// +optional
	Status GeassAppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GeassAppList contains a list of GeassApp.
type GeassAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassApp{}, &GeassAppList{})
		return nil
	})
}
