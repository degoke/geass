/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// reconcileTimes runs the reconciler n times for one store. A new store needs a pass to add its finalizer, and
// the cluster's object storage server needs two more that each record a default (its resources, then its
// placement) and ask to run again, before the first pass that creates its workload.
func reconcileTimes(ctx context.Context, r *GeassObjectStoreReconciler, name string, n int) {
	for range n {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: platform.SystemNamespace}})
		Expect(err).NotTo(HaveOccurred())
	}
}

// clusterServerReconcilesBeforeWorkload is how many passes the cluster server needs before its Deployment exists.
const clusterServerReconcilesBeforeWorkload = 4

// markObjectStoreServerAvailable does what the cluster's controllers would: it reports the server's pod up.
func markObjectStoreServerAvailable(ctx context.Context) {
	deploy := &appsv1.Deployment{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: platform.SystemNamespace}, deploy)).To(Succeed())
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas, deploy.Status.ReadyReplicas, deploy.Status.AvailableReplicas, deploy.Status.UpdatedReplicas = 1, 1, 1, 1
	Expect(k8sClient.Status().Update(ctx, deploy)).To(Succeed())
}

// clusterServer makes the cluster's object storage server and brings it to ready.
func clusterServer(ctx context.Context, reconciler *GeassObjectStoreReconciler, engine geassv1alpha1.GeassObjectStoreEngine) *geassv1alpha1.GeassObjectStore {
	server := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Engine: engine, Placement: geassv1alpha1.ObjectStorePlacementInCluster},
	}
	Expect(k8sClient.Create(ctx, server)).To(Succeed())
	reconcileTimes(ctx, reconciler, server.Name, clusterServerReconcilesBeforeWorkload)
	markObjectStoreServerAvailable(ctx)
	reconcileTimes(ctx, reconciler, server.Name, 1)
	return server
}

func isGone(err error) bool { return apierrors.IsNotFound(err) }

var _ = Describe("GeassObjectStore Controller", func() {
	ctx := context.Background()
	const ns = platform.SystemNamespace

	var fakeServer *fakeObjectStore
	var reconciler *GeassObjectStoreReconciler

	BeforeEach(func() {
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testDevTargetNS}})
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testHelmChartNS}})
		fakeServer = newFakeObjectStore(GinkgoT(), false)
		reconciler = &GeassObjectStoreReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), HTTP: fakeServer.client()}
	})

	AfterEach(func() {
		var list geassv1alpha1.GeassObjectStoreList
		_ = k8sClient.List(ctx, &list, client.InNamespace(ns))
		var clusterStores, projectStores []geassv1alpha1.GeassObjectStore
		for i := range list.Items {
			if isClusterObjectStore(&list.Items[i]) {
				clusterStores = append(clusterStores, list.Items[i])
				continue
			}
			projectStores = append(projectStores, list.Items[i])
		}
		// Buckets go before the server they live on.
		for _, item := range append(projectStores, clusterStores...) {
			current := item
			_ = k8sClient.Delete(ctx, &current)
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: current.Name, Namespace: ns}})
		}
		// Specs share one API server and it runs no volume controller, so a claim that was deleted stays
		// "terminating" behind its protection finalizer. Let it go.
		claim := &corev1.PersistentVolumeClaim{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}, claim); err == nil {
			claim.Finalizers = nil
			_ = k8sClient.Update(ctx, claim)
		}
	})

	It("creates the cluster's object storage on SeaweedFS", func() {
		server := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Engine:    geassv1alpha1.ObjectStoreEngineSeaweedFS,
				Placement: geassv1alpha1.ObjectStorePlacementInCluster,
			},
		}
		Expect(k8sClient.Create(ctx, server)).To(Succeed())
		reconcileTimes(ctx, reconciler, server.Name, clusterServerReconcilesBeforeWorkload)

		key := types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}
		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, deploy)).To(Succeed())
		Expect(deploy.Spec.Replicas).To(HaveValue(Equal(int32(1))))
		Expect(deploy.Spec.Strategy.Type).To(Equal(appsv1.RecreateDeploymentStrategyType), "one pod owns the volume at a time")
		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.Image).To(Equal(platform.SeaweedFSImage))
		Expect(container.Args).To(ContainElements("-ip.bind=127.0.0.1", "-s3.iam.readOnly=false"))
		Expect(strings.Join(container.Args, " ")).NotTo(ContainSubstring("0.0.0.0"), "SeaweedFS listens only on localhost")
		Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(2), "a forwarder publishes the S3 port, and only it")
		Expect(deploy.Spec.Template.Spec.Containers[1].Image).To(Equal(platform.S3ForwarderImage))
		Expect(container.Resources.Requests.Memory().String()).To(Equal("512Mi"))
		Expect(container.SecurityContext.RunAsNonRoot).To(HaveValue(BeTrue()))
		Expect(deploy.OwnerReferences).NotTo(BeEmpty(), "the server is deleted with its resource")

		By("not reporting ready before the pod is up")
		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: server.Name, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeFalse())
		Expect(conditionMessage(latest.Status.Conditions, platform.ConditionReady)).To(Equal(platform.MessageObjectStorageStarting))

		By("publishing its address once it is")
		markObjectStoreServerAvailable(ctx)
		reconcileTimes(ctx, reconciler, server.Name, 1)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: server.Name, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
		Expect(latest.Status.Endpoint).To(Equal("http://geass-s3.geass-system.svc:8333"))

		service := &corev1.Service{}
		Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
		Expect(service.Spec.Ports).To(HaveLen(1), "SeaweedFS publishes only its S3 port")
		Expect(service.Spec.Ports[0].Port).To(Equal(int32(8333)))
		claim := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, key, claim)).To(Succeed())
		Expect(claim.Spec.Resources.Requests.Storage().String()).To(Equal(platform.ObjectStoreServerVolumeSize))

		By("keeping the server's own login in its Secrets, and not in its arguments")
		root := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterObjectStoreName + "-root", Namespace: ns}, root)).To(Succeed())
		accessKey, secretKey := string(root.Data[platform.ConnectionKeyAccessKey]), string(root.Data[platform.ConnectionKeySecretKey])
		Expect(accessKey).To(HaveLen(32))
		Expect(secretKey).To(HaveLen(64))
		Expect(strings.Join(container.Args, " ")).NotTo(ContainSubstring(secretKey))
		config := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: objectStoreConfigSecretName, Namespace: ns}, config)).To(Succeed())
		var identities struct {
			Identities []struct {
				Credentials []struct{ AccessKey, SecretKey string }
			}
		}
		Expect(json.Unmarshal(config.Data["s3.json"], &identities)).To(Succeed())
		Expect(identities.Identities[0].Credentials[0].AccessKey).To(Equal(accessKey))
		Expect(string(config.Data["master.toml"])).To(ContainSubstring("copy_1 = 1"))

		connection := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterObjectStoreName + "-connection", Namespace: ns}, connection)).To(Succeed())
		Expect(string(connection.Data[platform.ConnectionKeyEndpoint])).To(Equal(latest.Status.Endpoint))
	})

	It("creates the cluster's object storage on Garage", func() {
		clusterServer(ctx, reconciler, geassv1alpha1.ObjectStoreEngineGarage)

		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}, deploy)).To(Succeed())
		container := deploy.Spec.Template.Spec.Containers[0]
		Expect(container.Image).To(Equal(platform.GarageImage))
		Expect(container.Args).To(ContainElement("--single-node"), "Garage lays its own cluster out")
		Expect(container.Resources.Requests.Memory().String()).To(Equal("256Mi"), "Garage starts smaller")
		secretEnv := map[string]string{}
		for _, env := range container.Env {
			if env.ValueFrom != nil && env.ValueFrom.SecretKeyRef != nil {
				secretEnv[env.Name] = env.ValueFrom.SecretKeyRef.Key
			}
		}
		Expect(secretEnv).To(HaveKeyWithValue("GARAGE_ADMIN_TOKEN", "adminToken"))
		Expect(secretEnv).To(HaveKeyWithValue("GARAGE_RPC_SECRET", "rpcSecret"))

		service := &corev1.Service{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}, service)).To(Succeed())
		Expect(service.Spec.Ports).To(HaveLen(2), "Geass drives Garage through its admin port")

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
		Expect(latest.Status.Endpoint).To(Equal("http://geass-s3.geass-system.svc:3900"))

		root := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterObjectStoreName + "-root", Namespace: ns}, root)).To(Succeed())
		Expect(string(root.Data[platform.ConnectionKeyAccessKey])).To(HavePrefix("GK"))
		for _, key := range []string{rootKeyAdminToken, rootKeyRPCSecret, rootKeyMetricsToken} {
			Expect(root.Data[key]).NotTo(BeEmpty(), key)
		}
		config := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: objectStoreConfigSecretName, Namespace: ns}, config)).To(Succeed())
		Expect(string(config.Data["garage.toml"])).To(ContainSubstring(`s3_region = "us-east-1"`))
		Expect(string(config.Data["garage.toml"])).NotTo(ContainSubstring(string(root.Data[rootKeyRPCSecret])), "secrets are not in the file")
	})

	It("writes nothing to a server that has not changed", func() {
		clusterServer(ctx, reconciler, geassv1alpha1.ObjectStoreEngineSeaweedFS)
		key := types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}
		deploy, service, config := &appsv1.Deployment{}, &corev1.Service{}, &corev1.Secret{}
		Expect(k8sClient.Get(ctx, key, deploy)).To(Succeed())
		Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: objectStoreConfigSecretName, Namespace: ns}, config)).To(Succeed())

		reconcileTimes(ctx, reconciler, platform.ClusterObjectStoreName, 2)

		after := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
		Expect(after.ResourceVersion).To(Equal(deploy.ResourceVersion), "the Deployment is left alone")
		Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
		Expect(service.ResourceVersion).NotTo(BeEmpty())
	})

	It("refuses to change the engine of a server that exists", func() {
		server := clusterServer(ctx, reconciler, geassv1alpha1.ObjectStoreEngineSeaweedFS)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(server), server)).To(Succeed())
		server.Spec.Engine = geassv1alpha1.ObjectStoreEngineGarage

		err := k8sClient.Update(ctx, server)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("engine cannot be changed"))
	})

	It("restarts the server when its size changes", func() {
		server := clusterServer(ctx, reconciler, geassv1alpha1.ObjectStoreEngineSeaweedFS)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(server), server)).To(Succeed())
		server.Spec.Resources = platform.DefaultObjectStoreResources(geassv1alpha1.ObjectStoreEngineGarage)
		Expect(k8sClient.Update(ctx, server)).To(Succeed())

		reconcileTimes(ctx, reconciler, server.Name, 1)

		deploy := &appsv1.Deployment{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}, deploy)).To(Succeed())
		Expect(deploy.Spec.Template.Spec.Containers[0].Resources.Requests.Memory().String()).To(Equal("256Mi"))
	})

	It("does not provision a draft project bucket before settings are saved", func() {
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineSeaweedFS,
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconcileTimes(ctx, reconciler, testObjectStoreName, 2)

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeFalse())
		Expect(conditionMessage(latest.Status.Conditions, platform.ConditionReady)).To(ContainSubstring("Configure the bucket name"))
	})

	It("does not provision a project bucket until the cluster has object storage", func() {
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineSeaweedFS,
				Buckets:     []string{"uploads"},
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconcileTimes(ctx, reconciler, testObjectStoreName, 2)

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeFalse())
		Expect(conditionMessage(latest.Status.Conditions, platform.ConditionReady)).To(Equal(platform.MessageSetUpObjectStorage))
		Expect(fakeServer.log.all()).To(BeEmpty())
	})

	It("creates a project bucket on the cluster's SeaweedFS", func() {
		clusterServer(ctx, reconciler, geassv1alpha1.ObjectStoreEngineSeaweedFS)
		store := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testObjectStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineSeaweedFS,
				Buckets:     []string{"uploads"},
			},
		}
		Expect(k8sClient.Create(ctx, store)).To(Succeed())

		reconcileTimes(ctx, reconciler, testObjectStoreName, 2)

		// Buckets on the shared server are stored behind the project environment.
		Expect(fakeServer.log.all()).To(ContainElement("PUT /payments.dev.uploads"))
		Expect(fakeServer.policyFor("geass-" + testObjectStoreName)).To(ContainSubstring("arn:aws:s3:::payments.dev.uploads"))
		secret := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "assets-connection", Namespace: testDevTargetNS}, secret)).To(Succeed())
		root := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterObjectStoreName + "-root", Namespace: ns}, root)).To(Succeed())
		Expect(string(secret.Data[platform.ConnectionKeyAccessKey])).NotTo(BeEmpty())
		Expect(secret.Data[platform.ConnectionKeyAccessKey]).NotTo(Equal(root.Data[platform.ConnectionKeyAccessKey]), "an app never gets the server's login")
		Expect(string(secret.Data[platform.ConnectionKeyBucket])).To(Equal("payments.dev.uploads"))

		latest := &geassv1alpha1.GeassObjectStore{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testObjectStoreName, Namespace: ns}, latest)).To(Succeed())
		Expect(conditionIsTrue(latest.Status.Conditions, platform.ConditionReady)).To(BeTrue())
	})

	It("removes the server's workloads only when the server is removed, and only without buckets", func() {
		server := clusterServer(ctx, reconciler, geassv1alpha1.ObjectStoreEngineSeaweedFS)
		bucket := &geassv1alpha1.GeassObjectStore{
			ObjectMeta: metav1.ObjectMeta{Name: testTempStoreName, Namespace: ns},
			Spec: geassv1alpha1.GeassObjectStoreSpec{
				Project:     testProjectName,
				Environment: geassv1alpha1.EnvironmentDev,
				Engine:      geassv1alpha1.ObjectStoreEngineSeaweedFS,
			},
		}
		Expect(k8sClient.Create(ctx, bucket)).To(Succeed())
		reconcileTimes(ctx, reconciler, testTempStoreName, 1)

		By("keeping it while a bucket lives on it")
		Expect(k8sClient.Delete(ctx, server)).To(Succeed())
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Name: server.Name, Namespace: ns}})
		Expect(err).To(MatchError(ContainSubstring("delete project buckets")))
		key := types.NamespacedName{Name: platform.ObjectStoreServerName, Namespace: ns}
		Expect(k8sClient.Get(ctx, key, &appsv1.Deployment{})).To(Succeed())

		By("removing it, and its data, once the bucket is gone")
		Expect(k8sClient.Delete(ctx, bucket)).To(Succeed())
		reconcileTimes(ctx, reconciler, testTempStoreName, 1)
		reconcileTimes(ctx, reconciler, server.Name, 1)
		Expect(isGone(k8sClient.Get(ctx, key, &appsv1.Deployment{}))).To(BeTrue())
		Expect(isGone(k8sClient.Get(ctx, key, &corev1.Service{}))).To(BeTrue())
		Expect(isGone(k8sClient.Get(ctx, types.NamespacedName{Name: objectStoreConfigSecretName, Namespace: ns}, &corev1.Secret{}))).To(BeTrue())
		Expect(isGone(k8sClient.Get(ctx, types.NamespacedName{Name: platform.ClusterObjectStoreName + "-root", Namespace: ns}, &corev1.Secret{}))).To(BeTrue())
		claim := &corev1.PersistentVolumeClaim{}
		err = k8sClient.Get(ctx, key, claim)
		Expect(isGone(err) || claim.DeletionTimestamp != nil).To(BeTrue(), "the volume is deleted with the server")
	})
})
