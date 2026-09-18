package controllers

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func rolloutTestStrategy(surge, unavailable intstr.IntOrString) appsv1.DeploymentStrategy {
	return appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{MaxSurge: &surge, MaxUnavailable: &unavailable}}
}

func TestRolloutSteps(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		strategy                     appsv1.DeploymentStrategy
		replicas, surge, unavailable int
	}{
		{"recreate", appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}, 3, 1, 0},
		{"nil rollingUpdate", appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}, 3, 1, 0},
		{"omitted fields", appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType, RollingUpdate: &appsv1.RollingUpdateDeployment{}}, 3, 1, 0},
		{"zero surge", rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(1)), 3, 0, 1},
		{"zero unavailable", rolloutTestStrategy(intstr.FromInt(2), intstr.FromInt(0)), 3, 2, 0},
		{"percentages", rolloutTestStrategy(intstr.FromString("20%"), intstr.FromString("20%")), 6, 2, 1},
		{"small percentage no surge", rolloutTestStrategy(intstr.FromString("0%"), intstr.FromString("1%")), 3, 0, 1},
		{"unavailable capped", rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(10)), 3, 0, 3},
		{"invalid", rolloutTestStrategy(intstr.FromString("bad"), intstr.FromInt(-1)), 3, 1, 0},
		{"invalid unavailable", rolloutTestStrategy(intstr.FromInt(-1), intstr.FromString("bad")), 3, 1, 0},
		{"zero both", rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(0)), 3, 0, 0},
		{"zero replicas", rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(1)), 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surge, unavailable := rolloutSteps(tc.strategy, tc.replicas)
			assert.Equal(t, tc.surge, surge)
			assert.Equal(t, tc.unavailable, unavailable)
		})
	}
}

func TestPlanScaleForRolloutZeroSurge(t *testing.T) {
	for _, changedLimits := range []bool{false, true} {
		for _, tc := range []struct {
			name                                 string
			old, current, available, unavailable int
			wantOld                              bool
			change                               int
		}{
			{"drain first", 3, 0, 0, 1, true, -1},
			{"refill", 2, 0, 0, 1, false, 1},
			{"wait for readiness", 2, 1, 0, 1, false, 0},
			{"next batch", 2, 1, 1, 1, true, -1},
			{"last old replica", 1, 2, 2, 1, true, -1},
			{"final refill", 0, 2, 2, 1, false, 1},
			{"finished", 0, 3, 3, 1, false, 0},
			{"batch drain", 3, 0, 0, 2, true, -2},
			{"batch refill", 1, 0, 0, 2, false, 2},
			{"recover pending surge", 3, 1, 0, 1, true, -1},
			{"cap old replicas", 1, 3, 3, 3, true, -1},
			{"no availability budget", 3, 0, 0, 0, false, 0},
		} {
			t.Run(tc.name+map[bool]string{true: "/changed limits", false: "/same limits"}[changedLimits], func(t *testing.T) {
				mc := v1beta1.Milvus{}
				mc.Spec.Com.QueryNode = &v1beta1.MilvusQueryNode{Component: v1beta1.Component{Replicas: int32Ptr(3)}}
				current := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(tc.current), Strategy: rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(tc.unavailable))}, Status: appsv1.DeploymentStatus{AvailableReplicas: int32(tc.available)}}
				old := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(tc.old)}, Status: appsv1.DeploymentStatus{AvailableReplicas: int32(tc.old)}}
				if changedLimits {
					current.Spec.Template.Spec.Containers = []corev1.Container{{Name: "querynode", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}}}
				}
				biz := NewDeployControllerBizUtil(QueryNode, nil, nil)
				action := biz.planScaleForRollout(mc, current, old)
				assert.Equal(t, tc.change, action.replicaChange)
				if tc.change != 0 {
					assert.Equal(t, tc.wantOld, action.deploy == old)
				} else {
					assert.Equal(t, noScaleAction, action)
				}
			})
		}
	}
}

func TestPlanScaleForRolloutSurgeBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		old, current, unavailable, change int
		wantOld                           bool
	}{
		{"full surge batch", 10, 0, 0, 2, false},
		{"refill existing deficit", 7, 1, 0, 2, false},
		{"drain only surplus with zero unavailable", 9, 2, 0, -1, true},
		{"drain within unavailable budget", 9, 2, 2, -2, true},
		{"cap new replicas", 1, 9, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mc := v1beta1.Milvus{}
			mc.Spec.Com.QueryNode = &v1beta1.MilvusQueryNode{Component: v1beta1.Component{Replicas: int32Ptr(10)}}
			current := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(tc.current), Strategy: rolloutTestStrategy(intstr.FromString("20%"), intstr.FromInt(tc.unavailable))}}
			old := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(tc.old)}}
			action := NewDeployControllerBizUtil(QueryNode, nil, nil).planScaleForRollout(mc, current, old)
			assert.Equal(t, tc.change, action.replicaChange)
			assert.Equal(t, tc.wantOld, action.deploy == old)
		})
	}
}

// Exercise the real stability checks and planner together, including recovery
// from an already Pending surge and waiting for old pods to finish terminating.
func TestScaleDeploymentsZeroSurge(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(*appsv1.Deployment, *appsv1.Deployment, *[]corev1.Pod, *[]corev1.Pod, *v1beta1.Milvus)
		wantScale bool
	}{
		{"pending surge recovers", nil, true},
		{"default still waits for readiness", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) {
			mc.Spec.Com.QueryNode.RollingUpdate = nil
		}, false},
		{"stale generation waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) { c.Generation++ }, false},
		{"scale not observed waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) { c.Spec.Replicas = int32Ptr(2) }, false},
		{"old revision waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) { c.Status.UpdatedReplicas = 0 }, false},
		{"pod count mismatch waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) { *cp = nil }, false},
		{"new terminating pod waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) {
			now := metav1.Now()
			(*cp)[0].DeletionTimestamp = &now
		}, false},
		{"old terminating pod waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) {
			now := metav1.Now()
			(*op)[0].DeletionTimestamp = &now
		}, false},
		{"last old pod still terminating waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) {
			o.Spec.Replicas = int32Ptr(0)
			now := metav1.Now()
			(*op)[0].DeletionTimestamp = &now
		}, false},
		{"old unavailable waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) { o.Status.AvailableReplicas-- }, false},
		{"budget exhausted waits", func(c, o *appsv1.Deployment, cp, op *[]corev1.Pod, mc *v1beta1.Milvus) {
			mc.Spec.Com.QueryNode.Replicas = int32Ptr(4)
		}, false},
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
			current := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{AppLabelComponent: QueryNodeName}, Generation: 1}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(1)}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, UnavailableReplicas: 1}}
			v1beta1.Labels().SetGroupIDStr(QueryNodeName, current.Labels, "1")
			old := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Generation: 1}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(3)}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 3, UpdatedReplicas: 3, ReadyReplicas: 3, AvailableReplicas: 3}}
			currentPods := []corev1.Pod{{}}
			ready := corev1.Pod{Status: corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}}
			oldPods := []corev1.Pod{ready, ready, ready}
			if tc.mutate != nil {
				tc.mutate(current, old, &currentPods, &oldPods, &mc)
			}
			oldReplicas := *old.Spec.Replicas
			util.EXPECT().MarkMilvusComponentGroupId(gomock.Any(), gomock.Any(), QueryNode, 1).Return(nil)
			util.EXPECT().ListDeployPods(gomock.Any(), old, QueryNode).Return(oldPods, nil).AnyTimes()
			util.EXPECT().ListDeployPods(gomock.Any(), current, QueryNode).Return(currentPods, nil).AnyTimes()
			util.EXPECT().DeploymentIsStable(gomock.Any(), gomock.Any()).DoAndReturn((&K8sUtilImpl{}).DeploymentIsStable).AnyTimes()
			if tc.wantScale {
				util.EXPECT().UpdateAndRequeue(gomock.Any(), old).DoAndReturn(func(_ context.Context, obj client.Object) error {
					assert.Equal(t, int32(2), *obj.(*appsv1.Deployment).Spec.Replicas)
					return ErrRequeue
				})
			}
			err := biz.ScaleDeployments(context.Background(), mc, current, old)
			if tc.wantScale {
				require.ErrorIs(t, err, ErrRequeue)
			}
			if !tc.wantScale {
				assert.Equal(t, oldReplicas, *old.Spec.Replicas)
			}
		})
	}
}

func TestZeroSurgeRolloutCompletes(t *testing.T) {
	for _, replicas := range []int{1, 3, 5} {
		t.Run(fmt.Sprint(replicas), func(t *testing.T) {
			ctrl := gomock.NewController(t)
			util := NewMockK8sUtil(ctrl)
			biz := NewDeployControllerBizUtil(QueryNode, nil, util)
			mc := v1beta1.Milvus{}
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Default()
			mc.Spec.Com.QueryNode.Replicas = int32Ptr(replicas)
			mc.Spec.Com.QueryNode.RollingUpdate = rolloutTestStrategy(intstr.FromInt(0), intstr.FromInt(1)).RollingUpdate
			current := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{AppLabelComponent: QueryNodeName}}, Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(0)}}
			v1beta1.Labels().SetGroupIDStr(QueryNodeName, current.Labels, "1")
			old := &appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: int32Ptr(replicas)}}
			// Exercise resource-limit changes as well as the final old replica.
			current.Spec.Template.Spec.Containers = []corev1.Container{{Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("2")}}}}
			settle := func(d *appsv1.Deployment) {
				n := *d.Spec.Replicas
				d.Status = appsv1.DeploymentStatus{ObservedGeneration: d.Generation, Replicas: n, UpdatedReplicas: n, AvailableReplicas: n, ReadyReplicas: n}
			}
			settle(current)
			settle(old)
			util.EXPECT().MarkMilvusComponentGroupId(gomock.Any(), gomock.Any(), QueryNode, 1).Return(nil).AnyTimes()
			util.EXPECT().ListDeployPods(gomock.Any(), gomock.Any(), QueryNode).DoAndReturn(func(_ context.Context, d *appsv1.Deployment, _ MilvusComponent) ([]corev1.Pod, error) {
				pods := make([]corev1.Pod, d.Status.Replicas)
				for i := range pods {
					pods[i].Status = corev1.PodStatus{Phase: corev1.PodRunning, Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}}
					if i >= int(*d.Spec.Replicas) {
						now := metav1.Now()
						pods[i].DeletionTimestamp = &now
					}
				}
				return pods, nil
			}).AnyTimes()
			util.EXPECT().DeploymentIsStable(gomock.Any(), gomock.Any()).DoAndReturn((&K8sUtilImpl{}).DeploymentIsStable).AnyTimes()
			var updated *appsv1.Deployment
			util.EXPECT().UpdateAndRequeue(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, obj client.Object) error {
				updated = obj.(*appsv1.Deployment)
				updated.Generation++
				return ErrRequeue
			}).AnyTimes()
			for step := 0; step < replicas*2; step++ {
				updated = nil
				require.ErrorIs(t, biz.ScaleDeployments(context.Background(), mc, current, old), ErrRequeue)
				want := old
				if step%2 == 1 {
					want = current
				}
				require.Same(t, want, updated)
				total := int(*old.Spec.Replicas + *current.Spec.Replicas)
				require.LessOrEqual(t, total, replicas)
				require.GreaterOrEqual(t, total, replicas-1)
				updated = nil
				err := biz.ScaleDeployments(context.Background(), mc, current, old)
				if step == replicas*2-1 {
					// At the desired count there is no further scaling to gate;
					// LastRolloutFinished separately waits for the final pods.
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, ErrRequeue)
				}
				require.Nil(t, updated, "wait for the previous scale and termination to finish")
				settle(current)
				settle(old)
			}
			require.Equal(t, int32(0), *old.Spec.Replicas)
			require.Equal(t, int32(replicas), *current.Spec.Replicas)
			require.NoError(t, biz.ScaleDeployments(context.Background(), mc, current, old))
		})
	}
}
