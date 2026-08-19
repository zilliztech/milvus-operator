package controllers

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func newStatefulSetTestMilvus() v1beta1.Milvus {
	mc := v1beta1.Milvus{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "mc"},
	}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	pvc := v1beta1.Values{}
	_ = pvc.FromObject(corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "qn-data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		},
	})
	mc.Spec.Com.QueryNode.StatefulSet = &v1beta1.QueryNodeStatefulSet{
		Enabled:              true,
		VolumeClaimTemplates: []v1beta1.Values{pvc},
	}
	return mc
}

func TestQueryNodeUsesStatefulSet(t *testing.T) {
	mc := newStatefulSetTestMilvus()
	assert.True(t, queryNodeUsesStatefulSet(mc, QueryNode))
	assert.False(t, queryNodeUsesStatefulSet(mc, DataNode))

	mc.Spec.Com.QueryNode.StatefulSet.Enabled = false
	assert.False(t, queryNodeUsesStatefulSet(mc, QueryNode))
}

func TestUpdateStatefulSet(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mc := newStatefulSetTestMilvus()
	replicas := int32(3)
	mc.Spec.Com.QueryNode.Replicas = &replicas

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: QueryNode.GetDeploymentName(mc.Name), Namespace: mc.Namespace},
	}
	err := r.updateStatefulSet(env.ctx, mc, sts, QueryNode)
	assert.NoError(t, err)

	// selector + serviceName
	assert.NotNil(t, sts.Spec.Selector)
	assert.Equal(t, QueryNode.Name, sts.Spec.Selector.MatchLabels[AppLabelComponent])
	assert.Equal(t, QueryNode.GetStatefulSetServiceName(mc.Name), sts.Spec.ServiceName)
	assert.Equal(t, appsv1.RollingUpdateStatefulSetStrategyType, sts.Spec.UpdateStrategy.Type)

	// replicas
	assert.NotNil(t, sts.Spec.Replicas)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)

	// volumeClaimTemplates rendered
	assert.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	assert.Equal(t, "qn-data", sts.Spec.VolumeClaimTemplates[0].Name)
	// template carries component labels so provisioned PVCs are findable
	assert.Equal(t, QueryNode.Name, sts.Spec.VolumeClaimTemplates[0].Labels[AppLabelComponent])

	// pod template: querynode container + config/tools volumes + config init container
	containerIdx := GetContainerIndex(sts.Spec.Template.Spec.Containers, QueryNode.Name)
	assert.GreaterOrEqual(t, containerIdx, 0)
	assert.GreaterOrEqual(t, GetVolumeIndex(sts.Spec.Template.Spec.Volumes, MilvusConfigVolumeName), 0)
	assert.GreaterOrEqual(t, GetVolumeIndex(sts.Spec.Template.Spec.Volumes, ToolsVolumeName), 0)
	assert.GreaterOrEqual(t, GetContainerIndex(sts.Spec.Template.Spec.InitContainers, configContainerName), 0)
}

func TestUpdateStatefulSetWithGroup(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mc := newStatefulSetTestMilvus()
	groupReplicas := int32(2)
	group := v1beta1.DeploymentGroup{Name: "g1", Replicas: &groupReplicas}
	workload := QueryNode
	workload.DeploymentGroup = &group

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: workload.GetDeploymentName(mc.Name), Namespace: mc.Namespace},
	}
	err := r.updateStatefulSet(env.ctx, mc, sts, workload)
	assert.NoError(t, err)

	// group-scoped name, service, selector
	assert.Equal(t, "mc-milvus-querynode-g1", sts.Name)
	assert.Equal(t, "mc-milvus-querynode-g1", sts.Spec.ServiceName)
	assert.Equal(t, "g1", sts.Spec.Selector.MatchLabels[v1beta1.DeploymentGroupLabel])
	// per-group replicas honored
	assert.NotNil(t, sts.Spec.Replicas)
	assert.Equal(t, int32(2), *sts.Spec.Replicas)
	// group label stamped on the PVC template too
	assert.Equal(t, "g1", sts.Spec.VolumeClaimTemplates[0].Labels[v1beta1.DeploymentGroupLabel])
}

func TestUpdateStatefulSetPausedAndManualMode(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler

	t.Run("manual mode leaves replicas unmanaged", func(t *testing.T) {
		mc := newStatefulSetTestMilvus()
		mc.Spec.Com.EnableManualMode = true
		specReplicas := int32(5)
		mc.Spec.Com.QueryNode.Replicas = &specReplicas
		sts := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: QueryNode.GetDeploymentName(mc.Name), Namespace: mc.Namespace},
		}
		manual := int32(2)
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: QueryNode.GetSelectorLabels(mc.Name)}
		sts.Spec.Replicas = &manual
		err := r.updateStatefulSet(env.ctx, mc, sts, QueryNode)
		assert.NoError(t, err)
		assert.Equal(t, int32(2), *sts.Spec.Replicas)
	})

	t.Run("paused freezes pod template on existing sts", func(t *testing.T) {
		mc := newStatefulSetTestMilvus()
		mc.Spec.Com.QueryNode.Paused = true
		sts := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: QueryNode.GetDeploymentName(mc.Name), Namespace: mc.Namespace},
		}
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: QueryNode.GetSelectorLabels(mc.Name)}
		one := int32(1)
		sts.Spec.Replicas = &one
		sts.Spec.Template.Spec.Containers = []corev1.Container{{Name: "frozen"}}
		err := r.updateStatefulSet(env.ctx, mc, sts, QueryNode)
		assert.NoError(t, err)
		assert.Len(t, sts.Spec.Template.Spec.Containers, 1)
		assert.Equal(t, "frozen", sts.Spec.Template.Spec.Containers[0].Name)
	})

	t.Run("paused still renders on create", func(t *testing.T) {
		mc := newStatefulSetTestMilvus()
		mc.Spec.Com.QueryNode.Paused = true
		sts := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: QueryNode.GetDeploymentName(mc.Name), Namespace: mc.Namespace},
		}
		err := r.updateStatefulSet(env.ctx, mc, sts, QueryNode)
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, GetContainerIndex(sts.Spec.Template.Spec.Containers, QueryNode.Name), 0)
	})
}

func TestSynthesizeStatefulSetStatus(t *testing.T) {
	desired := int32(2)
	base := func() *appsv1.StatefulSet {
		sts := &appsv1.StatefulSet{}
		sts.Generation = 5
		sts.Spec.Replicas = &desired
		sts.Spec.Template.Spec.Containers = []corev1.Container{{Name: QueryNode.Name, Image: "milvus:test"}}
		return sts
	}

	t.Run("complete when rolled out and ready", func(t *testing.T) {
		sts := base()
		sts.Status.ObservedGeneration = 5
		sts.Status.Replicas = 2
		sts.Status.UpdatedReplicas = 2
		sts.Status.ReadyReplicas = 2
		sts.Status.AvailableReplicas = 2
		sts.Status.CurrentRevision = "rev-1"
		sts.Status.UpdateRevision = "rev-1"

		status := synthesizeStatefulSetStatus(QueryNode, sts)
		assert.Equal(t, "milvus:test", status.Image)
		assert.Equal(t, v1beta1.DeploymentComplete, status.GetState())
		assert.True(t, DeploymentReady(status.Status))
		// CRD schema requires condition timestamps to be non-null strings;
		// a zero metav1.Time serializes to "null" and is rejected by the apiserver.
		for _, c := range status.Status.Conditions {
			assert.False(t, c.LastTransitionTime.IsZero(), "condition %s LastTransitionTime must be set", c.Type)
			assert.False(t, c.LastUpdateTime.IsZero(), "condition %s LastUpdateTime must be set", c.Type)
		}
	})

	t.Run("progressing when revisions differ", func(t *testing.T) {
		sts := base()
		sts.Status.ObservedGeneration = 5
		sts.Status.UpdatedReplicas = 1
		sts.Status.ReadyReplicas = 2
		sts.Status.CurrentRevision = "rev-1"
		sts.Status.UpdateRevision = "rev-2"

		status := synthesizeStatefulSetStatus(QueryNode, sts)
		assert.NotEqual(t, v1beta1.DeploymentComplete, status.GetState())
	})

	t.Run("not observed yet is progressing", func(t *testing.T) {
		sts := base()
		sts.Status.ObservedGeneration = 4
		status := synthesizeStatefulSetStatus(QueryNode, sts)
		assert.Equal(t, v1beta1.DeploymentProgressing, status.GetState())
	})

	t.Run("nil statefulset", func(t *testing.T) {
		status := synthesizeStatefulSetStatus(QueryNode, nil)
		assert.Equal(t, v1beta1.ComponentDeployStatus{}, status)
	})
}

func TestMatchStatefulSetPVCOrdinal(t *testing.T) {
	prefixes := []string{"qn-data-mc-milvus-querynode-", "cache-mc-milvus-querynode-"}
	cases := []struct {
		name   string
		want   int32
		wantOk bool
	}{
		{"qn-data-mc-milvus-querynode-3", 3, true},
		{"cache-mc-milvus-querynode-0", 0, true},
		{"qn-data-mc-milvus-querynode-12", 12, true},
		{"qn-data-mc-milvus-querynode-", 0, false},    // no ordinal
		{"qn-data-mc-milvus-querynode-abc", 0, false}, // non-numeric
		{"other-mc-milvus-querynode-1", 0, false},     // wrong prefix
	}
	for _, c := range cases {
		got, ok := matchStatefulSetPVCOrdinal(c.name, prefixes)
		assert.Equal(t, c.wantOk, ok, c.name)
		if c.wantOk {
			assert.Equal(t, c.want, got, c.name)
		}
	}
}

func TestCleanupScaledDownQueryNodePVCs(t *testing.T) {
	stsName := QueryNode.GetDeploymentName("mc")
	newSts := func(replicas int32) *appsv1.StatefulSet {
		sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: "ns"}}
		sts.Spec.Replicas = &replicas
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: QueryNode.GetSelectorLabels("mc")}
		sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
			{ObjectMeta: metav1.ObjectMeta{Name: "qn-data"}},
		}
		return sts
	}
	pvc := func(ordinal int) corev1.PersistentVolumeClaim {
		return corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{
			Name: "qn-data-" + stsName + "-" + itoa(ordinal), Namespace: "ns",
		}}
	}

	t.Run("deletes only ordinals >= desired replicas", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mc := newStatefulSetTestMilvus()

		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*corev1.PersistentVolumeClaimList).Items = []corev1.PersistentVolumeClaim{
					pvc(0), pvc(1), pvc(2), pvc(3),
				}
				return nil
			})
		var deleted []string
		mockClient.EXPECT().
			Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaim{})).
			DoAndReturn(func(_ context.Context, obj client.Object, _ ...any) error {
				deleted = append(deleted, obj.(*corev1.PersistentVolumeClaim).Name)
				return nil
			}).Times(2)

		err := r.cleanupScaledDownQueryNodePVCs(env.ctx, mc, newSts(2))
		assert.NoError(t, err)
		assert.ElementsMatch(t, []string{
			"qn-data-" + stsName + "-2",
			"qn-data-" + stsName + "-3",
		}, deleted)
	})

	t.Run("no templates is no-op", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mc := newStatefulSetTestMilvus()
		sts := newSts(2)
		sts.Spec.VolumeClaimTemplates = nil
		err := r.cleanupScaledDownQueryNodePVCs(env.ctx, mc, sts)
		assert.NoError(t, err)
	})
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func TestReconcileQueryNodePVCExpansion(t *testing.T) {
	stsName := QueryNode.GetDeploymentName("mc")
	// mc with a 100Gi volumeClaimTemplate named qn-data
	newMc := func(size string) v1beta1.Milvus {
		mc := newStatefulSetTestMilvus()
		pvc := v1beta1.Values{}
		_ = pvc.FromObject(corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "qn-data"},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
				},
			},
		})
		mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{pvc}
		return mc
	}
	newSts := func() *appsv1.StatefulSet {
		sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: "ns"}}
		sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: QueryNode.GetSelectorLabels("mc")}
		sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{{ObjectMeta: metav1.ObjectMeta{Name: "qn-data"}}}
		return sts
	}
	existingPVC := func(size string) corev1.PersistentVolumeClaim {
		return corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "qn-data-" + stsName + "-0", Namespace: "ns"},
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
				},
			},
		}
	}

	t.Run("expands when desired greater", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*corev1.PersistentVolumeClaimList).Items = []corev1.PersistentVolumeClaim{existingPVC("100Gi")}
				return nil
			})
		var patched resource.Quantity
		mockClient.EXPECT().
			Update(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaim{})).
			DoAndReturn(func(_ context.Context, obj client.Object, _ ...any) error {
				patched = obj.(*corev1.PersistentVolumeClaim).Spec.Resources.Requests[corev1.ResourceStorage]
				return nil
			})
		err := r.reconcileQueryNodePVCExpansion(env.ctx, newMc("200Gi"), newSts())
		assert.NoError(t, err)
		assert.Equal(t, "200Gi", patched.String())
	})

	t.Run("no-op when equal", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*corev1.PersistentVolumeClaimList).Items = []corev1.PersistentVolumeClaim{existingPVC("100Gi")}
				return nil
			})
		// no Update expected
		err := r.reconcileQueryNodePVCExpansion(env.ctx, newMc("100Gi"), newSts())
		assert.NoError(t, err)
	})

	t.Run("skips shrink", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*corev1.PersistentVolumeClaimList).Items = []corev1.PersistentVolumeClaim{existingPVC("200Gi")}
				return nil
			})
		// no Update expected (shrink skipped)
		err := r.reconcileQueryNodePVCExpansion(env.ctx, newMc("100Gi"), newSts())
		assert.NoError(t, err)
	})

	t.Run("patch failure is non-fatal", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*corev1.PersistentVolumeClaimList).Items = []corev1.PersistentVolumeClaim{existingPVC("100Gi")}
				return nil
			})
		mockClient.EXPECT().
			Update(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaim{})).
			Return(errors.New("storageclass does not allow expansion"))
		// error is logged, not returned
		err := r.reconcileQueryNodePVCExpansion(env.ctx, newMc("200Gi"), newSts())
		assert.NoError(t, err)
	})

	t.Run("no templates is no-op", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		sts := newSts()
		sts.Spec.VolumeClaimTemplates = nil
		err := r.reconcileQueryNodePVCExpansion(env.ctx, newMc("200Gi"), sts)
		assert.NoError(t, err)
	})
}

func TestReconcileComponentStatefulSet_Create(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	mc := newStatefulSetTestMilvus()

	// headless service: not found -> create
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, ""))
	mockClient.EXPECT().
		Create(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
		Return(nil)
	// statefulset: not found -> create
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, ""))
	mockClient.EXPECT().
		Create(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
		DoAndReturn(func(_ context.Context, obj client.Object, _ ...any) error {
			sts := obj.(*appsv1.StatefulSet)
			assert.Equal(t, "mc-milvus-querynode", sts.Name)
			assert.Len(t, sts.Spec.VolumeClaimTemplates, 1)
			return nil
		})

	err := r.ReconcileComponentStatefulSet(env.ctx, mc, QueryNode)
	assert.NoError(t, err)
}

func TestReconcileComponentStatefulSet_Update(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	mc := newStatefulSetTestMilvus()
	replicas := int32(3)
	mc.Spec.Com.QueryNode.Replicas = &replicas

	// headless service already exists and is up to date -> no update.
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
		DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...any) error {
			svc := obj.(*corev1.Service)
			svc.Name = QueryNode.GetStatefulSetServiceName(mc.Name)
			svc.Namespace = mc.Namespace
			_ = r.updateStatefulSetService(mc, svc, QueryNode, QueryNode.GetSelectorLabels(mc.Name))
			return nil
		})
	// statefulset exists with stale replicas -> updated.
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
		DoAndReturn(func(_ context.Context, _ client.ObjectKey, obj client.Object, _ ...any) error {
			sts := obj.(*appsv1.StatefulSet)
			sts.Name = QueryNode.GetDeploymentName(mc.Name)
			sts.Namespace = mc.Namespace
			old := int32(1)
			sts.Spec.Replicas = &old
			return nil
		})
	mockClient.EXPECT().
		Update(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
		DoAndReturn(func(_ context.Context, obj client.Object, _ ...any) error {
			assert.Equal(t, int32(3), *obj.(*appsv1.StatefulSet).Spec.Replicas)
			return nil
		})
	// scaled-down PVC cleanup lists PVCs (none). Expansion returns early since
	// the default template declares no storage request.
	mockClient.EXPECT().
		List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
		Return(nil)

	err := r.ReconcileComponentStatefulSet(env.ctx, mc, QueryNode)
	assert.NoError(t, err)
}

func TestReconcileComponentStatefulSet_GetError(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	mc := newStatefulSetTestMilvus()

	// service ok, statefulset Get errors.
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, ""))
	mockClient.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).Return(nil)
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
		Return(errors.New("boom"))

	err := r.ReconcileComponentStatefulSet(env.ctx, mc, QueryNode)
	assert.Error(t, err)
}

func TestGetStatefulSetForComponent(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	mc := newStatefulSetTestMilvus()

	t.Run("found", func(t *testing.T) {
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
			Return(nil)
		sts, err := getStatefulSetForComponent(env.ctx, r.Client, mc, QueryNode)
		assert.NoError(t, err)
		assert.NotNil(t, sts)
	})

	t.Run("not found returns nil", func(t *testing.T) {
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, ""))
		sts, err := getStatefulSetForComponent(env.ctx, r.Client, mc, QueryNode)
		assert.NoError(t, err)
		assert.Nil(t, sts)
	})

	t.Run("error propagates", func(t *testing.T) {
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
			Return(errors.New("boom"))
		_, err := getStatefulSetForComponent(env.ctx, r.Client, mc, QueryNode)
		assert.Error(t, err)
	})
}

func TestRenderVolumeClaimTemplatesError(t *testing.T) {
	mc := newStatefulSetTestMilvus()
	bad := v1beta1.Values{}
	bad.Data = map[string]any{"metadata": "not-an-object"}
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{bad}
	_, err := renderVolumeClaimTemplates(mc)
	assert.Error(t, err)
}

func TestCleanupQueryNodeWorkloadMode(t *testing.T) {
	t.Run("statefulset enabled deletes legacy deployments", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mc := newStatefulSetTestMilvus()

		legacy := appsv1.Deployment{}
		legacy.Name = "mc-milvus-querynode"
		legacy.OwnerReferences = []metav1.OwnerReference{
			{UID: mc.UID, Controller: boolPtr(true), Kind: "Milvus", Name: mc.Name},
		}
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*appsv1.DeploymentList).Items = []appsv1.Deployment{legacy}
				return nil
			})
		mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).Return(nil)
		// stale-STS pruning lists StatefulSets; the desired one is kept.
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSetList{}), gomock.Any(), gomock.Any()).
			Return(nil)

		err := r.cleanupQueryNodeWorkloadMode(env.ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("statefulset disabled deletes all statefulsets and services", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mc := newStatefulSetTestMilvus()
		mc.Spec.Com.QueryNode.StatefulSet.Enabled = false

		existing := appsv1.StatefulSet{}
		existing.Name = "mc-milvus-querynode"
		existing.Namespace = "ns"
		existing.OwnerReferences = []metav1.OwnerReference{
			{UID: mc.UID, Controller: boolPtr(true), Kind: "Milvus", Name: mc.Name},
		}
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSetList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{existing}
				return nil
			})
		mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).Return(nil)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
			Return(nil)
		mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).Return(nil)

		err := r.cleanupQueryNodeWorkloadMode(env.ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("statefulset disabled no-op when none exist", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mc := newStatefulSetTestMilvus()
		mc.Spec.Com.QueryNode.StatefulSet.Enabled = false

		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSetList{}), gomock.Any(), gomock.Any()).
			Return(nil)

		err := r.cleanupQueryNodeWorkloadMode(env.ctx, mc)
		assert.NoError(t, err)
	})

	t.Run("prunes statefulset of a removed group", func(t *testing.T) {
		env := newTestEnv(t)
		defer env.checkMocks()
		r := env.Reconciler
		mockClient := env.MockClient
		mc := newStatefulSetTestMilvus()
		// Desired topology: only group g1.
		replicas := int32(1)
		mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "g1", Replicas: &replicas}}

		// Cluster has both g1 (desired) and g2 (stale).
		mkSts := func(name string) appsv1.StatefulSet {
			sts := appsv1.StatefulSet{}
			sts.Name = name
			sts.Namespace = "ns"
			sts.OwnerReferences = []metav1.OwnerReference{
				{UID: mc.UID, Controller: boolPtr(true), Kind: "Milvus", Name: mc.Name},
			}
			return sts
		}
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).
			Return(nil)
		mockClient.EXPECT().
			List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSetList{}), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
				raw.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{
					mkSts("mc-milvus-querynode-g1"),
					mkSts("mc-milvus-querynode-g2"),
				}
				return nil
			})
		var deleted string
		mockClient.EXPECT().
			Delete(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).
			DoAndReturn(func(_ context.Context, obj client.Object, _ ...any) error {
				deleted = obj.(*appsv1.StatefulSet).Name
				return nil
			})
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
			Return(k8sErrors.NewNotFound(schema.GroupResource{}, ""))

		err := r.cleanupQueryNodeWorkloadMode(env.ctx, mc)
		assert.NoError(t, err)
		assert.Equal(t, "mc-milvus-querynode-g2", deleted)
	})
}

func TestCleanupStaleStatefulSetReclaimsPVCs(t *testing.T) {
	env := newTestEnv(t)
	defer env.checkMocks()
	r := env.Reconciler
	mockClient := env.MockClient
	mc := newStatefulSetTestMilvus()
	mc.Spec.Com.QueryNode.StatefulSet.Enabled = false // revert -> the ungrouped STS is stale

	stale := appsv1.StatefulSet{}
	stale.Name = "mc-milvus-querynode"
	stale.Namespace = "ns"
	stale.OwnerReferences = []metav1.OwnerReference{
		{UID: mc.UID, Controller: boolPtr(true), Kind: "Milvus", Name: mc.Name},
	}
	stale.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		{ObjectMeta: metav1.ObjectMeta{Name: "qn-local-data"}},
	}
	mockClient.EXPECT().
		List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSetList{}), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
			raw.(*appsv1.StatefulSetList).Items = []appsv1.StatefulSet{stale}
			return nil
		})
	mockClient.EXPECT().Delete(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.StatefulSet{})).Return(nil)
	// PVC list for reclaim: two belonging to the stale STS, plus a group PVC that
	// must NOT be deleted (its "-g1-0" tail is non-numeric under the stale prefix).
	mockClient.EXPECT().
		List(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaimList{}), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, raw client.ObjectList, _ ...any) error {
			mk := func(n string) corev1.PersistentVolumeClaim {
				return corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: "ns"}}
			}
			raw.(*corev1.PersistentVolumeClaimList).Items = []corev1.PersistentVolumeClaim{
				mk("qn-local-data-mc-milvus-querynode-0"),
				mk("qn-local-data-mc-milvus-querynode-1"),
				mk("qn-local-data-mc-milvus-querynode-g1-0"), // group PVC, must survive
			}
			return nil
		})
	var deletedPVCs []string
	mockClient.EXPECT().
		Delete(gomock.Any(), gomock.AssignableToTypeOf(&corev1.PersistentVolumeClaim{})).
		DoAndReturn(func(_ context.Context, obj client.Object, _ ...any) error {
			deletedPVCs = append(deletedPVCs, obj.(*corev1.PersistentVolumeClaim).Name)
			return nil
		}).Times(2)
	mockClient.EXPECT().
		Get(gomock.Any(), gomock.Any(), gomock.AssignableToTypeOf(&corev1.Service{})).
		Return(k8sErrors.NewNotFound(schema.GroupResource{}, ""))

	err := r.cleanupStaleQueryNodeStatefulSets(env.ctx, mc)
	assert.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"qn-local-data-mc-milvus-querynode-0",
		"qn-local-data-mc-milvus-querynode-1",
	}, deletedPVCs, "must delete the stale STS's PVCs but not the group PVC")
}
