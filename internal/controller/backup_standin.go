//go:build geasslivetest

package controller

import "os"

// A manager built with -tags geasslivetest can keep backups in an S3 server other than AWS's, such as a MinIO on the
// machine that runs a throwaway K3s, so that the whole of backups and restores can be tried without an AWS account.
// GEASS_LIVETEST_S3_ENDPOINT is its host and port. A release is not built with the tag, and has no such setting.
func init() {
	standInS3Endpoint = os.Getenv("GEASS_LIVETEST_S3_ENDPOINT")
}
