package controllers

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
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
			return errors.Wrapf(err, "step[no.%d][%s]", i, step.Name)
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
	return errors.Wrap(err, "marking changing deploy mode")
}

func (c *DeployModeChangerImpl) SaveDeleteOldDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	oldDeploy, err := c.util.GetOldDeploy(ctx, mc, c.component)
	if err == nil {
		err := c.util.SaveObject(ctx, mc, formatSaveOldDeployName(mc, c.component), oldDeploy)
		if err != nil {
			return errors.Wrap(err, "save old deploy")
		}
		err = c.util.OrphanDelete(ctx, oldDeploy)
		if err != nil {
			return errors.Wrap(err, "orphan delete old deploy")
		}
	}
	if err != nil && !kerrors.IsNotFound(err) {
		return errors.Wrapf(err, "get old deployments")
	}
	return nil
}

func (c *DeployModeChangerImpl) SaveDeleteOldReplicaSet(ctx context.Context, mc v1beta1.Milvus) error {
	replicasetList, err := c.util.ListOldReplicaSets(ctx, mc, c.component)
	if err != nil {
		return errors.Wrap(err, "list old replica sets")
	}
	err = c.util.SaveObject(ctx, mc, formatSaveOldReplicaSetListName(mc, c.component), &replicasetList)
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
	pods, err := c.util.ListOldPods(ctx, mc, c.component)
	if err != nil {
		return errors.Wrap(err, "list old pods")
	}
	for _, pod := range pods {
		v1beta1.Labels().SetGroupID(c.component.Name, pod.Labels, 0)
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
		Name:      formatSaveOldReplicaSetListName(mc, c.component),
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
		labelHelper.SetGroupID(c.component.Name, rs.Labels, 0)
		labelHelper.SetGroupID(c.component.Name, rs.Spec.Selector.MatchLabels, 0)
		labelHelper.SetGroupID(c.component.Name, rs.Spec.Template.Labels, 0)
		rs.UID = ""
		rs.ResourceVersion = ""
		splitedName := strings.Split(rs.Name, "-")
		if len(splitedName) < 2 {
			return errors.Errorf("invalid old replica set name: %s", rs.Name)
		}
		rsHash := splitedName[len(splitedName)-1]
		deployName := strings.Join(splitedName[:len(splitedName)-1], "-")
		rs.Name = fmt.Sprintf("%s-0-%s", deployName, rsHash)
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
		Name:      formatSaveOldDeployName(mc, c.component),
	}
	err := c.util.GetSavedObject(ctx, key, oldDeploy)
	if err != nil {
		return errors.Wrap(err, "get old deploy")
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
		return errors.Wrap(err, "recover old deploy")
	}
	return nil
}

func (c *DeployModeChangerImpl) MarkCurrentDeploy(ctx context.Context, mc v1beta1.Milvus) error {
	if v1beta1.Labels().GetCurrentGroupId(&mc, c.component.Name) == "0" {
		return nil
	}
	v1beta1.Labels().SetCurrentGroupID(&mc, c.component.Name, 0)
	err := c.util.UpdateAndRequeue(ctx, &mc)
	return errors.Wrap(err, "mark current deploy")
}

func formatSaveOldDeployName(mc v1beta1.Milvus, component MilvusComponent) string {
	return fmt.Sprintf("%s-%s-old-deploy", component.Name, mc.Name)
}

func formatSaveOldReplicaSetListName(mc v1beta1.Milvus, component MilvusComponent) string {
	return fmt.Sprintf("%s-%s-old-replicas", component.Name, mc.Name)
}

// ChangeToOneDeployMode switches QueryNode to single deployment mode
func (c *DeployModeChangerImpl) ChangeToOneDeployMode(ctx context.Context, mc v1beta1.Milvus) error {
	if c.component != QueryNode {
		return nil
	}
	oldDeploy, err := c.util.GetOldDeploy(ctx, mc, c.component)
	if err != nil && !kerrors.IsNotFound(err) {
		return errors.Wrap(err, "get old deployment for ChangeToOneDeployMode")
	}
	if oldDeploy != nil {
		oldDeploy.Spec.Replicas = int32Ptr(0)
		err = c.cli.Update(ctx, oldDeploy)
		if err != nil {
			return errors.Wrap(err, "scale down old deployment")
		}
		err = c.cli.Delete(ctx, oldDeploy)
		if err != nil && !kerrors.IsNotFound(err) {
			return errors.Wrap(err, "delete old deployment")
		}
	}
	currentDeployName := fmt.Sprintf("%s-%s-0", c.component.Name, mc.Name)
	currentDeploy := &appsv1.Deployment{}
	err = c.cli.Get(ctx, client.ObjectKey{Namespace: mc.Namespace, Name: currentDeployName}, currentDeploy)
	if kerrors.IsNotFound(err) {
		labels := map[string]string{
			"app":       mc.Name,
			"component": c.component.Name,
			fmt.Sprintf("%s-group-id", c.component.Name): "0",
		}
		currentDeploy = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: mc.Namespace,
				Name:      currentDeployName,
				Labels:    labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: int32Ptr(0),
				Selector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: labels,
					},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Name: c.component.Name, Image: "milvusdb/milvus:latest"},
						},
					},
				},
			},
		}
		ctrl.SetControllerReference(&mc, currentDeploy, c.cli.Scheme())
		err = c.util.CreateObject(ctx, currentDeploy)
		if err != nil {
			return errors.Wrap(err, "create current deployment with groupId=0")
		}
	} else if err != nil {
		return errors.Wrap(err, "check current deployment")
	}
	err = c.util.MarkMilvusComponentGroupId(ctx, mc, c.component, 0)
	if err != nil {
		return errors.Wrap(err, "mark group id to 0")
	}
	return nil
}
