// Package all registers every compute provider Geass has. Import it for its effect: a provider is available once its
// package has been imported, and this is the one place that lists them.
package all

import (
	_ "github.com/degoke/geass/pkg/compute/aws"
	_ "github.com/degoke/geass/pkg/compute/azure"
	_ "github.com/degoke/geass/pkg/compute/digitalocean"
	_ "github.com/degoke/geass/pkg/compute/gcp"
	_ "github.com/degoke/geass/pkg/compute/hetzner"
	_ "github.com/degoke/geass/pkg/compute/linode"
	_ "github.com/degoke/geass/pkg/compute/oci"
	_ "github.com/degoke/geass/pkg/compute/ovhcloud"
	_ "github.com/degoke/geass/pkg/compute/scaleway"
	_ "github.com/degoke/geass/pkg/compute/upcloud"
	_ "github.com/degoke/geass/pkg/compute/vultr"
	_ "github.com/degoke/geass/pkg/compute/webhook"
)
