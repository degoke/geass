package ssh

import (
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
	"github.com/degoke/geass/pkg/platform"
)

// k3sConfigPath is where k3s reads its settings from. A file, rather than flags on the install
// command, keeps every node setting in one place that can be read back and rewritten.
const k3sConfigPath = "/etc/rancher/k3s/config.yaml"

// AgentConfig is what a worker node is told about itself when it joins: where it sits and what it is
// for, so it carries the right labels from the first moment it can be scheduled on.
type AgentConfig struct {
	// NodeLabels are "key=value" labels the node registers with.
	NodeLabels []string
	// NodeTaints are "key=value:Effect" taints the node registers with.
	NodeTaints []string
	// NodeExternalIP is the public address other sites reach this node on. Only a multi-site cluster
	// sets it. It is all a worker needs: the server's flannel-external-ip setting is what makes the
	// overlay tunnel use these addresses, and k3s agents reject that flag themselves ("flag provided
	// but not defined"), so it must never be written here.
	NodeExternalIP string
	// KubeletArgs are "key=value" kubelet settings.
	KubeletArgs []string
	// VPNAuthFile is the file K3s reads its tailnet credentials from (--vpn-auth-file). Only a Tailscale mesh sets it.
	// The file itself is written by the join step that brings the node onto the tailnet, with mode 0600, so the key is
	// never in this configuration.
	VPNAuthFile string
	// ExternalIPFromTailnet means the node advertises the address it has on the tailnet, which is only known once the
	// node is on it. The configuration is then written with that address appended by the machine, and NodeExternalIP
	// is not used.
	ExternalIPFromTailnet bool
}

// AgentOptions is what a worker's configuration is built from.
type AgentOptions struct {
	Placement *geassv1alpha1.GeassNodePlacement
	// MultiSite and ExternalIP are set for a cluster whose nodes reach each other over the internet.
	MultiSite  bool
	ExternalIP string
	// Labels and Taints are what a node of a pool registers with beyond its placement.
	Labels map[string]string
	Taints []corev1.Taint
	// Tailscale is set for a node that joins a Tailscale mesh. It then advertises its tailnet address and not ExternalIP.
	Tailscale *TailscaleJoin
}

// NewAgentConfig builds a worker's join-time configuration: its placement, how long its container logs
// are kept, and, in a multi-site cluster, the public address it advertises.
func NewAgentConfig(options AgentOptions) AgentConfig {
	config := AgentConfigFor(options.Placement)
	for key, value := range options.Labels {
		config.NodeLabels = append(config.NodeLabels, key+"="+value)
	}
	for _, taint := range options.Taints {
		config.NodeTaints = append(config.NodeTaints, taint.Key+"="+taint.Value+":"+string(taint.Effect))
	}
	slices.Sort(config.NodeLabels)
	slices.Sort(config.NodeTaints)
	config.NodeLabels = slices.Compact(config.NodeLabels)
	config.NodeTaints = slices.Compact(config.NodeTaints)
	config.KubeletArgs = slices.Clone(platform.KubeletLogArgs)
	switch {
	case options.Tailscale != nil:
		config.VPNAuthFile = mesh.AuthFile
		config.ExternalIPFromTailnet = true
	case options.MultiSite && options.ExternalIP != "":
		config.NodeExternalIP = options.ExternalIP
	}
	return config
}

// AgentConfigFor builds the placement part of a node's join-time settings.
func AgentConfigFor(placement *geassv1alpha1.GeassNodePlacement) AgentConfig {
	if placement == nil {
		return AgentConfig{}
	}
	node := corev1.Node{}
	platform.ApplyNodePlacement(&node, *placement)
	config := AgentConfig{}
	for key, value := range node.Labels {
		config.NodeLabels = append(config.NodeLabels, key+"="+value)
	}
	for _, taint := range node.Spec.Taints {
		config.NodeTaints = append(config.NodeTaints, taint.Key+"="+taint.Value+":"+string(taint.Effect))
	}
	slices.Sort(config.NodeLabels)
	slices.Sort(config.NodeTaints)
	return config
}

// IsZero reports whether there is nothing to write.
func (c AgentConfig) IsZero() bool {
	return len(c.NodeLabels) == 0 && len(c.NodeTaints) == 0 && c.NodeExternalIP == "" && len(c.KubeletArgs) == 0 &&
		c.VPNAuthFile == "" && !c.ExternalIPFromTailnet
}

// YAML renders the k3s configuration file.
func (c AgentConfig) YAML() string {
	values := map[string]any{}
	if len(c.NodeLabels) > 0 {
		values["node-label"] = c.NodeLabels
	}
	if len(c.NodeTaints) > 0 {
		values["node-taint"] = c.NodeTaints
	}
	if c.NodeExternalIP != "" {
		values["node-external-ip"] = c.NodeExternalIP
	}
	if len(c.KubeletArgs) > 0 {
		values["kubelet-arg"] = c.KubeletArgs
	}
	if c.VPNAuthFile != "" {
		values["vpn-auth-file"] = c.VPNAuthFile
	}
	raw, err := yaml.Marshal(values)
	if err != nil {
		return ""
	}
	return string(raw)
}

// writeConfigCommand is the shell command that installs the configuration file. The content goes in
// through a quoted here-document, so nothing in it is interpreted by the shell.
func (c AgentConfig) writeConfigCommand() string {
	var b strings.Builder
	b.WriteString("mkdir -p /etc/rancher/k3s && cat > " + k3sConfigPath + " <<'GEASS_K3S_CONFIG'\n")
	b.WriteString(c.YAML())
	b.WriteString("GEASS_K3S_CONFIG")
	if c.ExternalIPFromTailnet {
		// The address is the one the node was given on the tailnet, so the machine says it. The step that joined the
		// tailnet has checked that there is one.
		b.WriteString("\necho \"node-external-ip: $(tailscale ip -4 | head -n 1)\" >> " + k3sConfigPath)
	}
	return b.String()
}
