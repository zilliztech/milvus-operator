package controllers

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestClusterReconciler_ReconcileDeployments_CreateIfNotFound(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockQnController := NewMockDeployController(env.Ctrl)
	r.deployCtrl = mockQnController
	mockClient := env.MockClient
	ctx := env.ctx
	mcDefault := env.Inst
	mcDefault.Spec.Mode = v1beta1.MilvusModeCluster
	mcDefault.Default()

	// mock hasTerminatingPod
	bak := CheckComponentHasTerminatingPod
	CheckComponentHasTerminatingPod = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus, component MilvusComponent) (bool, error) {
		return false, nil
	}
	defer func() {
		CheckComponentHasTerminatingPod = bak
	}()

	t.Cleanup(func() {
		env.Ctrl.Finish()
	})

	// all ok
	t.Run("v1 deploy mode all ok", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).Return(nil)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "")).
			Times(len(MilvusComponents) - 1)
		mockClient.EXPECT().
			Create(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			Return(nil).
			Times(len(MilvusComponents) - 1)
		mockQnController.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := r.ReconcileDeployments(ctx, *mcDefault.DeepCopy())
		assert.NoError(t, err)
	})

	t.Run("has old standlaone deploy delete ok", func(t *testing.T) {
		oldDeploy := appsv1.Deployment{}
		oldDeploy.Name = "mc-standalone"
		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(func(_, raw, _ interface{}) error {
				list := raw.(*appsv1.DeploymentList)
				list.Items = append(list.Items, oldDeploy)
				return nil
			})
		mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(func(_, deploy interface{}, opts ...interface{}) error {
			assert.Equal(t, oldDeploy.Name, deploy.(*appsv1.Deployment).Name)
			return nil
		})
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "")).
			Times(len(MilvusComponents) - 1)
		mockClient.EXPECT().
			Create(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			Return(nil).
			Times(len(MilvusComponents) - 1)
		mockQnController.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := r.ReconcileDeployments(ctx, *mcDefault.DeepCopy())
		assert.NoError(t, err)
	})

	t.Run("has volume& volumemounts ok", func(t *testing.T) {
		mc := *mcDefault.DeepCopy()
		mc.Spec.Com.Volumes = []v1beta1.Values{
			{},
		}
		mc.Spec.Com.VolumeMounts = []corev1.VolumeMount{
			{},
		}
		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).Return(nil)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, "")).
			Times(len(MilvusComponents) - 1)
		mockClient.EXPECT().
			Create(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			Return(nil).
			Times(len(MilvusComponents) - 1)
		mockQnController.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := r.ReconcileDeployments(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestClusterReconciler_ReconcileDeployments_Existed(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockQnController := NewMockDeployController(env.Ctrl)
	r.deployCtrl = mockQnController
	mockClient := env.MockClient
	ctx := env.ctx
	m := env.Inst
	m.Spec.Mode = v1beta1.MilvusModeCluster
	m.Default()

	// mock hasTerminatingPod
	bak := CheckComponentHasTerminatingPod
	CheckComponentHasTerminatingPod = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus, component MilvusComponent) (bool, error) {
		return false, nil
	}
	defer func() {
		CheckComponentHasTerminatingPod = bak
	}()
	t.Run("call client.Update if changed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).Return(nil)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
				cm := obj.(*appsv1.Deployment)
				cm.Namespace = "ns"
				cm.Name = "mc"
				return nil
			}).Times(len(MilvusComponents) - 1)
		mockClient.EXPECT().
			Update(gomock.Any(), gomock.Any()).Return(nil).
			Times(len(MilvusComponents) - 1)
		mockQnController.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := r.ReconcileDeployments(ctx, m)
		assert.NoError(t, err)
	})

	t.Run("not call client.Update if configmap not changed", func(t *testing.T) {
		mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).Return(nil)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj client.Object) error {
				cm := obj.(*appsv1.Deployment)
				cm.Namespace = "ns"
				cm.Name = "mc-xxx"
				switch key.Name {
				case "mc-milvus-proxy":
					r.updateDeployment(ctx, m, cm, Proxy)
				case "mc-milvus-rootcoord":
					r.updateDeployment(ctx, m, cm, RootCoord)
				case "mc-milvus-datacoord":
					r.updateDeployment(ctx, m, cm, DataCoord)
				case "mc-milvus-querycoord":
					r.updateDeployment(ctx, m, cm, QueryCoord)
				case "mc-milvus-indexcoord":
					r.updateDeployment(ctx, m, cm, IndexCoord)
				case "mc-milvus-datanode":
					r.updateDeployment(ctx, m, cm, DataNode)
				case "mc-milvus-querynode":
					r.updateDeployment(ctx, m, cm, QueryNode)
				case "mc-milvus-indexnode":
					r.updateDeployment(ctx, m, cm, IndexNode)
				case "mc-milvus-standalone":
					r.updateDeployment(ctx, m, cm, MilvusStandalone)
				}
				return nil
			}).Times(len(MilvusComponents) - 1)

		mockQnController.EXPECT().Reconcile(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

		err := r.ReconcileDeployments(ctx, m)
		assert.NoError(t, err)
	})

}

func TestGetStorageSecretRefEnv(t *testing.T) {
	ret := GetStorageSecretRefEnv("")
	assert.Len(t, ret, 0)
	ret = GetStorageSecretRefEnv("secret")
	assert.Len(t, ret, 2)
}

func TestReconciler_handleOldInstanceChangingMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	r := newMilvusReconcilerForTest(ctrl)
	mockClient := r.Client.(*MockK8sClient)
	ctx := context.Background()

	m := v1beta1.Milvus{}
	m.Default()
	component := MilvusStandalone

	t.Run("not changing", func(t *testing.T) {
		defer ctrl.Finish()
		err := r.handleOldInstanceChangingMode(ctx, m, component)
		assert.NoError(t, err)
	})

	t.Run("changing, update pod label, requeue", func(t *testing.T) {
		defer ctrl.Finish()
		m.Spec.Mode = v1beta1.MilvusModeCluster
		m.Default()
		m.Annotations[v1beta1.PodServiceLabelAddedAnnotation] = v1beta1.FalseStr

		mockClient.EXPECT().List(gomock.Any(), gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(ctx context.Context, list interface{}, opts ...interface{}) error {
			l := list.(*corev1.PodList)
			l.Items = []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "ns",
						Name:      "pod1",
					},
				},
			}
			return nil
		})
		pod := corev1.Pod{}
		mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&pod)).Times(2)
		mockClient.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&m)).Times(1)

		err := r.handleOldInstanceChangingMode(ctx, m, component)
		assert.Error(t, err)
		assert.Equal(t, ErrRequeue, errors.Cause(err))
	})

	t.Run("changing, updated", func(t *testing.T) {
		defer ctrl.Finish()
		m.Annotations[v1beta1.PodServiceLabelAddedAnnotation] = v1beta1.TrueStr
		err := r.handleOldInstanceChangingMode(ctx, m, component)
		assert.NoError(t, err)
	})
}

func Test_removeVolumeMounts(t *testing.T) {
	vms := []corev1.VolumeMount{
		{
			Name:      "v1",
			MountPath: "p1",
		},
		{
			Name:      "v1",
			MountPath: "p2",
		},
		{
			Name:      "v2",
			MountPath: "p3",
		},
		{
			Name:      "v3",
			MountPath: "p4",
		},
	}
	removeVolumeMounts(&vms, "v1")
	assert.Len(t, vms, 2)
	assert.Equal(t, "p3", vms[0].MountPath)
	assert.Equal(t, "p4", vms[1].MountPath)
}
