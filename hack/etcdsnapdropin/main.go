// Command etcdsnapdropin prints what the tests that run K3s in Docker take from the code instead of keeping a copy of:
// the file that the installer's `etcd-snapshots enable` puts in K3s's configuration directory, or, with
// --policy <bucket>, the IAM policy that the bucket's access key needs.
package main

import (
	"fmt"
	"os"

	"github.com/degoke/geass/pkg/installer"
	"github.com/degoke/geass/pkg/platform"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--policy" {
		fmt.Println(platform.EtcdSnapshotsPolicy(os.Args[2]))
		return
	}
	fmt.Print(installer.EtcdSnapshotsDropIn())
}
