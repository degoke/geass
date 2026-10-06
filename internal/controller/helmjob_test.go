package controller

import "testing"

func TestIsClusterMinIOHelmJob(t *testing.T) {
	if !isClusterMinIOHelmJob("helm-install-geass-minio") {
		t.Fatal("expected exact k3s install job name to match")
	}
	if !isClusterMinIOHelmJob("helm-install-geass-minio-123") {
		t.Fatal("expected prefixed install job name to match")
	}
	if isClusterMinIOHelmJob("backup-geass-minio") {
		t.Fatal("loose substring match must not enqueue unrelated jobs")
	}
}
