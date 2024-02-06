package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ DeployModeChanger = &DeployModeChangerImpl{}

type DeployModeChangerImpl struct {
	cli                 client.Client
	util                QueryNodeControllerBizUtil
	changeModeToV2Steps []step
}

func NewDeployModeChanger(cli client.Client, util QueryNodeControllerBizUtil) *DeployModeChangerImpl {
	c := &DeployModeChangerImpl{
		cli:  cli,
		util: util,
	}
	c.changeModeToV2Steps = []step{
		newStep("save and delete old deploy", c.SaveDeleteOldDeploy),
		newStep("save and delete old replica set", c.SaveDeleteOldReplicaSet),
		newStep("update old pod labels", c.UpdateOldPodLabels),
		newStep("recover replica sets", c.RecoverReplicaSets),
		newStep("recover deploy", c.RecoverDeploy),
		newStep("update status mode to v2", c.UpdateStatusToV2),
	}
	return c
}

func (c *DeployModeChangerImpl) MarkDeployModeChanging(ctx context.Context, mc v1beta1.Milvus, changing bool) error {
	return c.markChangingDeployMode(ctx, mc, changing)
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
	logger := ctrl.LoggerFrom(ctx)
	for i, step := range c.changeModeToV2Steps {
		logger.Info("changeModeToV2Steps", "step no.", i, "name", step.Name)
		err := step.Func(ctx, mc)
		if err != nil {
			return errors.Wrapf(err, "step[no.%d][%s]", i, step.Name)
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) markChangingDeployMode(ctx context.Context, mc v1beta1.Milvus, changing bool) error {
	if v1beta1.Labels().IsChangeQueryNodeMode(mc) == changing {
		return nil
	}
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("marking changing deploy mode", "changing", changing)
	v1beta1.Labels().SetChangingQueryNodeMode(&mc, changing)
	err := c.util.UpdateAndRequeue(ctx, &mc)
	return errors.Wrap(err, "marking changing deploy mode")
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
	err = c.util.SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc), &replicasetList)
	if err != nil {
		return errors.Wrap(err, "save old replicaset list")
	}
	var ret error
	for _, rs := range replicasetList.Items {
		err = c.util.OrphanDelete(ctx, &rs)
		if err != nil {
			ret = errors.Wrapf(err, "deleting old replica set[%s]", rs.Name)
		}
	}
	return ret
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
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("recovering old replica sets", "count", len(replicasetList.Items))
	for _, rs := range replicasetList.Items {
		logger.Info("recovering old replica set", "old-name", rs.Name)
		labelHelper.SetQueryNodeGroupID(rs.Labels, 0)
		labelHelper.SetQueryNodeGroupID(rs.Spec.Selector.MatchLabels, 0)
		labelHelper.SetQueryNodeGroupID(rs.Spec.Template.Labels, 0)
		rs.UID = ""
		rs.ResourceVersion = ""
		splitedName := strings.Split(rs.Name, "-")
		if len(splitedName) < 2 {
			return errors.Errorf("invalid old replica set name: %s", rs.Name)
		}
		rsHash := splitedName[len(splitedName)-1]
		rs.Name = fmt.Sprintf("%s-%s", formatQnDeployName(mc, 0), rsHash)
		logger.Info("recovering old replica set", "new-name", rs.Name)
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
	oldDeploy.UID = ""
	oldDeploy.ResourceVersion = ""
	oldDeploy.Name = formatQnDeployName(mc, 0)
	err = c.util.CreateObject(ctx, oldDeploy)
	if err != nil {
		return errors.Wrap(err, "recover old deploy")
	}
	return nil
}

func (c *DeployModeChangerImpl) UpdateStatusToV2(ctx context.Context, mc v1beta1.Milvus) error {
	mc.Status.RollingMode = v1beta1.RollingModeV2
	err := c.cli.Status().Update(ctx, &mc)
	if err != nil {
		return errors.Wrap(err, "update status rolling mode")
	}
	return errors.Wrap(ErrRequeue, "update status rolling mode")
}

func formatSaveOldDeployName(mc v1beta1.Milvus) string {
	return fmt.Sprintf("%s-old-deploy", mc.Name)
}

func formatSaveOldReplicaSetListName(mc v1beta1.Milvus) string {
	return fmt.Sprintf("%s-old-replicas", mc.Name)
}
