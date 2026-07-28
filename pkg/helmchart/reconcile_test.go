package helmchart

import (
	"context"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const testInstallJobName = "helm-install-test-chart"

func TestIsReady(t *testing.T) {
	t.Parallel()

	chart := func(jobName string) *helmv1.HelmChart {
		return &helmv1.HelmChart{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-chart",
				Namespace: platform.HelmChartNamespace,
			},
			Status: helmv1.HelmChartStatus{
				JobName: jobName,
			},
		}
	}

	tests := []struct {
		name    string
		chart   *helmv1.HelmChart
		job     *batchv1.Job
		want    bool
		wantErr bool
	}{
		{
			name:  "nil chart",
			chart: nil,
			want:  false,
		},
		{
			name:  "no job name",
			chart: chart(""),
			want:  false,
		},
		{
			name:  "install job missing",
			chart: chart(testInstallJobName),
			want:  false,
		},
		{
			name:  "install job completed",
			chart: chart(testInstallJobName),
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: testInstallJobName, Namespace: platform.HelmChartNamespace},
				Status:     batchv1.JobStatus{Succeeded: 1},
			},
			want: true,
		},
		{
			name:  "install job still running",
			chart: chart(testInstallJobName),
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: testInstallJobName, Namespace: platform.HelmChartNamespace},
				Status:     batchv1.JobStatus{Active: 1},
			},
			want: false,
		},
		{
			name:  "install job failed",
			chart: chart(testInstallJobName),
			job: &batchv1.Job{
				ObjectMeta: metav1.ObjectMeta{Name: testInstallJobName, Namespace: platform.HelmChartNamespace},
				Status:     batchv1.JobStatus{Failed: 1},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			objs := []runtime.Object{}
			if tt.job != nil {
				objs = append(objs, tt.job)
			}
			c := fake.NewClientBuilder().WithRuntimeObjects(objs...).Build()

			got, err := IsReady(context.Background(), c, tt.chart)
			if (err != nil) != tt.wantErr {
				t.Fatalf("IsReady() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("IsReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsReadyRequiresClient(t *testing.T) {
	t.Parallel()

	chart := &helmv1.HelmChart{
		ObjectMeta: metav1.ObjectMeta{Name: "test-chart", Namespace: platform.HelmChartNamespace},
		Status: helmv1.HelmChartStatus{
			JobName: testInstallJobName,
		},
	}

	if _, err := IsReady(context.Background(), nil, chart); err == nil {
		t.Fatal("expected error when client is nil")
	}
}
