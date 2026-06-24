package installer

import _ "embed"

//go:embed assets/geass-operator.tar
var embeddedOperatorImageTar []byte

//go:embed assets/operator.yaml
var embeddedOperatorManifests []byte
