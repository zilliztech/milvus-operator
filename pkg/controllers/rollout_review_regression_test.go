package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestZeroSurgeAvailabilityStatusLag(t *testing.T) {
	for _, tc := range []struct {
		name string
		pod  corev1.Pod
	}{
		{"pending", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodPending}}},
		{"readiness lost", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse}}}}},
		{"readiness unknown", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionUnknown}}}}},
		{"missing ready condition", corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			util := NewMockK8sUtil(ctrl)
			biz := NewDeployControllerBizUtil(QueryNode, nil, util)
			mc := v1beta1.Milvus{}
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Default()
			mc.Spec.Com.QueryNode.Replicas = int32Ptr(3)
			mc.Spec.Com.QueryNode.RollingUpdate = rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(1)).RollingUpdate
			current := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{AppLabelComponent: QueryNodeName}, Generation: 1}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(1)}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1, ReadyReplicas: 1}}
			v1beta1.Labels().SetGroupIDStr(QueryNodeName, current.Labels, "1")
			old := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(3)}, Status: appsv1.DeploymentStatus{Replicas: 3, UpdatedReplicas: 3, AvailableReplicas: 3, ReadyReplicas: 3}}
			ready := corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
			util.EXPECT().MarkMilvusComponentGroupId(gomock.Any(), gomock.Any(), QueryNode, 1).Return(nil).AnyTimes()
			util.EXPECT().ListDeployPods(gomock.Any(), old, QueryNode).Return([]corev1.Pod{ready, ready, ready}, nil).AnyTimes()
			util.EXPECT().ListDeployPods(gomock.Any(), current, QueryNode).Return([]corev1.Pod{tc.pod}, nil).AnyTimes()
			util.EXPECT().DeploymentIsStable(gomock.Any(), gomock.Any()).DoAndReturn((&K8sUtilImpl{}).DeploymentIsStable).AnyTimes()

			// The Pod has lost readiness without a Deployment generation change.
			// Do not count its stale AvailableReplicas and drain two old replicas.
			err := biz.ScaleDeployments(context.Background(), mc, current, old)
			require.ErrorIs(t, err, ErrRequeue)
			require.ErrorContains(t, err, "available replicas exceed ready pods")
			require.Equal(t, int32(3), *old.Spec.Replicas)
			require.Equal(t, int32(1), current.Status.AvailableReplicas, "do not mutate observed status")

			// Once status catches up, one old replica can be released safely.
			current.Status.AvailableReplicas = 0
			current.Status.ReadyReplicas = 0
			current.Status.UnavailableReplicas = 1
			util.EXPECT().UpdateAndRequeue(gomock.Any(), old).Return(ErrRequeue)
			require.ErrorIs(t, biz.ScaleDeployments(context.Background(), mc, current, old), ErrRequeue)
			require.Equal(t, int32(2), *old.Spec.Replicas)
		})
	}
}

func TestZeroSurgeNormalScaling(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		desired, current, actual, want int
		oldPodsRemain                  bool
	}{
		{"quota blocked downscale", 3, 5, 3, 4, false},
		{"quota blocked scale up", 5, 3, 2, 5, false},
		{"no scaling needed", 3, 3, 2, 3, false},
		{"final refill", 3, 2, 2, 3, false},
		{"final refill waits for old pod", 3, 2, 2, 2, true},
		{"downscale does not need old capacity", 3, 5, 3, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			util := NewMockK8sUtil(ctrl)
			biz := NewDeployControllerBizUtil(QueryNode, nil, util)
			mc := v1beta1.Milvus{}
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Default()
			mc.Spec.Com.QueryNode.Replicas = int32Ptr(tc.desired)
			mc.Spec.Com.QueryNode.RollingUpdate = rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(1)).RollingUpdate
			current := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{AppLabelComponent: QueryNodeName}, Generation: 1}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(tc.current)}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: int32(tc.actual), UpdatedReplicas: int32(tc.actual), AvailableReplicas: int32(tc.actual), ReadyReplicas: int32(tc.actual)}}
			v1beta1.Labels().SetGroupIDStr(QueryNodeName, current.Labels, "1")
			old := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(0)}}
			var oldPods []corev1.Pod
			if tc.oldPodsRemain {
				now := metav1.Now()
				oldPods = []corev1.Pod{{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &now}}}
			}
			util.EXPECT().MarkMilvusComponentGroupId(gomock.Any(), gomock.Any(), QueryNode, 1).Return(nil)
			util.EXPECT().ListDeployPods(gomock.Any(), old, QueryNode).Return(oldPods, nil).AnyTimes()
			util.EXPECT().DeploymentIsStable(old, oldPods).DoAndReturn((&K8sUtilImpl{}).DeploymentIsStable).AnyTimes()
			// Normal scaling must not inspect current Pod readiness or require
			// quota-blocked replicas to exist. Any such mock call fails this test.
			if tc.want != tc.current {
				util.EXPECT().UpdateAndRequeue(gomock.Any(), current).Return(ErrRequeue)
			}
			err := biz.ScaleDeployments(context.Background(), mc, current, old)
			if tc.want != tc.current || tc.oldPodsRemain {
				require.ErrorIs(t, err, ErrRequeue)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, int32(tc.want), *current.Spec.Replicas)
			require.Equal(t, int32(0), *old.Spec.Replicas)
		})
	}
}
