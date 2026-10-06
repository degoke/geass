package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestAppDeletionNamespaces(t *testing.T) {
	app := func(project, env string, statusNS string) *geassv1alpha1.GeassApp {
		return &geassv1alpha1.GeassApp{
			Spec: geassv1alpha1.GeassAppSpec{
				Project:     project,
				Environment: geassv1alpha1.GeassEnvironment(env),
			},
			Status: geassv1alpha1.GeassAppStatus{TargetNamespace: statusNS},
		}
	}

	t.Run("spec only", func(t *testing.T) {
		got := appDeletionNamespaces(app("payments", "dev", ""))
		if len(got) != 1 || got[0] != "payments-dev" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("status only when spec placement invalid", func(t *testing.T) {
		got := appDeletionNamespaces(app("", "dev", "payments-production"))
		if len(got) != 1 || got[0] != "payments-production" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("dedupes matching spec and status", func(t *testing.T) {
		got := appDeletionNamespaces(app("payments", "dev", "payments-dev"))
		if len(got) != 1 || got[0] != "payments-dev" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("cleans both after environment move", func(t *testing.T) {
		got := appDeletionNamespaces(app("payments", "staging", "payments-dev"))
		if len(got) != 2 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("ignores empty status", func(t *testing.T) {
		a := app("payments", "dev", " ")
		a.ObjectMeta = metav1.ObjectMeta{Name: "api"}
		got := appDeletionNamespaces(a)
		if len(got) != 1 || got[0] != "payments-dev" {
			t.Fatalf("got %v", got)
		}
	})
}
