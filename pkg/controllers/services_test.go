package controllers

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

func TestReconcileServices_IPFamilyPolicy(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())
	tests := []struct {
		name     string
		existing *corev1.IPFamilyPolicy
		desired  *corev1.IPFamilyPolicy
		want     *corev1.IPFamilyPolicy
	}{
		{name: "create with Kubernetes default"},
		{name: "create single stack", desired: ptr.To(corev1.IPFamilyPolicySingleStack), want: ptr.To(corev1.IPFamilyPolicySingleStack)},
		{name: "create prefer dual stack", desired: ptr.To(corev1.IPFamilyPolicyPreferDualStack), want: ptr.To(corev1.IPFamilyPolicyPreferDualStack)},
		{name: "create require dual stack", desired: ptr.To(corev1.IPFamilyPolicyRequireDualStack), want: ptr.To(corev1.IPFamilyPolicyRequireDualStack)},
		{name: "preserve default single stack", existing: ptr.To(corev1.IPFamilyPolicySingleStack), want: ptr.To(corev1.IPFamilyPolicySingleStack)},
		{name: "preserve manual dual stack patch", existing: ptr.To(corev1.IPFamilyPolicyPreferDualStack), want: ptr.To(corev1.IPFamilyPolicyPreferDualStack)},
		{name: "enable dual stack", existing: ptr.To(corev1.IPFamilyPolicySingleStack), desired: ptr.To(corev1.IPFamilyPolicyPreferDualStack), want: ptr.To(corev1.IPFamilyPolicyPreferDualStack)},
		{name: "require dual stack", existing: ptr.To(corev1.IPFamilyPolicyPreferDualStack), desired: ptr.To(corev1.IPFamilyPolicyRequireDualStack), want: ptr.To(corev1.IPFamilyPolicyRequireDualStack)},
		{name: "return to single stack", existing: ptr.To(corev1.IPFamilyPolicyPreferDualStack), desired: ptr.To(corev1.IPFamilyPolicySingleStack), want: ptr.To(corev1.IPFamilyPolicySingleStack)},
	}
	for _, mode := range []v1beta1.MilvusMode{v1beta1.MilvusModeStandalone, v1beta1.MilvusModeCluster} {
		for _, tt := range tests {
			t.Run(string(mode)+"/"+tt.name, func(t *testing.T) {
				scheme := runtime.NewScheme()
				require.NoError(t, corev1.AddToScheme(scheme))
				require.NoError(t, v1beta1.AddToScheme(scheme))
				m := v1beta1.Milvus{
					ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "milvus-uid"},
					Spec:       v1beta1.MilvusSpec{Mode: mode},
				}
				m.Default()
				m.Spec.GetServiceComponent().ServiceIPFamilyPolicy = tt.desired
				key := client.ObjectKey{Namespace: m.Namespace, Name: GetServiceInstanceName(m.Name)}
				builder := fake.NewClientBuilder().WithScheme(scheme)
				var original corev1.ServiceSpec
				if tt.existing != nil {
					original = corev1.ServiceSpec{
						IPFamilyPolicy: tt.existing,
						ClusterIP:      "10.0.0.1",
						ClusterIPs:     []string{"10.0.0.1"},
						IPFamilies:     []corev1.IPFamily{corev1.IPv4Protocol},
					}
					if *tt.existing != corev1.IPFamilyPolicySingleStack {
						original.ClusterIPs = append(original.ClusterIPs, "fd00::1")
						original.IPFamilies = append(original.IPFamilies, corev1.IPv6Protocol)
					}
					builder.WithObjects(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Spec: original})
				}
				kubeClient := builder.Build()
				r := &MilvusReconciler{Client: kubeClient, Scheme: scheme, logger: logr.Discard()}
				ctx := context.Background()
				require.NoError(t, r.ReconcileServices(ctx, m))
				var service corev1.Service
				require.NoError(t, kubeClient.Get(ctx, key, &service))
				assert.Equal(t, tt.want, service.Spec.IPFamilyPolicy)
				// The fake client only checks fields changed by the operator.
				// The API server allocates a secondary address on upgrade and removes
				// it on downgrade; these assertions do not validate that behavior.
				assert.Equal(t, original.ClusterIP, service.Spec.ClusterIP)
				assert.Equal(t, original.ClusterIPs, service.Spec.ClusterIPs)
				assert.Equal(t, original.IPFamilies, service.Spec.IPFamilies)

				before := service.DeepCopy()
				require.NoError(t, r.ReconcileServices(ctx, m))
				require.NoError(t, kubeClient.Get(ctx, key, &service))
				assert.Equal(t, before, &service, "reconciliation should not write an unchanged Service")

				m.Spec.GetServiceComponent().ServiceIPFamilyPolicy = nil
				require.NoError(t, r.ReconcileServices(ctx, m))
				require.NoError(t, kubeClient.Get(ctx, key, &service))
				assert.Equal(t, before, &service, "removing the setting should preserve the Service policy")
			})
		}
	}
}

func TestReconciler_ReconcileServices_CreateIfNotExist(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(1)

	mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).Times(1)

	err := r.ReconcileServices(ctx, m)
	assert.NoError(t, err)

	t.Run("cluster", func(t *testing.T) {
		m := v1beta1.Milvus{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "mc",
			},
		}
		m.Default()
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Default()
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "mockErr")).Times(1)

		mockClient.EXPECT().Create(gomock.Any(), gomock.Any()).Times(1)

		err := r.ReconcileServices(ctx, m)
		assert.NoError(t, err)
	})
}

func TestReconciler_ReconcileServices_UpdateIfExisted(t *testing.T) {
	config.Init(util.GetGitRepoRootDir())

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	m := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "mc",
		},
	}
	m.Default()

	mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
		Do(func(ctx, key, obj interface{}, opts ...any) error {
			s := obj.(*corev1.Service)
			s.Namespace = "ns"
			s.Name = "cm1"
			return nil
		}).Times(1)

	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

	err := r.ReconcileServices(ctx, m)
	assert.NoError(t, err)

	t.Run("cluster", func(t *testing.T) {
		m := v1beta1.Milvus{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "ns",
				Name:      "mc",
			},
		}
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Default()

		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).
			Do(func(ctx, key, obj interface{}, opts ...any) error {
				s := obj.(*corev1.Service)
				s.Namespace = "ns"
				s.Name = "cm1"
				return nil
			}).Times(1)

		mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Times(1)

		err := r.ReconcileServices(ctx, m)
		assert.NoError(t, err)
	})
}
