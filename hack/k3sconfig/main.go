// Command k3sconfig prints the K3s server configuration the installer writes for a first server. The throwaway K3s
// clusters that hack/ starts mount it, so they run with the settings a real cluster has (secrets encryption, log
// rotation) and not with a copy of them that could drift from what the installer does.
//
//	go run ./hack/k3sconfig
package main

import (
	"fmt"

	"github.com/degoke/geass/pkg/installer"
)

func main() {
	fmt.Print(installer.Network{}.ServerConfig())
}
