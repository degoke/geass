package platform

// KubeletLogArgs bound how much of a container's output the node keeps. Kubelet's default is 10Mi in
// five files, which a chatty service fills in minutes, so the live logs the dashboard reads from the
// node would hold almost no history. They are written into every node's k3s configuration at join.
var KubeletLogArgs = []string{"container-log-max-size=50Mi", "container-log-max-files=5"}

const (
	// WireGuardPort is where k3s's WireGuard flannel backend listens. A multi-site cluster opens it
	// between nodes instead of the VXLAN port, because VXLAN is not encrypted and the nodes' traffic
	// crosses the public internet.
	WireGuardPort = "51820"
	// VXLANPort is the default overlay's port, used when every node is on one private network.
	VXLANPort = "8472"
	// WireGuardBackend is the k3s flannel backend that encrypts node-to-node traffic.
	WireGuardBackend = "wireguard-native"
)
