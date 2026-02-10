package controllers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestDeployModeChangerImpl_MarkDeployModeChanging(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Default()
	v1beta1.Labels().SetChangingMode(&mc, QueryNodeName, true)
	ctx := context.Background()
	changing := true
	t.Run("already set ok", func(t *testing.T) {
		err := changer.MarkDeployModeChanging(ctx, mc, changing)
		assert.NoError(t, err)
	})

	t.Run("update failed", func(t *testing.T) {
		v1beta1.Labels().SetChangingMode(&mc, QueryNodeName, false)
		mockUtil.EXPECT().UpdateAndRequeue(gomock.Any(), &mc).Return(errMock)
		err := changer.MarkDeployModeChanging(ctx, mc, changing)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("update requeue ok", func(t *testing.T) {
		v1beta1.Labels().SetChangingMode(&mc, QueryNodeName, false)
		mockUtil.EXPECT().UpdateAndRequeue(gomock.Any(), &mc).Return(ErrRequeue)
		err := changer.MarkDeployModeChanging(ctx, mc, changing)
		assert.True(t, errors.Is(err, ErrRequeue))
	})
}

func TestDeployModeChangerImpl_ChangeToTwoDeployMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Default()

	ctx := context.Background()
	t.Run("step 0 failed", func(t *testing.T) {
		changer.changeModeToV2Steps = []step{
			newStep("step0", func(context.Context, v1beta1.Milvus) error {
				return errMock
			}),
		}
		err := changer.ChangeToTwoDeployMode(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("step 0 ok, step 1 failed", func(t *testing.T) {
		changer.changeModeToV2Steps = []step{
			newStep("step0", func(context.Context, v1beta1.Milvus) error {
				return nil
			}),
			newStep("step1", func(context.Context, v1beta1.Milvus) error {
				return errMock
			}),
		}
		err := changer.ChangeToTwoDeployMode(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("3 steps all ok", func(t *testing.T) {
		changer.changeModeToV2Steps = []step{
			newStep("step0", func(context.Context, v1beta1.Milvus) error {
				return nil
			}),
			newStep("step1", func(context.Context, v1beta1.Milvus) error {
				return nil
			}),
			newStep("step2", func(context.Context, v1beta1.Milvus) error {
				return nil
			}),
		}
		err := changer.ChangeToTwoDeployMode(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployModeChangerImpl_SaveDeleteOldDeploy(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Default()

	ctx := context.Background()
	t.Run("get old deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, errMock)
		err := changer.SaveDeleteOldDeploy(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("save old deploy failed", func(t *testing.T) {
		oldDeploy := &appsv1.Deployment{}
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(oldDeploy, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldDeployName(mc, QueryNode), oldDeploy).Return(errMock)
		err := changer.SaveDeleteOldDeploy(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("orphan delete old deploy failed", func(t *testing.T) {
		oldDeploy := &appsv1.Deployment{}
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(oldDeploy, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldDeployName(mc, QueryNode), oldDeploy).Return(nil)
		mockUtil.EXPECT().OrphanDelete(ctx, oldDeploy).Return(errMock)
		err := changer.SaveDeleteOldDeploy(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("orphan delete old deploy ok", func(t *testing.T) {
		oldDeploy := &appsv1.Deployment{}
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(oldDeploy, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldDeployName(mc, QueryNode), oldDeploy).Return(nil)
		mockUtil.EXPECT().OrphanDelete(ctx, oldDeploy).Return(nil)
		err := changer.SaveDeleteOldDeploy(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("old deploy not found, ok", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, kerrors.NewNotFound(appsv1.Resource("deployments"), "old"))
		err := changer.SaveDeleteOldDeploy(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployModeChangerImpl_SaveDeleteOldReplicaSet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Default()

	ctx := context.Background()
	t.Run("get list old replicasets failed", func(t *testing.T) {
		mockUtil.EXPECT().ListOldReplicaSets(ctx, mc, QueryNode).Return(appsv1.ReplicaSetList{}, errMock)
		err := changer.SaveDeleteOldReplicaSet(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("save old replicasets failed", func(t *testing.T) {
		replicasetList := appsv1.ReplicaSetList{}
		mockUtil.EXPECT().ListOldReplicaSets(ctx, mc, QueryNode).Return(replicasetList, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc, QueryNode), &replicasetList).Return(errMock)
		err := changer.SaveDeleteOldReplicaSet(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("delete one old replicaset failed", func(t *testing.T) {
		replicasetList := appsv1.ReplicaSetList{Items: []appsv1.ReplicaSet{{}, {}}}
		mockUtil.EXPECT().ListOldReplicaSets(ctx, mc, QueryNode).Return(replicasetList, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc, QueryNode), &replicasetList).Return(nil)
		mockUtil.EXPECT().OrphanDelete(ctx, &replicasetList.Items[0]).Return(errMock)
		mockUtil.EXPECT().OrphanDelete(ctx, &replicasetList.Items[1]).Return(nil)
		err := changer.SaveDeleteOldReplicaSet(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("delete one old replicaset ok", func(t *testing.T) {
		replicasetList := appsv1.ReplicaSetList{Items: []appsv1.ReplicaSet{{}}}
		mockUtil.EXPECT().ListOldReplicaSets(ctx, mc, QueryNode).Return(replicasetList, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc, QueryNode), &replicasetList).Return(nil)
		mockUtil.EXPECT().OrphanDelete(ctx, &replicasetList.Items[0]).Return(nil)
		err := changer.SaveDeleteOldReplicaSet(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("no old replicaset ok", func(t *testing.T) {
		replicasetList := appsv1.ReplicaSetList{}
		mockUtil.EXPECT().ListOldReplicaSets(ctx, mc, QueryNode).Return(replicasetList, nil)
		mockUtil.EXPECT().SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc, QueryNode), &replicasetList).Return(nil)
		err := changer.SaveDeleteOldReplicaSet(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployModeChangerImpl_UpdateOldPodLabels(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Default()

	ctx := context.Background()
	t.Run("list old pods failed", func(t *testing.T) {
		mockUtil.EXPECT().ListOldPods(ctx, mc, QueryNode).Return(nil, errMock)
		err := changer.UpdateOldPodLabels(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("update old pod labels failed", func(t *testing.T) {
		pods := []corev1.Pod{{}}
		pods[0].Labels = map[string]string{}
		mockUtil.EXPECT().ListOldPods(ctx, mc, QueryNode).Return(pods, nil)
		mockCli.EXPECT().Update(ctx, &pods[0]).Return(errMock)
		err := changer.UpdateOldPodLabels(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("update old pod labels ok", func(t *testing.T) {
		pods := []corev1.Pod{{}}
		pods[0].Labels = map[string]string{}
		mockUtil.EXPECT().ListOldPods(ctx, mc, QueryNode).Return(pods, nil)
		mockCli.EXPECT().Update(ctx, &pods[0]).Return(nil)
		err := changer.UpdateOldPodLabels(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployModeChangerImpl_RecoverReplicaSets(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns"
	mc.Default()

	ctx := context.Background()
	key := client.ObjectKey{
		Namespace: mc.Namespace,
		Name:      formatSaveOldReplicaSetListName(mc, QueryNode),
	}
	t.Run("get saved old replicaset list failed", func(t *testing.T) {
		mockUtil.EXPECT().GetSavedObject(ctx, key, &appsv1.ReplicaSetList{}).Return(errMock)
		err := changer.RecoverReplicaSets(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	replicasetList := appsv1.ReplicaSetList{Items: []appsv1.ReplicaSet{{}}}
	replicasetList.Items[0].Labels = map[string]string{}
	replicasetList.Items[0].Spec.Template.Labels = map[string]string{}
	replicasetList.Items[0].Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{}}
	t.Run("recover old replicaset: bad name", func(t *testing.T) {
		replicasetList.Items[0].Name = "rs1"
		mockUtil.EXPECT().GetSavedObject(ctx, key, gomock.AssignableToTypeOf(&appsv1.ReplicaSetList{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj interface{}) error {
				*obj.(*appsv1.ReplicaSetList) = replicasetList
				return nil
			})
		err := changer.RecoverReplicaSets(ctx, mc)
		assert.Error(t, err)
	})

	replicasetList.Items[0].Name = "rs1-hash"
	t.Run("recover old replicaset failed", func(t *testing.T) {
		mockUtil.EXPECT().GetSavedObject(ctx, key, gomock.AssignableToTypeOf(&appsv1.ReplicaSetList{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj interface{}) error {
				*obj.(*appsv1.ReplicaSetList) = replicasetList
				return nil
			})
		mockUtil.EXPECT().CreateObject(ctx, gomock.AssignableToTypeOf(&appsv1.ReplicaSet{})).Return(errMock)
		err := changer.RecoverReplicaSets(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("recover ok", func(t *testing.T) {
		mockUtil.EXPECT().GetSavedObject(ctx, key, gomock.AssignableToTypeOf(&appsv1.ReplicaSetList{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj interface{}) error {
				*obj.(*appsv1.ReplicaSetList) = replicasetList
				return nil
			})
		mockUtil.EXPECT().CreateObject(ctx, gomock.AssignableToTypeOf(&appsv1.ReplicaSet{})).Return(nil)
		err := changer.RecoverReplicaSets(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployModeChangerImpl_RecoverDeploy(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Namespace = "ns"
	mc.Default()

	ctx := context.Background()
	key := client.ObjectKey{
		Namespace: mc.Namespace,
		Name:      formatSaveOldDeployName(mc, QueryNode),
	}
	t.Run("get saved old deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetSavedObject(ctx, key, &appsv1.Deployment{}).Return(errMock)
		err := changer.RecoverDeploy(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	oldDeploy := appsv1.Deployment{}
	oldDeploy.Labels = map[string]string{}
	oldDeploy.Spec.Template.Labels = map[string]string{}
	oldDeploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{}}
	t.Run("recover old deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetSavedObject(ctx, key, gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj interface{}) error {
				*obj.(*appsv1.Deployment) = oldDeploy
				return nil
			})
		mockUtil.EXPECT().CreateObject(ctx, gomock.AssignableToTypeOf(&appsv1.Deployment{})).Return(errMock)
		err := changer.RecoverDeploy(ctx, mc)
		assert.True(t, errors.Is(err, errMock))
	})

	t.Run("recover old deploy ok", func(t *testing.T) {
		mockUtil.EXPECT().GetSavedObject(ctx, key, gomock.AssignableToTypeOf(&appsv1.Deployment{})).
			DoAndReturn(func(ctx context.Context, key client.ObjectKey, obj interface{}) error {
				*obj.(*appsv1.Deployment) = oldDeploy
				return nil
			})
		mockUtil.EXPECT().CreateObject(ctx, gomock.AssignableToTypeOf(&appsv1.Deployment{})).Return(nil)
		err := changer.RecoverDeploy(ctx, mc)
		assert.NoError(t, err)
	})
}

func TestDeployModeChangerImpl_ChangeToOneDeployMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockUtil := NewMockDeployControllerBizUtil(ctrl)
	changer := NewDeployModeChanger(QueryNode, mockCli, mockUtil)
	mc := v1beta1.Milvus{}
	mc.Default()

	ctx := context.Background()

	t.Run("not QueryNode, no action", func(t *testing.T) {
		changerNotQN := NewDeployModeChanger(RootCoord, mockCli, mockUtil)
		err := changerNotQN.ChangeToOneDeployMode(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("get old deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, errors.New("mock error"))
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "get old deployment")
	})

	t.Run("old deploy exists, scale down and delete", func(t *testing.T) {
		oldDeploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "old-deploy"},
			Spec:       appsv1.DeploymentSpec{Replicas: new(int32)},
		}
		*oldDeploy.Spec.Replicas = 1
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(oldDeploy, nil)
		mockCli.EXPECT().Update(ctx, oldDeploy, gomock.Any()).DoAndReturn(func(_ context.Context, d *appsv1.Deployment, _ ...client.UpdateOption) error {
			assert.Equal(t, int32(0), *d.Spec.Replicas)
			return nil
		})
		mockCli.EXPECT().Delete(ctx, oldDeploy).Return(nil)
		currentDeployName := fmt.Sprintf("%s-%s-0", QueryNode.Name, mc.Name)
		mockCli.EXPECT().Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: currentDeployName}, gomock.Any()).Return(nil)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(nil)
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("old deploy exists, update replicas failed", func(t *testing.T) {
		oldDeploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "old-deploy"},
			Spec:       appsv1.DeploymentSpec{Replicas: new(int32)},
		}
		*oldDeploy.Spec.Replicas = 1
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(oldDeploy, nil)
		mockCli.EXPECT().Update(ctx, oldDeploy, gomock.Any()).Return(errors.New("mock error"))
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "scale down old deployment")
	})

	t.Run("old deploy exists, delete failed", func(t *testing.T) {
		oldDeploy := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "old-deploy"},
			Spec:       appsv1.DeploymentSpec{Replicas: new(int32)},
		}
		*oldDeploy.Spec.Replicas = 1
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(oldDeploy, nil)
		mockCli.EXPECT().Update(ctx, oldDeploy, gomock.Any()).DoAndReturn(func(_ context.Context, d *appsv1.Deployment, _ ...client.UpdateOption) error {
			assert.Equal(t, int32(0), *d.Spec.Replicas)
			return nil
		})
		mockCli.EXPECT().Delete(ctx, oldDeploy).Return(errors.New("mock error"))
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete old deployment")
	})

	t.Run("old deploy not exists, current deploy exists", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, kerrors.NewNotFound(appsv1.Resource("deployments"), "old"))
		currentDeployName := fmt.Sprintf("%s-%s-0", QueryNode.Name, mc.Name)
		mockCli.EXPECT().Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: currentDeployName}, gomock.Any()).Return(nil)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(nil)
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("old deploy not exists, create current deploy", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, kerrors.NewNotFound(appsv1.Resource("deployments"), "old"))
		currentDeployName := fmt.Sprintf("%s-%s-0", QueryNode.Name, mc.Name)
		mockCli.EXPECT().Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: currentDeployName}, gomock.Any()).Return(kerrors.NewNotFound(appsv1.Resource("deployments"), currentDeployName))
		mockCli.EXPECT().Scheme().Return(runtime.NewScheme())
		mockUtil.EXPECT().CreateObject(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, obj client.Object) error {
			deploy, ok := obj.(*appsv1.Deployment)
			assert.True(t, ok)
			assert.Equal(t, currentDeployName, deploy.Name)
			assert.Equal(t, "0", deploy.Labels[fmt.Sprintf("%s-group-id", QueryNode.Name)])
			return nil
		})
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(nil)
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("create current deploy failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, kerrors.NewNotFound(appsv1.Resource("deployments"), "old"))
		currentDeployName := fmt.Sprintf("%s-%s-0", QueryNode.Name, mc.Name)
		mockCli.EXPECT().Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: currentDeployName}, gomock.Any()).Return(kerrors.NewNotFound(appsv1.Resource("deployments"), currentDeployName))
		mockCli.EXPECT().Scheme().Return(runtime.NewScheme())
		mockUtil.EXPECT().CreateObject(ctx, gomock.Any()).Return(errors.New("mock error"))
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "create current deployment")
	})

	t.Run("mark group id failed", func(t *testing.T) {
		mockUtil.EXPECT().GetOldDeploy(ctx, mc, QueryNode).Return(nil, kerrors.NewNotFound(appsv1.Resource("deployments"), "old"))
		currentDeployName := fmt.Sprintf("%s-%s-0", QueryNode.Name, mc.Name)
		mockCli.EXPECT().Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: currentDeployName}, gomock.Any()).Return(nil)
		mockUtil.EXPECT().MarkMilvusComponentGroupId(ctx, mc, QueryNode, 0).Return(errors.New("mock error"))
		err := changer.ChangeToOneDeployMode(ctx, mc)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "mark group id to 0")
	})
}
