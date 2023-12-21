package controllers

import (
	"context"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type DeployModeChanger interface {
	MarkChangingDeployMode(ctx context.Context, mc v1beta1.Milvus, changing bool) error
	SaveDeleteOldDeploy(ctx context.Context, mc v1beta1.Milvus) error
	SaveDeleteOldReplicaSet(ctx context.Context, mc v1beta1.Milvus) error
	UpdateOldPodLabels(ctx context.Context, mc v1beta1.Milvus) error
	RecoverReplicaSets(ctx context.Context, mc v1beta1.Milvus) error
	RecoverDeploy(ctx context.Context, mc v1beta1.Milvus) error
}

var _ DeployModeChanger = &DeployModeChangerImpl{}

type DeployModeChangerImpl struct {
	cli  client.Client
	util QueryNodeControllerBizUtil
}

func (c *DeployModeChangerImpl) MarkChangingDeployMode(ctx context.Context, mc v1beta1.Milvus, changing bool) error {
	if v1beta1.Labels().IsChangeQueryNodeMode(mc) == changing {
		return nil
	}
	v1beta1.Labels().SetChangingQueryNodeMode(&mc, changing)
	err := c.cli.Update(ctx, &mc)
	if err != nil {
		return errors.Wrap(err, "update milvus spec")
	}
	// return error to abort this reconcile, and trigger next
	return errors.Wrap(ErrRequeue, "update deploy mode changing anotation")
}

func (c *DeployModeChangerImpl) SaveDeleteOldDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	oldDeploy, err := c.util.GetOldQueryNodeDeploy(ctx, mc)
	if err == nil {
		err := c.util.SaveObject(ctx, formatSaveOldDeployName(mc), oldDeploy)
		if err != nil {
			return errors.Wrap(err, "save old deploy")
		}
		err = c.util.OrphanDelete(ctx, oldDeploy)
		if err != nil {
			return errors.Wrap(err, "orphan delete old deploy")
		}
	}
	if err != nil && !kerrors.IsNotFound(err) {
		return errors.Wrap(err, "get querynode deployments")
	}
	return nil
}

func (c *DeployModeChangerImpl) SaveDeleteOldReplicaSet(ctx context.Context, mc v1beta1.Milvus) error {
	replicasetList, err := c.util.ListOldReplicaSets(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "list old replica sets")
	}
	c.util.SaveObject(ctx, formatSaveOldReplicaSetListName(mc), &replicasetList)
	for _, rs := range replicasetList.Items {
		err = c.util.OrphanDelete(ctx, &rs)
		if err != nil {
			return errors.Wrap(err, "delete old replica set")
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) UpdateOldPodLabels(ctx context.Context, mc v1beta1.Milvus) error {
	pods, err := c.util.ListOldPods(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "list old pods")
	}
	for _, pod := range pods {
		v1beta1.Labels().SetQueryNodeGroupID(pod.Labels, 0)
		err = c.cli.Update(ctx, &pod)
		if err != nil {
			return errors.Wrap(err, "update old pod labels")
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) RecoverReplicaSets(ctx context.Context, mc v1beta1.Milvus) error {
	replicasetList := &appsv1.ReplicaSetList{}
	err := c.util.GetSavedObject(ctx, formatSaveOldReplicaSetListName(mc), replicasetList)
	if err != nil {
		return errors.Wrap(err, "list old replica sets")
	}
	labelHelper := v1beta1.Labels()
	for _, rs := range replicasetList.Items {
		labelHelper.SetQueryNodeGroupID(rs.Labels, 0)
		labelHelper.SetQueryNodeGroupID(rs.Spec.Selector.MatchLabels, 0)
		labelHelper.SetQueryNodeGroupID(rs.Spec.Template.Labels, 0)
		err = c.util.CreateObject(ctx, &rs)
		if err != nil {
			return errors.Wrap(err, "recover old replica set")
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) RecoverDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	oldDeploy := &appsv1.Deployment{}
	err := c.util.GetSavedObject(ctx, formatSaveOldDeployName(mc), oldDeploy)
	if err != nil {
		return errors.Wrap(err, "get old deploy")
	}
	labelHelper := v1beta1.Labels()
	labelHelper.SetQueryNodeGroupID(oldDeploy.Labels, 0)
	labelHelper.SetQueryNodeGroupID(oldDeploy.Spec.Selector.MatchLabels, 0)
	labelHelper.SetQueryNodeGroupID(oldDeploy.Spec.Template.Labels, 0)
	err = c.util.CreateObject(ctx, oldDeploy)
	if err != nil {
		return errors.Wrap(err, "recover old deploy")
	}
	return nil
}
