package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ DeployModeChanger = &DeployModeChangerImpl{}

type DeployModeChangerImpl struct {
	component           MilvusComponent
	cli                 client.Client
	util                K8sUtil
	changeModeToV2Steps []step
}

func NewDeployModeChanger(component MilvusComponent, cli client.Client, util K8sUtil) *DeployModeChangerImpl {
	c := &DeployModeChangerImpl{
		component: component,
		cli:       cli,
		util:      util,
	}
	c.changeModeToV2Steps = []step{
		newStep("save and delete old deploy", c.SaveDeleteOldDeploy),
		newStep("save and delete old replica set", c.SaveDeleteOldReplicaSet),
		newStep("update old pod labels", c.UpdateOldPodLabels),
		newStep("recover replica sets", c.RecoverReplicaSets),
		newStep("recover deploy", c.RecoverDeploy),
		newStep("mark current deploy", c.MarkCurrentDeploy),
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

func (c *DeployModeChangerImpl) ChangeToTwoDeployMode(ctx context.Context, mc v1beta1.Milvus) error {
	logger := ctrl.LoggerFrom(ctx)
	for i, step := range c.changeModeToV2Steps {
		logger.Info("changeModeToV2Steps", "step no.", i, "name", step.Name)
		err := step.Func(ctx, mc)
		if err != nil {
			return fmt.Errorf("step[no.%d][%s]: %w", i, step.Name, err)
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) markChangingDeployMode(ctx context.Context, mc v1beta1.Milvus, changing bool) error {
	if v1beta1.Labels().IsChangingMode(mc, c.component.Name) == changing {
		return nil
	}
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("marking changing deploy mode", "changing", changing)
	v1beta1.Labels().SetChangingMode(&mc, c.component.Name, changing)
	err := c.util.UpdateAndRequeue(ctx, &mc)
	return fmt.Errorf("marking changing deploy mode: %w", err)
}

func (c *DeployModeChangerImpl) SaveDeleteOldDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	oldDeploy, err := c.util.GetOldDeploy(ctx, mc, c.component)
	if err == nil {
		err := c.util.SaveObject(ctx, mc, formatSaveOldDeployName(mc, c.component), oldDeploy)
		if err != nil {
			return fmt.Errorf("save old deploy: %w", err)
		}
		err = c.util.OrphanDelete(ctx, oldDeploy)
		if err != nil {
			return fmt.Errorf("orphan delete old deploy: %w", err)
		}
	}
	if err != nil && !k8sErrors.IsNotFound(err) {
		return fmt.Errorf("get old deployments error: %w", err)
	}
	return nil
}

func (c *DeployModeChangerImpl) SaveDeleteOldReplicaSet(ctx context.Context, mc v1beta1.Milvus) error {
	replicasetList, err := c.util.ListOldReplicaSets(ctx, mc, c.component)
	if err != nil {
		return fmt.Errorf("list old replica sets: %w", err)
	}
	err = c.util.SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc, c.component), &replicasetList)
	if err != nil {
		return fmt.Errorf("save old replicaset list: %w", err)
	}
	var ret error
	for _, rs := range replicasetList.Items {
		err = c.util.OrphanDelete(ctx, &rs)
		if err != nil {
			ret = fmt.Errorf("deleting old replica set[%s]: %w", rs.Name, err)
		}
	}
	return ret
}

func (c *DeployModeChangerImpl) UpdateOldPodLabels(ctx context.Context, mc v1beta1.Milvus) error {
	pods, err := c.util.ListOldPods(ctx, mc, c.component)
	if err != nil {
		return fmt.Errorf("list old pods: %w", err)
	}
	for _, pod := range pods {
		v1beta1.Labels().SetGroupID(c.component.Name, pod.Labels, 0)
		err = c.cli.Update(ctx, &pod)
		if err != nil {
			return fmt.Errorf("update old pod labels: %w", err)
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) RecoverReplicaSets(ctx context.Context, mc v1beta1.Milvus) error {
	replicasetList := &appsv1.ReplicaSetList{}
	key := client.ObjectKey{
		Namespace: mc.Namespace,
		Name:      formatSaveOldReplicaSetListName(mc, c.component),
	}
	err := c.util.GetSavedObject(ctx, key, replicasetList)
	if err != nil {
		return fmt.Errorf("list old replica sets: %w", err)
	}
	labelHelper := v1beta1.Labels()
	logger := ctrl.LoggerFrom(ctx)
	logger.Info("recovering old replica sets", "count", len(replicasetList.Items))
	for _, rs := range replicasetList.Items {
		logger.Info("recovering old replica set", "old-name", rs.Name)
		labelHelper.SetGroupID(c.component.Name, rs.Labels, 0)
		labelHelper.SetGroupID(c.component.Name, rs.Spec.Selector.MatchLabels, 0)
		labelHelper.SetGroupID(c.component.Name, rs.Spec.Template.Labels, 0)
		rs.UID = ""
		rs.ResourceVersion = ""
		splitedName := strings.Split(rs.Name, "-")
		if len(splitedName) < 2 {
			return fmt.Errorf("invalid old replica set name: %s", rs.Name)
		}
		rsHash := splitedName[len(splitedName)-1]
		deployName := strings.Join(splitedName[:len(splitedName)-1], "-")
		rs.Name = fmt.Sprintf("%s-0-%s", deployName, rsHash)
		logger.Info("recovering old replica set", "new-name", rs.Name)
		err = c.util.CreateObject(ctx, &rs)
		if err != nil {
			return fmt.Errorf("recover old replica set: %w", err)
		}
	}
	return nil
}

func (c *DeployModeChangerImpl) RecoverDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	oldDeploy := &appsv1.Deployment{}
	key := client.ObjectKey{
		Namespace: mc.Namespace,
		Name:      formatSaveOldDeployName(mc, c.component),
	}
	err := c.util.GetSavedObject(ctx, key, oldDeploy)
	if err != nil {
		return fmt.Errorf("get old deploy: %w", err)
	}
	labelHelper := v1beta1.Labels()
	labelHelper.SetGroupID(c.component.Name, oldDeploy.Labels, 0)
	labelHelper.SetGroupID(c.component.Name, oldDeploy.Spec.Selector.MatchLabels, 0)
	labelHelper.SetGroupID(c.component.Name, oldDeploy.Spec.Template.Labels, 0)
	oldDeploy.UID = ""
	oldDeploy.ResourceVersion = ""
	oldDeploy.Name = fmt.Sprintf("%s-0", oldDeploy.Name)
	err = c.util.CreateObject(ctx, oldDeploy)
	if err != nil {
		return fmt.Errorf("recover old deploy: %w", err)
	}
	return nil
}

func (c *DeployModeChangerImpl) MarkCurrentDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	if v1beta1.Labels().GetCurrentGroupId(&mc, c.component.Name) == "0" {
		return nil
	}
	v1beta1.Labels().SetCurrentGroupID(&mc, c.component.Name, 0)
	err := c.util.UpdateAndRequeue(ctx, &mc)
	return fmt.Errorf("mark current deploy: %w", err)
}

func formatSaveOldDeployName(mc v1beta1.Milvus, component MilvusComponent) string {
	return fmt.Sprintf("%s-%s-old-deploy", component.Name, mc.Name)
}

func formatSaveOldReplicaSetListName(mc v1beta1.Milvus, component MilvusComponent) string {
	return fmt.Sprintf("%s-%s-old-replicas", component.Name, mc.Name)
}
