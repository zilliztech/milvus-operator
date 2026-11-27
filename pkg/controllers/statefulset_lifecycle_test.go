package controllers

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func TestStatefulSetAttachDetachLifecycle(t *testing.T) {
	for _, component := range []MilvusComponent{QueryNode, DataNode, IndexNode} {
		for _, grouped := range []bool{false, true} {
			if grouped && component.Name == IndexNodeName {
				continue
			}
			name := component.Name
			if grouped {
				name += "/group"
			}
			t.Run(name, func(t *testing.T) {
				r, mc, component, sts := storageRemovalFixture(t, grouped, component)
				ctx := context.Background()
				// Model an existing diskless StatefulSet. The CR now adds qn-data.
				sts.Spec.VolumeClaimTemplates = nil
				for i := range sts.Spec.Template.Spec.Containers {
					removeVolumeMounts(&sts.Spec.Template.Spec.Containers[i].VolumeMounts, "qn-data")
				}
				require.NoError(t, r.Update(ctx, sts))
				reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
				require.ErrorIs(t, reconcile(), ErrRequeue) // save intent
				require.ErrorIs(t, reconcile(), ErrRequeue) // validate + delete
				require.ErrorIs(t, reconcile(), ErrRequeue) // await foreground GC
				finishStorageRemovalDeletion(t, r, sts)
				require.NoError(t, reconcile())
				current := &appsv1.StatefulSet{}
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
				require.NotEqual(t, sts.UID, current.UID)
				require.Len(t, current.Spec.VolumeClaimTemplates, 1)
				require.Equal(t, "qn-data", current.Spec.VolumeClaimTemplates[0].Name)
				index := GetContainerIndex(current.Spec.Template.Spec.Containers, component.Name)
				require.GreaterOrEqual(t, index, 0)
				require.Contains(t, current.Spec.Template.Spec.Containers[index].VolumeMounts,
					corev1.VolumeMount{Name: "qn-data", MountPath: "/data"})
				require.ErrorIs(t, reconcile(), ErrRequeue) // finish intent
				require.NoError(t, reconcile())

				// Fake API has no StatefulSet controller; model the PVC it creates.
				claim := storageRemovalPVC(t, r, current, "qn-data-"+current.Name+"-0")
				original := getComponentStatefulSetSpec(mc, component).VolumeClaimTemplates
				getComponentStatefulSetSpec(mc, component).VolumeClaimTemplates = nil
				require.ErrorIs(t, reconcile(), ErrRequeue)
				require.ErrorIs(t, reconcile(), ErrRequeue)
				finishStorageRemovalDeletion(t, r, current)
				require.ErrorIs(t, reconcile(), ErrRequeue) // delete removed PVC
				require.NoError(t, reconcile())
				require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(claim), &corev1.PersistentVolumeClaim{})))
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
				require.Empty(t, current.Spec.VolumeClaimTemplates)
				index = GetContainerIndex(current.Spec.Template.Spec.Containers, component.Name)
				require.NotContains(t, current.Spec.Template.Spec.Containers[index].VolumeMounts,
					corev1.VolumeMount{Name: "qn-data", MountPath: "/data"})
				// Keeping the retired mount in the CR must not cause a second
				// rollout after the diskless replacement has started serving.
				replacementTemplate := current.Spec.Template.DeepCopy()
				require.ErrorIs(t, reconcile(), ErrRequeue)
				for i := 0; i < 3; i++ {
					require.NoError(t, reconcile())
					require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
					require.Equal(t, replacementTemplate, &current.Spec.Template, "unchanged CR must preserve replacement Pod template")
				}

				// Reattaching a retired name must restore its mount and claim template.
				getComponentStatefulSetSpec(mc, component).VolumeClaimTemplates = original
				require.ErrorIs(t, reconcile(), ErrRequeue)
				require.ErrorIs(t, reconcile(), ErrRequeue)
				finishStorageRemovalDeletion(t, r, current)
				require.NoError(t, reconcile())
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
				require.Len(t, current.Spec.VolumeClaimTemplates, 1)
				index = GetContainerIndex(current.Spec.Template.Spec.Containers, component.Name)
				require.Contains(t, current.Spec.Template.Spec.Containers[index].VolumeMounts,
					corev1.VolumeMount{Name: "qn-data", MountPath: "/data"})
			})
		}
	}
}

func TestStatefulSetAttachPreservesExistingPVC(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	claim := storageRemovalPVC(t, r, sts, "qn-data-"+sts.Name+"-0")
	additional := sts.Spec.VolumeClaimTemplates[0].DeepCopy()
	additional.Name = "extra"
	value := v1beta1.Values{}
	require.NoError(t, value.FromObject(additional))
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = append(mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates, value)
	mc.Spec.Com.QueryNode.VolumeMounts = append(mc.Spec.Com.QueryNode.VolumeMounts, corev1.VolumeMount{Name: "extra", MountPath: "/extra"})
	reconcile := func() error { return r.ReconcileComponentStatefulSet(ctx, mc, component) }
	require.ErrorIs(t, reconcile(), ErrRequeue)
	require.ErrorIs(t, reconcile(), ErrRequeue)
	finishStorageRemovalDeletion(t, r, sts)
	require.NoError(t, reconcile())
	remaining := &corev1.PersistentVolumeClaim{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(claim), remaining))
	require.Equal(t, claim.UID, remaining.UID)
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Len(t, current.Spec.VolumeClaimTemplates, 2)
}

func TestStatefulSetSizeChangeDoesNotRebuild(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	template := sts.Spec.VolumeClaimTemplates[0].DeepCopy()
	template.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")}
	value := v1beta1.Values{}
	require.NoError(t, value.FromObject(template))
	mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = []v1beta1.Values{value}
	require.NoError(t, r.ReconcileComponentStatefulSet(ctx, mc, component))
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Equal(t, sts.UID, current.UID)
	require.Nil(t, current.DeletionTimestamp)
}

func TestStatefulSetInvalidReplacementPreservesWorkload(t *testing.T) {
	r, mc, component, sts := storageRemovalFixture(t, false)
	ctx := context.Background()
	sts.Spec.VolumeClaimTemplates = nil
	require.NoError(t, r.Update(ctx, sts))
	validationError := errors.New("API server rejected new PVC template")
	r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			options := &client.CreateOptions{}
			for _, opt := range opts {
				opt.ApplyToCreate(options)
			}
			if _, ok := obj.(*appsv1.StatefulSet); ok {
				require.Equal(t, []string{metav1.DryRunAll}, options.DryRun)
				return validationError
			}
			return c.Create(ctx, obj, opts...)
		},
	})
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
	require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), validationError)
	current := &appsv1.StatefulSet{}
	require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
	require.Nil(t, current.DeletionTimestamp)
	require.Equal(t, sts.UID, current.UID)
}

func TestStatefulSetChildValidationPreservesWorkload(t *testing.T) {
	for _, source := range []string{"StatefulSet", "Deployment"} {
		for _, rejected := range []string{"Pod", "PVC"} {
			t.Run(source+"/"+rejected, func(t *testing.T) {
				r, mc, component, sts := storageRemovalFixture(t, false)
				mc.Annotations = map[string]string{queryNodeRebuildGracePeriodAnnotation: "60"}
				ctx := context.Background()
				var workload client.Object = sts
				if source == "Deployment" {
					sts.Finalizers = nil
					require.NoError(t, r.Update(ctx, sts))
					require.NoError(t, r.Delete(ctx, sts))
					workload = &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
						Name: sts.Name, Namespace: mc.Namespace, UID: "legacy-deployment",
						Labels: sts.Labels, OwnerReferences: sts.OwnerReferences,
					}}
					require.NoError(t, r.Create(ctx, workload))
				} else {
					sts.Spec.VolumeClaimTemplates = nil
					require.NoError(t, r.Update(ctx, sts))
				}
				validationError := errors.New("child resource rejected by API server")
				validated := false
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						options := &client.CreateOptions{}
						for _, opt := range opts {
							opt.ApplyToCreate(options)
						}
						if len(options.DryRun) == 0 {
							return c.Create(ctx, obj, opts...)
						}
						require.Equal(t, []string{metav1.DryRunAll}, options.DryRun)
						switch child := obj.(type) {
						case *corev1.Pod:
							require.Equal(t, mc.Namespace, child.Namespace)
							idx := GetVolumeIndex(child.Spec.Volumes, "qn-data")
							require.GreaterOrEqual(t, idx, 0)
							require.Equal(t, "qn-data-"+sts.Name+"-0", child.Spec.Volumes[idx].PersistentVolumeClaim.ClaimName)
							if rejected == "Pod" {
								validated = true
								return validationError
							}
						case *corev1.PersistentVolumeClaim:
							require.Equal(t, mc.Namespace, child.Namespace)
							require.Empty(t, child.Name)
							require.NotEmpty(t, child.GenerateName)
							if rejected == "PVC" {
								validated = true
								return validationError
							}
						}
						return nil // Model an API server accepting the StatefulSet.
					},
				})
				if source == "Deployment" {
					require.ErrorIs(t, r.cleanupStatefulSetWorkloadModes(ctx, mc), validationError)
				} else {
					require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), ErrRequeue)
					require.ErrorIs(t, r.ReconcileComponentStatefulSet(ctx, mc, component), validationError)
				}
				require.True(t, validated)
				originalUID := workload.GetUID()
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(workload), workload))
				require.Nil(t, workload.GetDeletionTimestamp())
				require.Equal(t, originalUID, workload.GetUID())
			})
		}
	}
}

func TestDeploymentToStatefulSetLifecycle(t *testing.T) {
	for _, withDisk := range []bool{false, true} {
		name := "diskless"
		if withDisk {
			name = "with PVC"
		}
		t.Run(name, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			sts.Finalizers = nil
			require.NoError(t, r.Update(ctx, sts))
			require.NoError(t, r.Delete(ctx, sts))
			if !withDisk {
				mc.Spec.Com.QueryNode.StatefulSet.VolumeClaimTemplates = nil
				mc.Spec.Com.QueryNode.VolumeMounts = nil
			}
			var legacy []*appsv1.Deployment
			for _, suffix := range []string{"", "-1"} {
				deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
					Name: sts.Name + suffix, Namespace: mc.Namespace, Labels: component.GetSelectorLabels(mc.Name),
					OwnerReferences: sts.OwnerReferences, Finalizers: []string{"test/foreground"},
				}}
				require.NoError(t, r.Create(ctx, deployment))
				legacy = append(legacy, deployment)
			}
			foreign := legacy[0].DeepCopy()
			foreign.Name += "-foreign"
			foreign.ResourceVersion = ""
			foreign.UID = ""
			foreign.OwnerReferences = nil
			require.NoError(t, r.Create(ctx, foreign))
			require.ErrorIs(t, r.cleanupStatefulSetWorkloadModes(ctx, mc), ErrRequeue)
			require.ErrorIs(t, r.cleanupStatefulSetWorkloadModes(ctx, mc), ErrRequeue)
			require.True(t, kerrors.IsNotFound(r.Get(ctx, client.ObjectKeyFromObject(sts), &appsv1.StatefulSet{})))
			for _, deployment := range legacy {
				current := &appsv1.Deployment{}
				require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(deployment), current))
				require.NotNil(t, current.DeletionTimestamp)
				current.Finalizers = nil
				require.NoError(t, r.Update(ctx, current))
			}
			require.NoError(t, r.cleanupStatefulSetWorkloadModes(ctx, mc))
			require.NoError(t, r.ReconcileComponentStatefulSet(ctx, mc, component))
			current := &appsv1.StatefulSet{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(sts), current))
			if withDisk {
				require.Len(t, current.Spec.VolumeClaimTemplates, 1)
			} else {
				require.Empty(t, current.Spec.VolumeClaimTemplates)
			}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(foreign), foreign))
			require.Nil(t, foreign.DeletionTimestamp)
		})
	}
}

func TestStatefulSetHPATarget(t *testing.T) {
	for _, component := range []MilvusComponent{QueryNode, DataNode, IndexNode} {
		t.Run(component.Name, func(t *testing.T) {
			r, mc, component, _ := storageRemovalFixture(t, false, component)
			require.NoError(t, autoscalingv2.AddToScheme(r.Scheme))
			hpaSpec := &v1beta1.HPASpec{MinReplicas: int32Ptr(2), MaxReplicas: 5}
			switch component.Name {
			case QueryNodeName:
				mc.Spec.Com.QueryNode.HPA = hpaSpec
			case DataNodeName:
				mc.Spec.Com.DataNode.HPA = hpaSpec
			case IndexNodeName:
				mc.Spec.Com.IndexNode.HPA = hpaSpec
			}
			ctx := context.Background()
			// Same target name, different Kind: catches name-only comparisons.
			existing, err := r.buildHPA(ctx, mc, component, hpaSpec, component.GetDeploymentName(mc.Name))
			require.NoError(t, err)
			existing.Spec.ScaleTargetRef.Kind = "Deployment"
			require.NoError(t, r.Create(ctx, existing))
			require.NoError(t, r.reconcileComponentHPA(ctx, mc, component))
			current := &autoscalingv2.HorizontalPodAutoscaler{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(existing), current))
			require.Equal(t, autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1", Kind: "StatefulSet", Name: component.GetDeploymentName(mc.Name),
			}, current.Spec.ScaleTargetRef)
		})
	}
}

func TestDeploymentToStatefulSetPreservesWorkloadBeforeValidation(t *testing.T) {
	for _, paused := range []bool{false, true} {
		name := "invalid replacement"
		if paused {
			name = "paused"
		}
		t.Run(name, func(t *testing.T) {
			r, mc, component, sts := storageRemovalFixture(t, false)
			ctx := context.Background()
			mc.Spec.Com.QueryNode.Paused = paused
			deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
				Name: sts.Name, Namespace: mc.Namespace, Labels: component.GetSelectorLabels(mc.Name),
				OwnerReferences: sts.OwnerReferences,
			}}
			require.NoError(t, r.Create(ctx, deployment))
			validationError := errors.New("replacement rejected by admission")
			validations := 0
			r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					if _, ok := obj.(*appsv1.StatefulSet); ok {
						options := &client.CreateOptions{}
						for _, opt := range opts {
							opt.ApplyToCreate(options)
						}
						require.Equal(t, []string{metav1.DryRunAll}, options.DryRun)
						validations++
						return validationError
					}
					return c.Create(ctx, obj, opts...)
				},
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					t.Fatal("must not delete a workload before successful validation")
					return nil
				},
			})
			err := r.cleanupStatefulSetWorkloadModes(ctx, mc)
			if paused {
				require.ErrorIs(t, err, ErrRequeue)
				require.Zero(t, validations)
			} else {
				require.ErrorIs(t, err, validationError)
				require.Equal(t, 1, validations)
			}
			current := &appsv1.Deployment{}
			require.NoError(t, r.Get(ctx, client.ObjectKeyFromObject(deployment), current))
			require.Nil(t, current.DeletionTimestamp)
		})
	}
}
