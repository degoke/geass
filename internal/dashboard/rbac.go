package dashboard

// Dashboard RBAC markers are picked up by controller-gen via make manifests.

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch
