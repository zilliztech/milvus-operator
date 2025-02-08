package controllers

import (
	"errors"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	networkingv1 "k8s.io/api/networking/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var mockSetControllerReference = func(owner, controlled metav1.Object, scheme *runtime.Scheme) error {
	return mockSetControllerReferenceErr
}

var mockSetControllerReferenceErr error

func mockSetCtrlRef(err error) {
	mockSetControllerReferenceErr = err
	SetControllerReference = mockSetControllerReference
}

func TestMilvusClusterReconciler_ReconcileIngress(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	ctx := env.ctx
	mc := env.Inst
	mockErr := errors.New("failed")

	mockRenderer := NewMockingressRendererInterface(env.Ctrl)
	ingressRenderer = mockRenderer

	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	t.Run("disabled", func(t *testing.T) {
		err := r.ReconcileIngress(ctx, mc)
		assert.NoError(t, err)
	})

	mc.Spec.Com.Proxy.Ingress = &v1beta1.MilvusIngress{}
	mockSetCtrlRef(mockErr)
	t.Run("SetControllerReference failed", func(t *testing.T) {
		mockRenderer.EXPECT().Render(gomock.Any(), gomock.Any()).Return(nil)
		err := r.ReconcileIngress(ctx, mc)
		assert.Error(t, err)
	})

	mockSetCtrlRef(nil)
	rendered := networkingv1.Ingress{}
	t.Run("ingress not found, create", func(t *testing.T) {
		defer env.checkMocks()
		mockRenderer.EXPECT().Render(gomock.Any(), gomock.Any()).Return(&rendered)
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(k8sErrors.NewNotFound(networkingv1.Resource("ingress"), "test"))
		mockClient.EXPECT().Create(gomock.Any(), &rendered).Return(mockErr)
		err := r.ReconcileIngress(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("ingress get failed", func(t *testing.T) {
		defer env.checkMocks()
		mockRenderer.EXPECT().Render(gomock.Any(), gomock.Any()).Return(&rendered)
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Return(mockErr)
		err := r.ReconcileIngress(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("ingress found, equal not update", func(t *testing.T) {
		defer env.checkMocks()
		mockRenderer.EXPECT().Render(gomock.Any(), gomock.Any()).Return(&rendered)
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Do(
			func(any1, any2, input interface{}, opt ...any) {
				ingress := input.(*networkingv1.Ingress)
				ingress.Status.LoadBalancer.Ingress = make([]networkingv1.IngressLoadBalancerIngress, 2)
			}).Return(nil)
		err := r.ReconcileIngress(ctx, mc)
		assert.NoError(t, err)
	})

	icn := "class1"
	rendered.Spec.IngressClassName = &icn
	t.Run("ingress found, update", func(t *testing.T) {
		defer env.checkMocks()
		finalizers := []string{"finalizer"}
		mockRenderer.EXPECT().Render(gomock.Any(), gomock.Any()).Return(&rendered)
		mockClient.EXPECT().Get(gomock.Any(), gomock.Any(), gomock.Any()).Do(
			func(any1, any2, input interface{}, opt ...any) {
				ingress := input.(*networkingv1.Ingress)
				ingress.ObjectMeta.Finalizers = finalizers
				ingress.Status.LoadBalancer.Ingress = make([]networkingv1.IngressLoadBalancerIngress, 2)
			}).Return(nil)
		mockClient.EXPECT().Update(gomock.Any(), &rendered).Return(mockErr)
		err := r.ReconcileIngress(ctx, mc)
		assert.Equal(t, finalizers, rendered.ObjectMeta.Finalizers)
		assert.Error(t, err)
	})
}

func TestIngressRenderer_Render(t *testing.T) {
	env := newTestEnv(t)
	mc := env.Inst
	icn := "class1"
	ingressSpec := v1beta1.MilvusIngress{
		IngressClassName: &icn,
		Labels:           map[string]string{"label1": "value1"},
		Annotations:      map[string]string{"anno1": "value1"},
		Hosts:            []string{"host1", "host2"},
		TLSSecretRefs: map[string][]string{
			"secret1": {"host1", "host2"},
			"secret2": {"host1", "host2"},
		},
	}
	renderer := ingressRendererImpl{}
	ingress := renderer.Render(&mc, ingressSpec)
	assert.Equal(t, mc.Name+"-milvus", ingress.Name)
	assert.Equal(t, mc.Namespace, ingress.Namespace)
	assert.Equal(t, ingress.Labels, ingress.Labels)
	assert.Equal(t, ingress.Annotations, ingress.Annotations)
	assert.Equal(t, ingress.Spec.IngressClassName, ingress.Spec.IngressClassName)

	assert.Len(t, ingress.Spec.Rules, 2)
	assert.Len(t, ingress.Spec.TLS, 2)
	assert.Len(t, ingress.Spec.TLS[0].Hosts, 2)
}
