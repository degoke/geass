package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHelmReleasePodsAreNotConfusedWithOtherReleases(t *testing.T) {
	pod := func(name string, labels map[string]string) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", Labels: labels}}
	}
	for _, tc := range []struct {
		name    string
		pod     *corev1.Pod
		release string
		want    bool
	}{
		{"instance label", pod("anything-0", map[string]string{"app.kubernetes.io/instance": "redis"}), "redis", true},
		{"release label", pod("anything-0", map[string]string{"release": "redis"}), "redis", true},
		{"unlabelled pod named after the release", pod("redis-0", nil), "redis", true},
		{"release name is only a prefix of the word", pod("redisinsight-5d8f-abcde", nil), "redis", false},
		{"db1 is not db10", pod("db10-0", nil), "db1", false},
		{"another release's label wins over the name", pod("redis-cache-0", map[string]string{"app.kubernetes.io/instance": "redis-cache"}), "redis", false},
		{"another release label", pod("redis-0", map[string]string{"release": "other"}), "redis", false},
		{"other namespace", &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "redis-0", Namespace: "elsewhere"}}, "redis", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isHelmReleasePod(tc.pod, "ns", tc.release))
		})
	}
}
