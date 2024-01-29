package controllers

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -destination=./query_node_mode_changer_mock.go -package=controllers github.com/milvus-io/milvus-operator/pkg/controllers DeployModeChanger

// DeployModeChanger changes deploy mode
type DeployModeChanger interface {
	ChangeRollingModeToV2(ctx context.Context, mc v1beta1.Milvus) error
}

var _ DeployModeChanger = &DeployModeChangerImpl{}

type DeployModeChangerImpl struct {
	cli  client.Client
	util QueryNodeControllerBizUtil
}

func NewDeployModeChanger(cli client.Client, util QueryNodeControllerBizUtil) *DeployModeChangerImpl {
	return &DeployModeChangerImpl{
		cli:  cli,
		util: util,
	}
}

type step struct {
	Name string
	Func func(context.Context, v1beta1.Milvus) error
}

func newStep(name string, f func(context.Context, v1beta1.Milvus) error) step {
	return step{
		Name: name,
		Func: f,
	}
}

func (c *DeployModeChangerImpl) ChangeRollingModeToV2(ctx context.Context, mc v1beta1.Milvus) error {
	steps := []step{
		newStep("mark changing deploy mode", c.MarkChangingDeployModeTrue),
		newStep("save and delete old deploy", c.SaveDeleteOldDeploy),
		newStep("save and delete old replica set", c.SaveDeleteOldReplicaSet),
		newStep("update old pod labels", c.UpdateOldPodLabels),
		newStep("recover replica sets", c.RecoverReplicaSets),
		newStep("recover deploy", c.RecoverDeploy),
		newStep("unmark changing deploy mode", c.MarkChangingDeployModeFalse),
	}
	for i, step := range steps {
		err := step.Func(ctx, mc)
		if err != nil {
			return errors.Wrapf(err, "step[no.%d][%s]", i, step.Name)
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) MarkChangingDeployModeTrue(ctx context.Context, mc v1beta1.Milvus) error {
	return c.markChangingDeployMode(ctx, mc, true)
}

func (c *DeployModeChangerImpl) MarkChangingDeployModeFalse(ctx context.Context, mc v1beta1.Milvus) error {
	return c.markChangingDeployMode(ctx, mc, false)
}

func (c *DeployModeChangerImpl) markChangingDeployMode(ctx context.Context, mc v1beta1.Milvus, changing bool) error {
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
		err := c.util.SaveObject(ctx, mc, formatSaveOldDeployName(mc), oldDeploy)
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
	c.util.SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc), &replicasetList)
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
	key := client.ObjectKey{
		Namespace: mc.Namespace,
		Name:      formatSaveOldReplicaSetListName(mc),
	}
	err := c.util.GetSavedObject(ctx, key, replicasetList)
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
	key := client.ObjectKey{
		Namespace: mc.Namespace,
		Name:      formatSaveOldDeployName(mc),
	}
	err := c.util.GetSavedObject(ctx, key, oldDeploy)
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

func formatSaveOldDeployName(mc v1beta1.Milvus) string {
	return fmt.Sprintf("%s-old-deploy", mc.Name)
}

func formatSaveOldReplicaSetListName(mc v1beta1.Milvus) string {
	return fmt.Sprintf("%s-old-replicas", mc.Name)
}
