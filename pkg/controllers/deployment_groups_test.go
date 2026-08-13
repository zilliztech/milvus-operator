package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

const (
	deploymentRevisionAnnotation = "deployment.kubernetes.io/revision"
	kubectlRestartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"
	kubectlLastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
	kubectlDefaultContainer      = "kubectl.kubernetes.io/default-container"
)

func TestGetComponentWorkloadsBySpec(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one, two := int32(1), int32(2)
	mc.Spec.Com.Proxy.Replicas = int32Ptr(99)
	mc.Spec.Com.Proxy.Groups = []v1beta1.DeploymentGroup{
		{Name: "zone-b", Replicas: &two},
		{Name: "zone-a", Replicas: &one},
	}

	workloads := GetComponentWorkloadsBySpec(mc.Spec)
	var proxies []MilvusComponent
	for _, workload := range workloads {
		if workload.Is(Proxy) {
			proxies = append(proxies, workload)
		}
	}
	require.Len(t, proxies, 2)
	assert.Equal(t, "zone-a", proxies[0].GetDeploymentGroupName())
	assert.Equal(t, "zone-b", proxies[1].GetDeploymentGroupName())
	assert.Equal(t, "mc-milvus-proxy-zone-a", proxies[0].GetDeploymentName("mc"))
	assert.Equal(t, int32(1), *proxies[0].GetReplicas(mc.Spec))
	assert.NotEqual(t, proxies[0].GetStateKey(), proxies[1].GetStateKey())
	assert.Equal(t, ProxyName, Proxy.GetStateKey(), "legacy rollout keys must remain unchanged")
}

func TestDeploymentGroupRolloutTopologyMatchesComponentMode(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.Proxy.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}, {Name: "b", Replicas: &one}}
	mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}, {Name: "b", Replicas: &one}}
	mc.Spec.Com.DataNode.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}}

	mc.Spec.Com.RollingMode = v1beta1.RollingModeV2
	v2 := GetExpectedTwoDeployComponents(mc.Spec)
	require.Len(t, v2, 2)
	for _, workload := range v2 {
		assert.True(t, workload.Is(QueryNode))
	}

	mc.Spec.Com.RollingMode = v1beta1.RollingModeV3
	v3 := GetExpectedTwoDeployComponents(mc.Spec)
	assert.Equal(t, GetComponentWorkloadsBySpec(mc.Spec), v3)
	for _, workload := range v3 {
		assert.True(t, componentUsesTwoDeployments(mc, workload))
	}
	var groupedProxy MilvusComponent
	for _, workload := range v3 {
		if workload.Is(Proxy) {
			groupedProxy = workload
			break
		}
	}
	require.NotNil(t, groupedProxy.DeploymentGroup)
	require.Len(t, groupedProxy.GetDependencies(mc.Spec), 1)
	assert.True(t, groupedProxy.GetDependencies(mc.Spec)[0].Is(DataNode))
}

func TestDeploymentGroupMergePrecedence(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.Env = []corev1.EnvVar{{Name: "GLOBAL", Value: "global"}, {Name: "OVERRIDE", Value: "global"}}
	mc.Spec.Com.NodeSelector = map[string]string{"level": "global"}
	mc.Spec.Com.Tolerations = []corev1.Toleration{{Key: "global"}}
	mc.Spec.Com.DataNode.Env = []corev1.EnvVar{{Name: "COMPONENT", Value: "component"}, {Name: "OVERRIDE", Value: "component"}}
	mc.Spec.Com.DataNode.NodeSelector = map[string]string{"level": "component"}
	emptyNodeSelector := map[string]string{}
	emptyTolerations := []corev1.Toleration{}
	mc.Spec.Com.DataNode.Groups = []v1beta1.DeploymentGroup{{
		Name:         "zone-a",
		Replicas:     &one,
		ExtraEnv:     []corev1.EnvVar{{Name: "GROUP", Value: "group"}, {Name: "OVERRIDE", Value: "group"}},
		NodeSelector: &emptyNodeSelector,
		Tolerations:  &emptyTolerations,
		Affinity:     &corev1.Affinity{},
	}}

	var workload MilvusComponent
	for _, candidate := range GetComponentWorkloadsBySpec(mc.Spec) {
		if candidate.Is(DataNode) {
			workload = candidate
			break
		}
	}
	merged := newMilvusDeploymentUpdater(mc, scheme, workload).GetMergedComponentSpec()
	assert.Empty(t, merged.NodeSelector, "an explicit empty group map clears inherited scheduling")
	assert.Empty(t, merged.Tolerations, "an explicit empty group list clears inherited scheduling")
	assert.Equal(t, &corev1.Affinity{}, merged.Affinity)
	env := map[string]string{}
	for _, variable := range merged.Env {
		env[variable.Name] = variable.Value
	}
	assert.Equal(t, "global", env["GLOBAL"])
	assert.Equal(t, "component", env["COMPONENT"])
	assert.Equal(t, "group", env["GROUP"])
	assert.Equal(t, "group", env["OVERRIDE"])
}

func TestUpdateDeploymentForDeploymentGroup(t *testing.T) {
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	external := int32(-1)
	nodeSelector := map[string]string{"topology.kubernetes.io/zone": "zone-a"}
	group := v1beta1.DeploymentGroup{
		Name:         "zone-a",
		Replicas:     &external,
		Labels:       map[string]string{"custom": "label", AppLabelComponent: "invalid-user-value"},
		Annotations:  map[string]string{"custom": "annotation"},
		ExtraEnv:     []corev1.EnvVar{{Name: "GROUP_ENV", Value: "group"}},
		NodeSelector: &nodeSelector,
	}
	workload := Proxy
	workload.DeploymentGroup = &group
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: workload.GetDeploymentName(mc.Name), Namespace: mc.Namespace}}
	deployment.Spec.Replicas = int32Ptr(7)

	err := updateDeployment(deployment, newMilvusDeploymentUpdater(mc, scheme, workload))
	require.NoError(t, err)
	assert.Equal(t, int32(7), *deployment.Spec.Replicas, "replicas=-1 leaves the external HPA in control")
	assert.Equal(t, "zone-a", deployment.Labels[v1beta1.DeploymentGroupLabel])
	assert.Equal(t, "label", deployment.Labels["custom"])
	assert.Equal(t, ProxyName, deployment.Labels[AppLabelComponent])
	assert.Equal(t, "annotation", deployment.Annotations["custom"])
	assert.Equal(t, "zone-a", deployment.Spec.Selector.MatchLabels[v1beta1.DeploymentGroupLabel])
	assert.NotContains(t, deployment.Spec.Selector.MatchLabels, "custom")
	assert.Equal(t, "zone-a", deployment.Spec.Template.Labels[v1beta1.DeploymentGroupLabel])
	assert.Equal(t, "label", deployment.Spec.Template.Labels["custom"])
	assert.Equal(t, "annotation", deployment.Spec.Template.Annotations["custom"])
	assert.Equal(t, *group.NodeSelector, deployment.Spec.Template.Spec.NodeSelector)
	container := deployment.Spec.Template.Spec.Containers[GetContainerIndex(deployment.Spec.Template.Spec.Containers, ProxyName)]
	var groupEnv string
	for _, variable := range container.Env {
		if variable.Name == "GROUP_ENV" {
			groupEnv = variable.Value
		}
	}
	assert.Equal(t, "group", groupEnv)
}

func TestDeploymentGroupMetadataIsAuthoritativeForOneDeployment(t *testing.T) {
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	mc.Spec.Com.Proxy.PodLabels = map[string]string{
		"parent-label": "parent",
	}
	mc.Spec.Com.Proxy.PodAnnotations = map[string]string{
		"parent-annotation": "parent",
	}
	one := int32(1)
	group := v1beta1.DeploymentGroup{
		Name:        "zone-a",
		Replicas:    &one,
		Labels:      map[string]string{"current-group-label": "current"},
		Annotations: map[string]string{"current-group-annotation": "current"},
	}
	workload := Proxy
	workload.DeploymentGroup = &group
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workload.GetDeploymentName(mc.Name),
			Namespace: mc.Namespace,
			Labels: map[string]string{
				"removed-group-label":                     "old",
				"external-label":                          "external",
				AppLabelComponent:                         "wrong-component",
				"deployment.kubernetes.io/future-label":   "deployment-system",
				"kubectl.kubernetes.io/future-label":      "kubectl-system",
				v1beta1.MilvusIO + "future-runtime-label": "milvus-system",
			},
			Annotations: map[string]string{
				"removed-group-annotation":                "old",
				"external-annotation":                     "external",
				AnnotationMilvusGeneration:                "7",
				deploymentRevisionAnnotation:              "4",
				"deployment.kubernetes.io/future-key":     "deployment-system",
				kubectlLastAppliedAnnotation:              "last-applied",
				"kubectl.kubernetes.io/future-key":        "kubectl-system",
				v1beta1.MilvusIO + "future-runtime-state": "milvus-system",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: workload.GetSelectorLabels(mc.Name)},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"removed-group-label":                      "old",
					"external-label":                           "external",
					"parent-label":                             "old-group-value",
					v1beta1.ServiceLabel:                       "false",
					"deployment.kubernetes.io/future-template": "deployment-system",
					"kubectl.kubernetes.io/future-template":    "kubectl-system",
					v1beta1.MilvusIO + "future-template-label": "milvus-system",
				},
				Annotations: map[string]string{
					"removed-group-annotation":                 "old",
					"external-annotation":                      "external",
					"parent-annotation":                        "old-group-value",
					kubectlRestartedAtAnnotation:               "2026-08-13T10:00:00-07:00",
					kubectlDefaultContainer:                    ProxyName,
					AnnotationCheckSum:                         "existing-checksum",
					"deployment.kubernetes.io/future-template": "deployment-system",
					v1beta1.MilvusIO + "future-template-state": "milvus-system",
				},
			}},
		},
	}

	err := updateDeployment(deployment, newMilvusDeploymentUpdater(mc, scheme, workload))
	require.NoError(t, err)

	assert.Equal(t, "current", deployment.Labels["current-group-label"])
	assert.NotContains(t, deployment.Labels, "removed-group-label")
	assert.NotContains(t, deployment.Labels, "external-label")
	assert.Equal(t, ProxyName, deployment.Labels[AppLabelComponent])
	assert.Equal(t, "deployment-system", deployment.Labels["deployment.kubernetes.io/future-label"])
	assert.Equal(t, "kubectl-system", deployment.Labels["kubectl.kubernetes.io/future-label"])
	assert.Equal(t, "milvus-system", deployment.Labels[v1beta1.MilvusIO+"future-runtime-label"])
	assert.Equal(t, "current", deployment.Annotations["current-group-annotation"])
	assert.NotContains(t, deployment.Annotations, "removed-group-annotation")
	assert.NotContains(t, deployment.Annotations, "external-annotation")
	assert.Equal(t, "7", deployment.Annotations[AnnotationMilvusGeneration])
	assert.Equal(t, "4", deployment.Annotations[deploymentRevisionAnnotation])
	assert.Equal(t, "deployment-system", deployment.Annotations["deployment.kubernetes.io/future-key"])
	assert.Equal(t, "last-applied", deployment.Annotations[kubectlLastAppliedAnnotation])
	assert.Equal(t, "kubectl-system", deployment.Annotations["kubectl.kubernetes.io/future-key"])
	assert.Equal(t, "milvus-system", deployment.Annotations[v1beta1.MilvusIO+"future-runtime-state"])

	template := deployment.Spec.Template
	assert.Equal(t, "parent", template.Labels["parent-label"], "removing a group override restores the component value")
	assert.Equal(t, "current", template.Labels["current-group-label"])
	assert.NotContains(t, template.Labels, "removed-group-label")
	assert.NotContains(t, template.Labels, "external-label")
	assert.Equal(t, v1beta1.TrueStr, template.Labels[v1beta1.ServiceLabel])
	assert.Equal(t, "deployment-system", template.Labels["deployment.kubernetes.io/future-template"])
	assert.Equal(t, "kubectl-system", template.Labels["kubectl.kubernetes.io/future-template"])
	assert.Equal(t, "milvus-system", template.Labels[v1beta1.MilvusIO+"future-template-label"])
	assert.Equal(t, "parent", template.Annotations["parent-annotation"])
	assert.Equal(t, "current", template.Annotations["current-group-annotation"])
	assert.NotContains(t, template.Annotations, "removed-group-annotation")
	assert.NotContains(t, template.Annotations, "external-annotation")
	assert.Equal(t, "2026-08-13T10:00:00-07:00", template.Annotations[kubectlRestartedAtAnnotation])
	assert.Equal(t, ProxyName, template.Annotations[kubectlDefaultContainer])
	assert.Equal(t, "existing-checksum", template.Annotations[AnnotationCheckSum])
	assert.Equal(t, mc.GetActiveConfigMap(), template.Annotations[v1beta1.PodAnnotationUsingConfigMap])
	assert.Equal(t, "deployment-system", template.Annotations["deployment.kubernetes.io/future-template"])
	assert.Equal(t, "milvus-system", template.Annotations[v1beta1.MilvusIO+"future-template-state"])

	converged := deployment.DeepCopy()
	require.NoError(t, updateDeployment(deployment, newMilvusDeploymentUpdater(mc, scheme, workload)))
	assert.Equal(t, converged, deployment, "authoritative metadata reconciliation must be idempotent")
}

func TestUngroupedMetadataReconciliationRemainsAdditive(t *testing.T) {
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        Proxy.GetDeploymentName(mc.Name),
			Namespace:   mc.Namespace,
			Labels:      map[string]string{"external-label": "preserved"},
			Annotations: map[string]string{"external-annotation": "preserved"},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{MatchLabels: Proxy.GetSelectorLabels(mc.Name)},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
				Labels:      map[string]string{"external-label": "preserved"},
				Annotations: map[string]string{"external-annotation": "preserved"},
			}},
		},
	}

	require.NoError(t, updateDeployment(deployment, newMilvusDeploymentUpdater(mc, scheme, Proxy)))
	assert.Equal(t, "preserved", deployment.Labels["external-label"])
	assert.Equal(t, "preserved", deployment.Annotations["external-annotation"])
	assert.Equal(t, "preserved", deployment.Spec.Template.Labels["external-label"])
	assert.Equal(t, "preserved", deployment.Spec.Template.Annotations["external-annotation"])
}

func TestCreateTwoDeploymentSlotForDeploymentGroup(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mockUtil := NewMockK8sUtil(mockCtrl)
	one := int32(1)
	group := v1beta1.DeploymentGroup{
		Name:        "zone-a",
		Replicas:    &one,
		Labels:      map[string]string{"custom": "label"},
		Annotations: map[string]string{"custom": "annotation"},
	}
	workload := QueryNode
	workload.DeploymentGroup = &group
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	mockClient.EXPECT().Scheme().Return(scheme).AnyTimes()
	mockClient.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.Deployment{})).DoAndReturn(
		func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
			deployment := obj.(*appsv1.Deployment)
			assert.Equal(t, "mc-milvus-querynode-zone-a-0", deployment.Name)
			assert.Equal(t, "zone-a", deployment.Labels[v1beta1.DeploymentGroupLabel])
			assert.Equal(t, "label", deployment.Labels["custom"])
			assert.Equal(t, "annotation", deployment.Annotations["custom"])
			assert.Equal(t, "0", deployment.Labels[v1beta1.GetComponentGroupIdLabel(QueryNodeName)])
			assert.Equal(t, "zone-a", deployment.Spec.Template.Labels[v1beta1.DeploymentGroupLabel])
			assert.Equal(t, "label", deployment.Spec.Template.Labels["custom"])
			assert.NotContains(t, deployment.Spec.Selector.MatchLabels, "custom")
			return nil
		})

	err := NewDeployControllerBizUtil(workload, mockClient, mockUtil).CreateDeploy(context.Background(), mc, nil, 0)
	assert.NoError(t, err)
}

func TestRenderTwoDeploymentGroupPodMetadataAuthoritatively(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mockUtil := NewMockK8sUtil(mockCtrl)
	one := int32(1)
	group := v1beta1.DeploymentGroup{
		Name:        "zone-a",
		Replicas:    &one,
		Labels:      map[string]string{"current-group-label": "current"},
		Annotations: map[string]string{"current-group-annotation": "current"},
	}
	workload := QueryNode
	workload.DeploymentGroup = &group
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	mc.Spec.Com.QueryNode.PodLabels = map[string]string{"parent-label": "parent"}
	mc.Spec.Com.QueryNode.PodAnnotations = map[string]string{"parent-annotation": "parent"}
	current := &corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{
		Labels: MergeLabels(workload.GetSelectorLabels(mc.Name), map[string]string{
			"removed-group-label": "old",
			"external-label":      "external",
			"parent-label":        "old-group-value",
		}),
		Annotations: map[string]string{
			"removed-group-annotation":   "old",
			"external-annotation":        "external",
			"parent-annotation":          "old-group-value",
			kubectlRestartedAtAnnotation: "2026-08-13T10:00:00-07:00",
		},
	}}
	v1beta1.Labels().SetGroupID(QueryNodeName, current.Labels, 1)
	mockClient.EXPECT().Scheme().Return(scheme).Times(2)

	rendered := NewDeployControllerBizUtil(workload, mockClient, mockUtil).
		RenderPodTemplateWithoutGroupID(mc, current, workload, false)

	assert.Equal(t, "zone-a", rendered.Labels[v1beta1.DeploymentGroupLabel])
	assert.Equal(t, "1", rendered.Labels[v1beta1.GetComponentGroupIdLabel(QueryNodeName)], "the runtime rollout slot is preserved")
	assert.Equal(t, "parent", rendered.Labels["parent-label"])
	assert.Equal(t, "current", rendered.Labels["current-group-label"])
	assert.NotContains(t, rendered.Labels, "removed-group-label")
	assert.NotContains(t, rendered.Labels, "external-label")
	assert.Equal(t, "parent", rendered.Annotations["parent-annotation"])
	assert.Equal(t, "current", rendered.Annotations["current-group-annotation"])
	assert.NotContains(t, rendered.Annotations, "removed-group-annotation")
	assert.NotContains(t, rendered.Annotations, "external-annotation")
	assert.Equal(t, "2026-08-13T10:00:00-07:00", rendered.Annotations[kubectlRestartedAtAnnotation])
	renderedAgain := NewDeployControllerBizUtil(workload, mockClient, mockUtil).
		RenderPodTemplateWithoutGroupID(mc, rendered, workload, false)
	assert.Equal(t, rendered, renderedAgain, "rendering converged metadata must not request another rollout")
}

func TestReconcileTwoDeploymentGroupMetadata(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mockUtil := NewMockDeployControllerBizUtil(mockCtrl)
	one := int32(1)
	group := v1beta1.DeploymentGroup{
		Name:        "zone-a",
		Replicas:    &one,
		Labels:      map[string]string{"custom": "label", AppLabelComponent: "invalid"},
		Annotations: map[string]string{"custom": "annotation"},
	}
	workload := QueryNode
	workload.DeploymentGroup = &group
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns"}}
	mc.Default()
	deployments := []*appsv1.Deployment{
		{ObjectMeta: metav1.ObjectMeta{
			Labels: MergeLabels(workload.GetSelectorLabels(mc.Name), map[string]string{
				"removed-group-label": "old",
				"external-label":      "external",
			}),
			Annotations: map[string]string{
				"removed-group-annotation":   "old",
				"external-annotation":        "external",
				AnnotationMilvusGeneration:   "8",
				deploymentRevisionAnnotation: "3",
			},
		}},
		{ObjectMeta: metav1.ObjectMeta{
			Labels: MergeLabels(workload.GetSelectorLabels(mc.Name), map[string]string{
				"removed-group-label": "old",
				"external-label":      "external",
			}),
			Annotations: map[string]string{
				"removed-group-annotation":   "old",
				"external-annotation":        "external",
				AnnotationMilvusGeneration:   "8",
				deploymentRevisionAnnotation: "3",
			},
		}},
	}
	for slot := range deployments {
		v1beta1.Labels().SetGroupID(workload.Name, deployments[slot].Labels, slot)
	}
	mockClient.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	biz := NewDeployControllerBizImpl(workload, mockUtil, nil, mockClient)

	err := biz.reconcileDeploymentGroupMetadata(context.Background(), mc, deployments...)
	assert.ErrorIs(t, err, ErrRequeue)
	for slot, deployment := range deployments {
		assert.Equal(t, "label", deployment.Labels["custom"])
		assert.Equal(t, QueryNodeName, deployment.Labels[AppLabelComponent])
		assert.Equal(t, []string{"0", "1"}[slot], deployment.Labels[v1beta1.GetComponentGroupIdLabel(QueryNodeName)])
		assert.NotContains(t, deployment.Labels, "removed-group-label")
		assert.NotContains(t, deployment.Labels, "external-label")
		assert.Equal(t, "annotation", deployment.Annotations["custom"])
		assert.NotContains(t, deployment.Annotations, "removed-group-annotation")
		assert.NotContains(t, deployment.Annotations, "external-annotation")
		assert.Equal(t, "8", deployment.Annotations[AnnotationMilvusGeneration])
		assert.Equal(t, "3", deployment.Annotations[deploymentRevisionAnnotation])
	}
	assert.NoError(t, biz.reconcileDeploymentGroupMetadata(context.Background(), mc, deployments...))
}

func TestDeploymentGroupActiveSlotSelectionIsIsolated(t *testing.T) {
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.QueryNode.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}, {Name: "b", Replicas: &one}}
	workloads := GetComponentWorkloadsBySpec(mc.Spec)
	var queryWorkloads []MilvusComponent
	for _, workload := range workloads {
		if workload.Is(QueryNode) {
			queryWorkloads = append(queryWorkloads, workload)
		}
	}
	require.Len(t, queryWorkloads, 2)
	v1beta1.Labels().SetCurrentGroupID(&mc, queryWorkloads[0].GetStateKey(), 1)
	v1beta1.Labels().SetCurrentGroupID(&mc, queryWorkloads[1].GetStateKey(), 0)
	v1beta1.Labels().SetComponentRolling(&mc, queryWorkloads[0].GetStateKey(), true)

	var deployments []appsv1.Deployment
	for _, workload := range queryWorkloads {
		for slot := 0; slot < 2; slot++ {
			deployment := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
				Name:      formatComponentDeployName(mc, workload, slot),
				Namespace: mc.Namespace,
				Labels:    workload.GetSelectorLabels(mc.Name),
			}}
			v1beta1.Labels().SetGroupID(workload.Name, deployment.Labels, slot)
			require.NoError(t, controllerutil.SetControllerReference(&mc, &deployment, scheme))
			deployments = append(deployments, deployment)
		}
	}

	selected := makeWorkloadDeploymentMap(mc, deployments)
	assert.Equal(t, "mc-milvus-querynode-a-1", selected[queryWorkloads[0].GetStateKey()].Name)
	assert.Equal(t, "mc-milvus-querynode-b-0", selected[queryWorkloads[1].GetStateKey()].Name)
	assert.Equal(t, corev1.ConditionFalse, selected[queryWorkloads[0].GetStateKey()].Status.Conditions[0].Status)
	assert.Empty(t, selected[queryWorkloads[1].GetStateKey()].Status.Conditions)
}

func TestDeploymentGroupSavedRolloutNamesAreScopedAndBounded(t *testing.T) {
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc"}}
	one := int32(1)
	groupA, groupB := QueryNode, QueryNode
	groupA.DeploymentGroup = &v1beta1.DeploymentGroup{Name: "a", Replicas: &one}
	groupB.DeploymentGroup = &v1beta1.DeploymentGroup{Name: "b", Replicas: &one}
	assert.Equal(t, "querynode-mc-a-old-deploy", formatSaveOldDeployName(mc, groupA))
	assert.Equal(t, "querynode-mc-b-old-deploy", formatSaveOldDeployName(mc, groupB))
	assert.Equal(t, "querynode-mc-a-old-replicas", formatSaveOldReplicaSetListName(mc, groupA))
	assert.Equal(t, "querynode-mc-b-old-replicas", formatSaveOldReplicaSetListName(mc, groupB))
	assert.LessOrEqual(t, len(formatSaveOldDeployName(mc, groupA)), 253)
	assert.Equal(t, "querynode-mc-old-deploy", formatSaveOldDeployName(mc, QueryNode), "legacy saved-object names stay unchanged")
}

func TestAggregateComponentDeployStatuses(t *testing.T) {
	complete := func(replicas int32, image string) v1beta1.ComponentDeployStatus {
		return v1beta1.ComponentDeployStatus{
			Generation: 1,
			Image:      image,
			Status: appsv1.DeploymentStatus{
				ObservedGeneration: 1,
				Replicas:           replicas,
				ReadyReplicas:      replicas,
				AvailableReplicas:  replicas,
				UpdatedReplicas:    replicas,
				Conditions: []appsv1.DeploymentCondition{{
					Type: appsv1.DeploymentProgressing, Status: corev1.ConditionTrue, Reason: v1beta1.NewReplicaSetAvailableReason,
				}},
			},
		}
	}
	aggregate := aggregateComponentDeployStatuses([]v1beta1.ComponentDeployStatus{complete(1, "image"), complete(2, "image")})
	assert.Equal(t, int32(3), aggregate.Status.ReadyReplicas)
	assert.Equal(t, "image", aggregate.Image)
	assert.Equal(t, v1beta1.DeploymentComplete, aggregate.GetState())

	aggregate = aggregateComponentDeployStatuses([]v1beta1.ComponentDeployStatus{complete(1, "image"), {}})
	assert.Equal(t, v1beta1.DeploymentProgressing, aggregate.GetState())
	assert.Empty(t, aggregate.Image)
}

func TestComponentsDeployStatusUpdaterWithDeploymentGroups(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one, two := int32(1), int32(2)
	mc.Spec.Com.Proxy.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}, {Name: "b", Replicas: &two}}

	mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			deploymentList := list.(*appsv1.DeploymentList)
			for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
				if !workload.Is(Proxy) {
					continue
				}
				replicas := *workload.GetReplicas(mc.Spec)
				deployment := appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: workload.GetDeploymentName(mc.Name), Namespace: mc.Namespace, Labels: workload.GetSelectorLabels(mc.Name)},
					Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: ProxyName, Image: mc.Spec.Com.Image}}}}},
					Status:     readyDeployStatus,
				}
				deployment.Generation = 1
				deployment.Status.Replicas = replicas
				deployment.Status.ReadyReplicas = replicas
				deployment.Status.AvailableReplicas = replicas
				deployment.Status.UpdatedReplicas = replicas
				require.NoError(t, controllerutil.SetControllerReference(&mc, &deployment, scheme))
				deploymentList.Items = append(deploymentList.Items, deployment)
			}
			return nil
		})

	err := newComponentsDeployStatusUpdaterImpl(mockClient).Update(context.Background(), &mc)
	require.NoError(t, err)
	require.Len(t, mc.Status.DeploymentGroupsDeployStatus[ProxyName], 2)
	assert.Equal(t, int32(1), mc.Status.DeploymentGroupsDeployStatus[ProxyName]["a"].Status.ReadyReplicas)
	assert.Equal(t, int32(2), mc.Status.DeploymentGroupsDeployStatus[ProxyName]["b"].Status.ReadyReplicas)
	assert.Equal(t, int32(3), mc.Status.ComponentsDeployStatus[ProxyName].Status.ReadyReplicas)
	assert.Equal(t, v1beta1.DeploymentComplete, mc.Status.ComponentsDeployStatus[ProxyName].GetState())
}

func TestMilvusUpdatedConditionReportsDeploymentGroup(t *testing.T) {
	mc := v1beta1.Milvus{}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.Proxy.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}, {Name: "b", Replicas: &one}}
	mc.Status.ComponentsDeployStatus = map[string]v1beta1.ComponentDeployStatus{}
	mc.Status.DeploymentGroupsDeployStatus = map[string]map[string]v1beta1.ComponentDeployStatus{}
	complete := v1beta1.ComponentDeployStatus{Generation: 1, Image: mc.Spec.Com.Image, Status: readyDeployStatus}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		if workload.DeploymentGroup == nil {
			mc.Status.ComponentsDeployStatus[workload.Name] = complete
			continue
		}
		if mc.Status.DeploymentGroupsDeployStatus[workload.Name] == nil {
			mc.Status.DeploymentGroupsDeployStatus[workload.Name] = map[string]v1beta1.ComponentDeployStatus{}
		}
		mc.Status.DeploymentGroupsDeployStatus[workload.Name][workload.GetDeploymentGroupName()] = complete
	}
	assert.Equal(t, corev1.ConditionTrue, GetMilvusUpdatedCondition(&mc).Status)

	mc.Status.DeploymentGroupsDeployStatus[ProxyName]["b"] = v1beta1.ComponentDeployStatus{}
	condition := GetMilvusUpdatedCondition(&mc)
	assert.Equal(t, corev1.ConditionFalse, condition.Status)
	assert.Contains(t, condition.Message, "proxy/b")
}

func newOwnedDeploymentForCleanup(
	t *testing.T,
	mc *v1beta1.Milvus,
	workload MilvusComponent,
	name string,
	groupName string,
	replicas int32,
	status appsv1.DeploymentStatus,
) appsv1.Deployment {
	t.Helper()
	labels := workload.GetSelectorLabels(mc.Name)
	if groupName == "" {
		delete(labels, v1beta1.DeploymentGroupLabel)
	} else {
		labels[v1beta1.DeploymentGroupLabel] = groupName
	}
	deployment := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: mc.Namespace,
			Labels:    labels,
		},
		Spec:   appsv1.DeploymentSpec{Replicas: &replicas},
		Status: status,
	}
	require.NoError(t, controllerutil.SetControllerReference(mc, &deployment, scheme))
	return deployment
}

func TestCleanupStaleDeploymentGroupsAfterGroupsRemoved(t *testing.T) {
	tests := []struct {
		name        string
		groupStatus map[string]map[string]v1beta1.ComponentDeployStatus
	}{
		{name: "nil group status"},
		{name: "empty group status", groupStatus: map[string]map[string]v1beta1.ComponentDeployStatus{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			mockClient := NewMockK8sClient(mockCtrl)
			mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
			mc.Spec.Mode = v1beta1.MilvusModeCluster
			mc.Default()
			mc.Status.DeploymentGroupsDeployStatus = test.groupStatus

			desired := newOwnedDeploymentForCleanup(
				t, &mc, Proxy, Proxy.GetDeploymentName(mc.Name), "", 1, *readyDeployStatus.DeepCopy())
			staleOne := newOwnedDeploymentForCleanup(
				t, &mc, Proxy, "mc-milvus-proxy-zone-a", "zone-a", 1, appsv1.DeploymentStatus{})
			staleTwo := newOwnedDeploymentForCleanup(
				t, &mc, Proxy, "mc-milvus-proxy-zone-b", "zone-b", 1, appsv1.DeploymentStatus{})

			foreign := newOwnedDeploymentForCleanup(
				t, &mc, Proxy, "mc-milvus-proxy-foreign", "foreign", 1, appsv1.DeploymentStatus{})
			foreign.OwnerReferences = nil
			other := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: mc.Namespace, UID: "other-uid"}}
			require.NoError(t, controllerutil.SetControllerReference(&other, &foreign, scheme))

			deployments := []appsv1.Deployment{desired, staleOne, staleTwo, foreign}
			mockClient.EXPECT().List(
				gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any(), gomock.Any(),
			).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
				list.(*appsv1.DeploymentList).Items = deployments
				return nil
			})

			deleted := map[string]bool{}
			mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
					deleted[obj.GetName()] = true
					return nil
				},
			).Times(2)

			reconciler := &MilvusReconciler{Client: mockClient, Scheme: scheme}
			require.NoError(t, reconciler.cleanupStaleDeploymentGroups(context.Background(), mc))
			assert.Equal(t, map[string]bool{staleOne.Name: true, staleTwo.Name: true}, deleted)
		})
	}
}

func TestCleanupStaleDeploymentGroupsAfterGroupsRemovedWaitsForLegacyReadiness(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()

	desired := newOwnedDeploymentForCleanup(
		t, &mc, Proxy, Proxy.GetDeploymentName(mc.Name), "", 1, appsv1.DeploymentStatus{})
	stale := newOwnedDeploymentForCleanup(
		t, &mc, Proxy, "mc-milvus-proxy-zone-a", "zone-a", 1, appsv1.DeploymentStatus{})
	deployments := []appsv1.Deployment{desired, stale}
	mockClient.EXPECT().List(
		gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any(), gomock.Any(),
	).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*appsv1.DeploymentList).Items = deployments
		return nil
	})

	reconciler := &MilvusReconciler{Client: mockClient, Scheme: scheme}
	assert.NoError(t, reconciler.cleanupStaleDeploymentGroups(context.Background(), mc))
}

func TestCleanupStaleDeploymentGroupsAfterGroupsRemovedWithTwoDeployments(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	v1beta1.Labels().SetCurrentGroupID(&mc, QueryNode.GetStateKey(), 0)

	desiredCurrent := newOwnedDeploymentForCleanup(
		t, &mc, QueryNode, formatComponentDeployName(mc, QueryNode, 0), "", 1, *readyDeployStatus.DeepCopy())
	v1beta1.Labels().SetGroupID(QueryNodeName, desiredCurrent.Labels, 0)
	desiredLast := newOwnedDeploymentForCleanup(
		t, &mc, QueryNode, formatComponentDeployName(mc, QueryNode, 1), "", 0, appsv1.DeploymentStatus{})
	v1beta1.Labels().SetGroupID(QueryNodeName, desiredLast.Labels, 1)

	one := int32(1)
	groupedQueryNode := QueryNode
	groupedQueryNode.DeploymentGroup = &v1beta1.DeploymentGroup{Name: "zone-a", Replicas: &one}
	staleCurrent := newOwnedDeploymentForCleanup(
		t, &mc, groupedQueryNode, formatComponentDeployName(mc, groupedQueryNode, 0), "zone-a", 1, appsv1.DeploymentStatus{})
	v1beta1.Labels().SetGroupID(QueryNodeName, staleCurrent.Labels, 0)
	staleLast := newOwnedDeploymentForCleanup(
		t, &mc, groupedQueryNode, formatComponentDeployName(mc, groupedQueryNode, 1), "zone-a", 0, appsv1.DeploymentStatus{})
	v1beta1.Labels().SetGroupID(QueryNodeName, staleLast.Labels, 1)

	deployments := []appsv1.Deployment{desiredCurrent, desiredLast, staleCurrent, staleLast}
	mockClient.EXPECT().List(
		gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any(), gomock.Any(),
	).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*appsv1.DeploymentList).Items = deployments
		return nil
	})
	deleted := map[string]bool{}
	mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
			deleted[obj.GetName()] = true
			return nil
		},
	).Times(2)

	reconciler := &MilvusReconciler{Client: mockClient, Scheme: scheme}
	require.NoError(t, reconciler.cleanupStaleDeploymentGroups(context.Background(), mc))
	assert.Equal(t, map[string]bool{staleCurrent.Name: true, staleLast.Name: true}, deleted)
}

func TestCleanupStaleDeploymentGroupsWithoutStaleDeployments(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	desired := newOwnedDeploymentForCleanup(
		t, &mc, Proxy, Proxy.GetDeploymentName(mc.Name), "", 1, *readyDeployStatus.DeepCopy())
	mockClient.EXPECT().List(
		gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any(), gomock.Any(),
	).DoAndReturn(func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
		list.(*appsv1.DeploymentList).Items = []appsv1.Deployment{desired}
		return nil
	})

	reconciler := &MilvusReconciler{Client: mockClient, Scheme: scheme}
	assert.NoError(t, reconciler.cleanupStaleDeploymentGroups(context.Background(), mc))
}

func TestCleanupStaleDeploymentGroupsAfterDesiredReady(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.Proxy.Groups = []v1beta1.DeploymentGroup{{Name: "new", Replicas: &one}}

	deployments := []appsv1.Deployment{}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		deployment := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name:      workload.GetDeploymentName(mc.Name),
			Namespace: mc.Namespace,
			Labels:    workload.GetSelectorLabels(mc.Name),
		}, Status: readyDeployStatus}
		deployment.Spec.Replicas = int32Ptr(1)
		if componentUsesTwoDeployments(mc, workload) {
			v1beta1.Labels().SetCurrentGroupID(&mc, workload.GetStateKey(), 0)
			v1beta1.Labels().SetGroupID(workload.Name, deployment.Labels, 0)
			deployment.Name += "-0"
		}
		require.NoError(t, controllerutil.SetControllerReference(&mc, &deployment, scheme))
		deployments = append(deployments, deployment)
	}
	stale := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Name:      "mc-milvus-proxy-old",
		Namespace: mc.Namespace,
		Labels:    NewComponentAppLabels(mc.Name, ProxyName),
	}}
	stale.Labels[v1beta1.DeploymentGroupLabel] = "old"
	require.NoError(t, controllerutil.SetControllerReference(&mc, &stale, scheme))
	deployments = append(deployments, stale)

	mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			list.(*appsv1.DeploymentList).Items = deployments
			return nil
		})
	mockClient.EXPECT().Delete(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
			assert.Equal(t, stale.Name, obj.GetName())
			return nil
		})

	reconciler := &MilvusReconciler{Client: mockClient, Scheme: scheme}
	assert.NoError(t, reconciler.cleanupStaleDeploymentGroups(context.Background(), mc))
}

func TestCleanupStaleDeploymentGroupsWaitsForDesiredReadiness(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.DataNode.Groups = []v1beta1.DeploymentGroup{{Name: "new", Replicas: &one}}

	deployments := []appsv1.Deployment{}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		deployment := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name:      workload.GetDeploymentName(mc.Name),
			Namespace: mc.Namespace,
			Labels:    workload.GetSelectorLabels(mc.Name),
		}, Status: readyDeployStatus}
		deployment.Spec.Replicas = int32Ptr(1)
		if workload.Is(DataNode) {
			deployment.Status = appsv1.DeploymentStatus{}
		}
		if componentUsesTwoDeployments(mc, workload) {
			v1beta1.Labels().SetCurrentGroupID(&mc, workload.GetStateKey(), 0)
			v1beta1.Labels().SetGroupID(workload.Name, deployment.Labels, 0)
			deployment.Name += "-0"
		}
		require.NoError(t, controllerutil.SetControllerReference(&mc, &deployment, scheme))
		deployments = append(deployments, deployment)
	}
	stale := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "mc-milvus-datanode-old", Namespace: mc.Namespace, Labels: NewComponentAppLabels(mc.Name, DataNodeName)}}
	stale.Labels[v1beta1.DeploymentGroupLabel] = "old"
	require.NoError(t, controllerutil.SetControllerReference(&mc, &stale, scheme))
	deployments = append(deployments, stale)

	mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			list.(*appsv1.DeploymentList).Items = deployments
			return nil
		})

	reconciler := &MilvusReconciler{Client: mockClient, Scheme: scheme}
	assert.NoError(t, reconciler.cleanupStaleDeploymentGroups(context.Background(), mc))
}

func TestComponentConditionRequiresEveryDeploymentGroup(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	mockClient := NewMockK8sClient(mockCtrl)
	mc := v1beta1.Milvus{ObjectMeta: metav1.ObjectMeta{Name: "mc", Namespace: "ns", UID: "uid"}}
	mc.Spec.Mode = v1beta1.MilvusModeCluster
	mc.Default()
	one := int32(1)
	mc.Spec.Com.Proxy.Groups = []v1beta1.DeploymentGroup{{Name: "a", Replicas: &one}, {Name: "b", Replicas: &one}}
	deployments := []appsv1.Deployment{}
	for _, workload := range GetComponentWorkloadsBySpec(mc.Spec) {
		deployment := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
			Name:      workload.GetDeploymentName(mc.Name),
			Namespace: mc.Namespace,
			Labels:    workload.GetSelectorLabels(mc.Name),
		}, Status: *readyDeployStatus.DeepCopy()}
		require.NoError(t, controllerutil.SetControllerReference(&mc, &deployment, scheme))
		if workload.Is(Proxy) && workload.GetDeploymentGroupName() == "b" {
			deployment.Status.Conditions[1].Status = corev1.ConditionFalse
		}
		deployments = append(deployments, deployment)
	}
	mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			list.(*appsv1.DeploymentList).Items = deployments
			return nil
		})
	oldGetErrorDetail := getComponentErrorDetail
	getComponentErrorDetail = func(_ context.Context, _ client.Client, component string, _ *appsv1.Deployment) (*ComponentErrorDetail, error) {
		return &ComponentErrorDetail{ComponentName: component}, nil
	}
	defer func() { getComponentErrorDetail = oldGetErrorDetail }()

	condition, err := GetComponentConditionGetter().GetMilvusInstanceCondition(context.Background(), mockClient, mc)
	require.NoError(t, err)
	assert.Equal(t, corev1.ConditionFalse, condition.Status)
	assert.Contains(t, condition.Message, "proxy/b")

	for i := range deployments {
		deployments[i].Status = *readyDeployStatus.DeepCopy()
	}
	mockClient.EXPECT().List(gomock.Any(), gomock.AssignableToTypeOf(&appsv1.DeploymentList{}), gomock.Any()).DoAndReturn(
		func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			list.(*appsv1.DeploymentList).Items = deployments
			return nil
		})

	condition, err = GetComponentConditionGetter().GetMilvusInstanceCondition(context.Background(), mockClient, mc)
	require.NoError(t, err)
	assert.Equal(t, v1beta1.MilvusReady, condition.Type)
	assert.Equal(t, corev1.ConditionTrue, condition.Status, condition.Message)
	assert.Equal(t, v1beta1.ReasonMilvusHealthy, condition.Reason, condition.Message)
}
