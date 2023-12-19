package controllers

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// QueryNodeControllerBizUtil are the business logics of QueryNodeControllerBizImpl, abstracted for unit test
type QueryNodeControllerBizUtil interface {
	UpdateStatusRollingMode(ctx context.Context, mc v1beta1.Milvus, mode v1beta1.RollingMode) error
	RenderPodTemplate(mc v1beta1.Milvus, component MilvusComponent) (*corev1.PodTemplateSpec, error)
	MarkChangingDeployMode(ctx context.Context, mc v1beta1.Milvus) error
	IsChangingDeployMode(mc v1beta1.Milvus) bool
	OrphanDeleteDeploy(ctx context.Context, deploy *appsv1.Deployment) error
	GetOldQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus) (*appsv1.Deployment, error)
	GetRevisionedQueryNodeDeploys(ctx context.Context, mc v1beta1.Milvus) (currentDeployment, lastDeployment *appsv1.Deployment, err error)
	CreateQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus, podTemplate *corev1.PodTemplateSpec, groupId int) error

	ShouldRollback(ctx context.Context, lastDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
	LastRolloutNotFinished(ctx context.Context, currentDeployment, lastDeployment *appsv1.Deployment) bool
	Rollout(ctx context.Context, currentDeployment, lastDeployment *appsv1.Deployment) error
	IsNewRollout(ctx context.Context, currentDeployment *appsv1.Deployment, podTemplate *corev1.PodTemplateSpec) bool
}

type QueryNodeControllerBizUtilImpl struct {
	cli client.Client
}

func (c *QueryNodeControllerBizUtilImpl) IsChangingDeployMode(mc v1beta1.Milvus) bool {
	_, isChanging := mc.Annotations[MilvusIOAnnotationChangingQueryNodeMode]
	return isChanging
}

func (c *QueryNodeControllerBizUtilImpl) GetOldQueryNodeDeploy(ctx context.Context, mc v1beta1.Milvus) (*appsv1.Deployment, error) {
	deployList := appsv1.DeploymentList{}
	labels := NewComponentAppLabels(mc.Name, QueryNode.Name)
	err := c.cli.List(ctx, &deployList, client.InNamespace(mc.Namespace), client.MatchingLabels(labels))
	if err != nil {
		return nil, errors.Wrap(err, "list querynode deployments")
	}
	deploys := deployList.Items
	if len(deploys) > 1 {
		return nil, errors.Errorf("unexpected: more than 1 querynode deployment found %d, admin please fix this, leave only 1 deployment", len(deploys))
	}
	if len(deploys) < 1 {
		return nil, kerrors.NewNotFound(schema.GroupResource{
			Group:    appsv1.SchemeGroupVersion.Group,
			Resource: "deployments",
		}, fmt.Sprintf("component=querynode,instance=%s", mc.Name))
	}
	return &deploys[0], nil
}
