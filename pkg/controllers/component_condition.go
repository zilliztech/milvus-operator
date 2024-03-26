package controllers

import (
	"context"
	"fmt"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/util/rest"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -package=controllers -source=component_condition.go -destination=component_condition_mock.go ComponentConditionGetter
type ComponentConditionGetter interface {
	GetMilvusInstanceCondition(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (v1beta1.MilvusCondition, error)
}

type ComponentConditionGetterImpl struct{}

func (c ComponentConditionGetterImpl) GetMilvusInstanceCondition(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (v1beta1.MilvusCondition, error) {
	if mc.Spec.IsStopping() {
		reason := v1beta1.ReasonMilvusStopping
		msg := MessageMilvusStopped
		stopped, err := CheckMilvusStopped(ctx, cli, mc)
		if err != nil {
			return v1beta1.MilvusCondition{}, err
		}
		if stopped {
			reason = v1beta1.ReasonMilvusStopped
			msg = MessageMilvusStopping
		}

		return v1beta1.MilvusCondition{
			Type:    v1beta1.MilvusReady,
			Status:  corev1.ConditionFalse,
			Reason:  reason,
			Message: msg,
		}, nil
	}

	if !IsDependencyReady(mc.Status.Conditions) {
		notReadyConditions := GetNotReadyDependencyConditions(mc.Status.Conditions)
		reason := v1beta1.ReasonDependencyNotReady
		var msg string
		for depType, notReadyCondition := range notReadyConditions {
			if notReadyCondition != nil {
				msg += fmt.Sprintf("dep[%s]: %s;", depType, notReadyCondition.Message)
			} else {
				msg = "condition not probed yet"
			}
		}
		ctrl.LoggerFrom(ctx).Info("milvus dependency unhealty", "reason", reason, "msg", msg)
	}

	deployList := &appsv1.DeploymentList{}
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(map[string]string{
		AppLabelInstance: mc.GetName(),
		AppLabelName:     "milvus",
	})
	if err := cli.List(ctx, deployList, opts); err != nil {
		return v1beta1.MilvusCondition{}, err
	}

	allComponents := GetComponentsBySpec(mc.Spec)
	var notReadyComponents []string
	var errDetail *ComponentErrorDetail
	var err error
	componentDeploy := makeComponentDeploymentMap(mc, deployList.Items)
	hasReadyReplica := false
	for _, component := range allComponents {
		deployment := componentDeploy[component.Name]
		if deployment != nil && DeploymentReady(deployment.Status) {
			if deployment.Status.ReadyReplicas > 0 {
				hasReadyReplica = true
			}
			continue
		}
		notReadyComponents = append(notReadyComponents, component.Name)
		if errDetail == nil {
			errDetail, err = getComponentErrorDetail(ctx, cli, component.Name, deployment)
			if err != nil {
				return v1beta1.MilvusCondition{}, errors.Wrap(err, "failed to get component err detail")
			}
		}
	}

	cond := v1beta1.MilvusCondition{
		Type: v1beta1.MilvusReady,
	}

	if len(notReadyComponents) == 0 {
		if !hasReadyReplica {
			return v1beta1.MilvusCondition{}, nil
		}
		cond.Status = corev1.ConditionTrue
		cond.Reason = v1beta1.ReasonMilvusHealthy
		cond.Message = MessageMilvusHealthy
	} else {
		cond.Status = corev1.ConditionFalse
		cond.Reason = v1beta1.ReasonMilvusComponentNotHealthy
		cond.Message = fmt.Sprintf("%s not ready, detail: %s", notReadyComponents, errDetail)
		ctrl.LoggerFrom(ctx).Info("milvus unhealty", "reason", cond.Reason, "msg", cond.Message)
	}

	return cond, nil
}

var getComponentErrorDetail = func(ctx context.Context, cli client.Client, component string, deploy *appsv1.Deployment) (*ComponentErrorDetail, error) {
	ret := &ComponentErrorDetail{ComponentName: component}
	if deploy == nil {
		return ret, nil
	}
	if deploy.Status.ObservedGeneration < deploy.Generation {
		ret.NotObserved = true
		return ret, nil
	}
	var err error
	ret.Deployment, err = GetDeploymentFalseCondition(*deploy)
	if err != nil {
		return ret, err
	}

	pods := &corev1.PodList{}
	opts := &client.ListOptions{
		Namespace: deploy.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(deploy.Spec.Selector.MatchLabels)
	if err := cli.List(ctx, pods, opts); err != nil {
		return nil, errors.Wrap(err, "list pods")
	}
	if len(pods.Items) == 0 {
		return ret, nil
	}
	for _, pod := range pods.Items {
		if !PodReady(pod) {
			podCondition, err := GetPodFalseCondition(pod)
			if err != nil {
				return nil, err
			}
			ret.PodName = pod.Name
			ret.Pod = podCondition
			ret.Container = getFirstNotReadyContainerStatus(pod.Status.ContainerStatuses)
			return ret, nil
		}
	}
	return ret, nil
}

func GetComponentConditionGetter() ComponentConditionGetter {
	return singletonComponentConditionGetter
}

var singletonComponentConditionGetter ComponentConditionGetter = ComponentConditionGetterImpl{}

var ListMilvusTerminatingPods = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (*corev1.PodList, error) {
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(NewAppLabels(mc.Name))
	return listTerminatingPodByOpts(ctx, cli, opts)
}

var CheckComponentHasTerminatingPod = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus, component MilvusComponent) (bool, error) {
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(NewComponentAppLabels(mc.Name, component.Name))
	list, err := listTerminatingPodByOpts(ctx, cli, opts)
	if err != nil {
		return false, err
	}
	return len(list.Items) > 0, nil
}

func listMilvusPods(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (*corev1.PodList, error) {
	opts := &client.ListOptions{
		Namespace: mc.Namespace,
	}
	opts.LabelSelector = labels.SelectorFromSet(NewAppLabels(mc.Name))
	return listPodByOpts(ctx, cli, opts)
}

func listPodByOpts(ctx context.Context, cli client.Client, opts *client.ListOptions) (*corev1.PodList, error) {
	podList := &corev1.PodList{}
	if err := cli.List(ctx, podList, opts); err != nil {
		return nil, err
	}
	return podList, nil
}

func filterTerminatingPod(podList *corev1.PodList) *corev1.PodList {
	ret := &corev1.PodList{
		Items: make([]corev1.Pod, 0),
	}
	for _, pod := range podList.Items {
		if pod.DeletionTimestamp != nil {
			ret.Items = append(ret.Items, pod)
		}
	}
	return ret
}

func listTerminatingPodByOpts(ctx context.Context, cli client.Client, opts *client.ListOptions) (*corev1.PodList, error) {
	podList, err := listPodByOpts(ctx, cli, opts)
	if err != nil {
		return nil, err
	}
	return filterTerminatingPod(podList), nil
}

var CheckMilvusStopped = func(ctx context.Context, cli client.Client, mc v1beta1.Milvus) (bool, error) {
	podList, err := listMilvusPods(ctx, cli, mc)
	if err != nil {
		return false, err
	}
	if len(podList.Items) > 0 {
		logger := ctrl.LoggerFrom(ctx)
		logger.Info("milvus has pods not stopped", "pods count", len(podList.Items))
		return false, ExecKillIfTerminating(ctx, podList)
	}
	return true, nil
}

func ExecKillIfTerminating(ctx context.Context, podList *corev1.PodList) error {
	// we use kubectl exec to kill milvus process, because tini ignore SIGKILL
	cli := rest.GetRestClient()
	var ret error
	for _, pod := range podList.Items {
		if pod.DeletionTimestamp == nil {
			continue
		}
		// kill milvus process
		logger := ctrl.LoggerFrom(ctx)
		containerName := pod.Labels[AppLabelComponent]
		logger.Info("kill milvus process", "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), "container", containerName)
		stdout, stderr, err := cli.Exec(ctx, pod.Namespace, pod.Name, containerName, []string{"bash", "-c", "pid=$(ps -C milvus -o pid=); kill -9 $pid"})
		if err != nil {
			logger.Error(err, "kill milvus process err", "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), "container", containerName)
			ret = err
		}
		logger.Info("kill milvus output", "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), "stdout", stdout, "stderr", stderr)
	}
	if ret != nil {
		return errors.Wrap(ret, "failed to kill some milvus pod")
	}
	return nil
}
