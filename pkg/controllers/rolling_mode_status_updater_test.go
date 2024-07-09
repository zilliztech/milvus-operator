package controllers

import (
	"context"
	"testing"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
)

func Test_GetExpectedTwoDeployComponents(t *testing.T) {
	spec := v1beta1.MilvusSpec{
		Mode: v1beta1.MilvusModeStandalone,
	}
	spec.Com.RollingMode = v1beta1.RollingModeV2
	t.Run("v2 standalone", func(t *testing.T) {
		components := GetExpectedTwoDeployComponents(spec)
		assert.Len(t, components, 0)
	})
	spec.Com.RollingMode = v1beta1.RollingModeV3
	t.Run("v3 standalone", func(t *testing.T) {
		components := GetExpectedTwoDeployComponents(spec)
		assert.Equal(t, StandaloneComponents, components)
	})

	spec.Mode = v1beta1.MilvusModeCluster
	spec.Com.RollingMode = v1beta1.RollingModeV2
	t.Run("v2 cluster", func(t *testing.T) {
		components := GetExpectedTwoDeployComponents(spec)
		assert.Len(t, components, 1)
		assert.Equal(t, QueryNode, components[0])
	})

	spec.Com.RollingMode = v1beta1.RollingModeV3
	t.Run("v3 cluster", func(t *testing.T) {
		components := GetExpectedTwoDeployComponents(spec)
		assert.Equal(t, MilvusComponents, components)
	})

	t.Run("v3 mixture", func(t *testing.T) {
		spec.Com.MixCoord = &v1beta1.MilvusMixCoord{}
		components := GetExpectedTwoDeployComponents(spec)
		assert.Equal(t, MixtureComponents, components)
	})

}

func TestRollingModeStatusUpdaterImpl_Update(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockCli := NewMockK8sClient(ctrl)
	mockStatusCli := NewMockK8sStatusClient(ctrl)
	mockCli.EXPECT().Status().Return(mockStatusCli).AnyTimes()
	mockBizFactory := NewMockDeployControllerBizFactory(ctrl)
	mockBiz := NewMockDeployControllerBiz(ctrl)
	updater := NewRollingModeStatusUpdater(mockCli, mockBizFactory)
	ctx := context.TODO()
	t.Run("no update, ok", func(t *testing.T) {
		mc := &v1beta1.Milvus{}
		mc.Status.RollingMode = v1beta1.RollingModeV2
		mc.Spec.Com.RollingMode = v1beta1.RollingModeV2
		err := updater.Update(ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("standalone v2, update status", func(t *testing.T) {
		mc := &v1beta1.Milvus{
			Spec: v1beta1.MilvusSpec{
				Mode: v1beta1.MilvusModeStandalone,
				Com: v1beta1.MilvusComponents{
					RollingMode: v1beta1.RollingModeV2,
				},
			},
		}
		mockStatusCli.EXPECT().Update(ctx, gomock.AssignableToTypeOf(mc)).Return(nil)
		err := updater.Update(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.RollingModeV2, mc.Status.RollingMode)
	})

	t.Run("cluster v2, querynode one deploy, status no change", func(t *testing.T) {
		mc := &v1beta1.Milvus{
			Spec: v1beta1.MilvusSpec{
				Mode: v1beta1.MilvusModeCluster,
				Com: v1beta1.MilvusComponents{
					RollingMode: v1beta1.RollingModeV2,
				},
			},
		}
		mc.Status.RollingMode = v1beta1.RollingModeV1
		mockBizFactory.EXPECT().GetBiz(QueryNode).Return(mockBiz).Times(1)
		mockBiz.EXPECT().CheckDeployMode(ctx, *mc).Return(v1beta1.OneDeployMode, nil)
		err := updater.Update(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.RollingModeV1, mc.Status.RollingMode)
	})

	t.Run("cluster v2, querynode two deploy, status update", func(t *testing.T) {
		mc := &v1beta1.Milvus{
			Spec: v1beta1.MilvusSpec{
				Mode: v1beta1.MilvusModeCluster,
				Com: v1beta1.MilvusComponents{
					RollingMode: v1beta1.RollingModeV2,
				},
			},
		}
		mc.Status.RollingMode = v1beta1.RollingModeV1
		mockBizFactory.EXPECT().GetBiz(QueryNode).Return(mockBiz).Times(1)
		mockBiz.EXPECT().CheckDeployMode(ctx, *mc).Return(v1beta1.TwoDeployMode, nil)
		mockStatusCli.EXPECT().Update(ctx, gomock.AssignableToTypeOf(mc)).Return(nil)
		err := updater.Update(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.RollingModeV2, mc.Status.RollingMode)
	})

	t.Run("v3 standalone, check update failed when check deploy mode", func(t *testing.T) {
		mc := &v1beta1.Milvus{
			Spec: v1beta1.MilvusSpec{
				Mode: v1beta1.MilvusModeStandalone,
				Com: v1beta1.MilvusComponents{
					RollingMode: v1beta1.RollingModeV3,
				},
			},
		}
		mockBizFactory.EXPECT().GetBiz(MilvusStandalone).Return(mockBiz).Times(1)
		mockBiz.EXPECT().CheckDeployMode(ctx, *mc).Return(v1beta1.DeployModeUnknown, assert.AnError)
		err := updater.Update(ctx, mc)
		assert.Error(t, err)
	})

	t.Run("v3 standalone, two deploy ok", func(t *testing.T) {
		mc := &v1beta1.Milvus{
			Spec: v1beta1.MilvusSpec{
				Mode: v1beta1.MilvusModeStandalone,
				Com: v1beta1.MilvusComponents{
					RollingMode: v1beta1.RollingModeV3,
				},
			},
		}
		mockBizFactory.EXPECT().GetBiz(MilvusStandalone).Return(mockBiz).Times(1)
		mockBiz.EXPECT().CheckDeployMode(ctx, *mc).Return(v1beta1.TwoDeployMode, nil)
		mockStatusCli.EXPECT().Update(ctx, gomock.AssignableToTypeOf(mc)).Return(nil)
		err := updater.Update(ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, v1beta1.RollingModeV3, mc.Status.RollingMode)
	})
}
