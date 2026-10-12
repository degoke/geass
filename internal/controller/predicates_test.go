package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func projectWith(mutate func(*geassv1alpha1.GeassProject)) *geassv1alpha1.GeassProject {
	project := &geassv1alpha1.GeassProject{ObjectMeta: metav1.ObjectMeta{Name: "shop", Generation: 1}}
	if mutate != nil {
		mutate(project)
	}
	return project
}

func TestAProjectIsNotWokenByItsOwnStatus(t *testing.T) {
	pred := specOrMetadataChanged()
	before := projectWith(nil)
	update := func(mutate func(*geassv1alpha1.GeassProject)) bool {
		return pred.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: projectWith(mutate)})
	}

	require.False(t, update(func(p *geassv1alpha1.GeassProject) {
		p.Status.WebhookDeliveries = []geassv1alpha1.GeassWebhookDelivery{{WebhookID: "abcd1234"}}
	}), "a webhook delivery is recorded in status all the time")
	require.True(t, update(func(p *geassv1alpha1.GeassProject) { p.Generation = 2 }), "the spec changed")
	require.True(t, update(func(p *geassv1alpha1.GeassProject) { p.Annotations = map[string]string{"geass.dev/retry": "1"} }))
	require.True(t, update(func(p *geassv1alpha1.GeassProject) { p.Finalizers = []string{platform.FinalizerProject} }), "a finalizer added is the update that lets the next pass go on")
	now := metav1.Now()
	require.True(t, update(func(p *geassv1alpha1.GeassProject) { p.DeletionTimestamp = &now }))
}

func TestDependentsOnlyHearAboutAProjectSpecOrReadiness(t *testing.T) {
	pred := readyOrSpecChanged()
	ready := func(status metav1.ConditionStatus) *geassv1alpha1.GeassProject {
		return projectWith(func(p *geassv1alpha1.GeassProject) {
			p.Status.Conditions = []metav1.Condition{{Type: platform.ConditionReady, Status: status}}
		})
	}
	update := func(oldP, newP *geassv1alpha1.GeassProject) bool {
		return pred.Update(event.UpdateEvent{ObjectOld: oldP, ObjectNew: newP})
	}

	withDeliveries := ready(metav1.ConditionTrue)
	withDeliveries.Status.WebhookDeliveries = []geassv1alpha1.GeassWebhookDelivery{{WebhookID: "abcd1234"}}
	require.False(t, update(ready(metav1.ConditionTrue), withDeliveries), "apps are not requeued for each webhook delivery")
	require.True(t, update(ready(metav1.ConditionFalse), ready(metav1.ConditionTrue)), "an app waiting for its project hears that it is ready")
	require.True(t, update(projectWith(nil), ready(metav1.ConditionFalse)))
	spec := ready(metav1.ConditionTrue)
	spec.Generation = 2
	require.True(t, update(ready(metav1.ConditionTrue), spec))

	cluster := &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "c"}, Status: geassv1alpha1.GeassClusterStatus{
		Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}},
	}}
	flipped := cluster.DeepCopy()
	flipped.Status.Conditions[0].Status = metav1.ConditionFalse
	require.True(t, pred.Update(event.UpdateEvent{ObjectOld: cluster, ObjectNew: flipped}), "the same rule serves a cluster")
}

func TestAnArchiveOnlyHearsThatItsDatabaseIsGoing(t *testing.T) {
	pred := databaseGoneOrDeleting()
	db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Name: "orders"}}
	deleting := db.DeepCopy()
	now := metav1.Now()
	deleting.DeletionTimestamp = &now

	require.False(t, pred.Update(event.UpdateEvent{ObjectOld: db, ObjectNew: db.DeepCopy()}))
	require.False(t, pred.Create(event.CreateEvent{Object: db}))
	require.True(t, pred.Update(event.UpdateEvent{ObjectOld: db, ObjectNew: deleting}))
	require.True(t, pred.Delete(event.DeleteEvent{Object: db}))
}

func TestHAReadinessHearsAboutAClustersAddonsToo(t *testing.T) {
	pred := readyOrSpecChanged()
	cluster := func(addons metav1.ConditionStatus) *geassv1alpha1.GeassCluster {
		return &geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "c"}, Status: geassv1alpha1.GeassClusterStatus{
			Conditions: []metav1.Condition{
				{Type: platform.ConditionReady, Status: metav1.ConditionTrue},
				{Type: platform.ConditionAddonsReady, Status: addons},
			},
		}}
	}

	require.True(t, pred.Update(event.UpdateEvent{ObjectOld: cluster(metav1.ConditionFalse), ObjectNew: cluster(metav1.ConditionTrue)}))
	require.False(t, pred.Update(event.UpdateEvent{ObjectOld: cluster(metav1.ConditionTrue), ObjectNew: cluster(metav1.ConditionTrue)}))
}
