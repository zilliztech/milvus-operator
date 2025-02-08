package controllers

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1alpha1"
	"github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/milvus-io/milvus-operator/pkg/config"
	"github.com/milvus-io/milvus-operator/pkg/util"
)

const (
	ConditionCheckingUpgrationState = "CheckingUpgrationState"
	ConditionUpgraded               = "Upgraded"
	ConditionRollbacked             = "Rollbacked"
)

func isStoppedByAnnotation(ctx context.Context, milvus *v1beta1.Milvus) bool {
	_, stoppedAtAnnotationExists := milvus.GetAnnotations()[v1beta1.StoppedAtAnnotation]
	return stoppedAtAnnotationExists
}

func markStoppedAtAnnotation(ctx context.Context, cli client.Client, milvus *v1beta1.Milvus) error {
	milvus.SetStoppedAtAnnotation(time.Now())
	return cli.Update(ctx, milvus)
}

func isComponentsDeregistered(ctx context.Context, milvus *v1beta1.Milvus) bool {
	stoppedAtStr := milvus.GetAnnotations()[v1beta1.StoppedAtAnnotation]
	logger := ctrl.LoggerFrom(ctx)
	t, err := time.Parse(time.RFC3339, stoppedAtStr)
	if err != nil {
		logger.Error(err, "parse stoppedAt failed, assumes stopped long enough", "stoppedAt", stoppedAtStr)
		return true
	}
	// TODO: check directly by etcd key, for now we just use default session ttl
	return time.Since(t) > time.Minute
}

func (r *MilvusUpgradeReconciler) RunStateMachine(ctx context.Context, upgrade *v1beta1.MilvusUpgrade) error {
	nextState := r.DetermineCurrentState(ctx, upgrade)

	milvus := new(v1beta1.Milvus)
	conditionType := ConditionCheckingUpgrationState
	err := r.Get(ctx, upgrade.Spec.Milvus.Object(), milvus)
	if err != nil {
		condition := metav1.Condition{
			Type:    conditionType,
			Status:  metav1.ConditionFalse,
			Reason:  "GetMilvusFailed",
			Message: err.Error(),
		}
		meta.SetStatusCondition(&upgrade.Status.Conditions, condition)
		return err
	}
	meta.RemoveStatusCondition(&upgrade.Status.Conditions, conditionType)
	v1beta1.InitLabelAnnotation(milvus)

	defer func() {
		r.updateCondition(upgrade, err)
	}()

	for {
		upgrade.Status.State = nextState
		stateFunc, found := r.stateFuncMap[nextState]
		if !found {
			return fmt.Errorf("Unexpected upgrade state[%s]", nextState)
		}
		if stateFunc == nil {
			return nil
		}
		ctrl.LoggerFrom(ctx).Info("callStateFunc", "state", nextState)
		nextState, err = stateFunc(ctx, upgrade, milvus)
		if err != nil {
			return err
		}
		if nextState == "" {
			return nil
		}
	}
}

func (r *MilvusUpgradeReconciler) updateCondition(upgrade *v1beta1.MilvusUpgrade, err error) {
	currentState := upgrade.Status.State
	conditionType := ConditionUpgraded
	if upgrade.Status.IsRollbacking {
		conditionType = ConditionRollbacked
	}

	condition := metav1.Condition{
		Type:   conditionType,
		Reason: string(currentState),
	}

	switch currentState {
	case v1beta1.UpgradeStateSucceeded, v1beta1.UpgradeStateRollbackSucceeded:
		condition.Status = metav1.ConditionTrue
	default:
		condition.Status = metav1.ConditionFalse
	}

	if err != nil {
		condition.Message = err.Error()
	}
	meta.SetStatusCondition(&upgrade.Status.Conditions, condition)
}

var notRollbackStates = []v1beta1.MilvusUpgradeState{
	v1beta1.UpgradeStatePending,
	v1beta1.UpgradeStateOldVersionStopping,
	v1beta1.UpgradeStateBackupMeta,
	v1beta1.UpgradeStateUpdatingMeta,
	v1beta1.UpgradeStateNewVersionStarting,
	v1beta1.UpgradeStateSucceeded,
	v1beta1.UpgradeStateBakupMetaFailed,
	v1beta1.UpgradeStateUpdateMetaFailed,
}

func (r *MilvusUpgradeReconciler) DetermineCurrentState(ctx context.Context, upgrade *v1beta1.MilvusUpgrade) (nextState v1beta1.MilvusUpgradeState) {
	if upgrade.Status.IsRollbacking || upgrade.Spec.Operation == v1beta1.OperationRollback {
		if isStateIn(upgrade.Status.State, notRollbackStates) {
			upgrade.Status.IsRollbacking = true
			upgrade.Status.State = v1beta1.UpgradeStateRollbackNewVersionStopping
		}
		return upgrade.Status.State
	}
	// else
	if upgrade.Status.State == v1beta1.UpgradeStatePending {
		upgrade.Status.State = v1beta1.UpgradeStateOldVersionStopping
	}

	return upgrade.Status.State
}

func (r *MilvusUpgradeReconciler) HandleUpgradeFailed(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	if upgrade.Spec.RollbackIfFailed {
		upgrade.Status.IsRollbacking = true
		upgrade.Status.State = v1beta1.UpgradeStateRollbackNewVersionStopping
		return upgrade.Status.State, nil
	}
	return "", nil
}

func (r *MilvusUpgradeReconciler) RollbackOldVersionStarting(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	// restore image
	milvus.Spec.Com.Image = upgrade.Status.SourceImage
	err := startMilvus(ctx, r.Client, upgrade, milvus)
	if err != nil {
		return "", err
	}
	return v1beta1.UpgradeStateRollbackSucceeded, nil
}

func (r *MilvusUpgradeReconciler) RollbackNewVersionStopping(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	if !isMilvusStopping(ctx, r.Client, milvus) {
		err := stopMilvus(ctx, r.Client, upgrade, milvus)
		return "", fmt.Errorf("stop milvus: %w", err)
	}
	// else isStopping
	stopped, err := isMilvusStopped(ctx, r.Client, milvus)
	if err != nil {
		return "", fmt.Errorf("check milvus is stopped: %w", err)
	}

	if stopped {
		return v1beta1.UpgradeStateRollbackRestoringOldMeta, nil
	}
	return "", nil
}

func (r *MilvusUpgradeReconciler) OldVersionStopping(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	if !isMilvusStopping(ctx, r.Client, milvus) {
		recordOldInfo(ctx, r.Client, upgrade, milvus)
		err := stopMilvus(ctx, r.Client, upgrade, milvus)
		return "", fmt.Errorf("stop milvus: %w", err)
	}
	// else isStopping
	stopped, err := isMilvusStopped(ctx, r.Client, milvus)
	if err != nil {
		return "", fmt.Errorf("check milvus is stopped: %w", err)
	}

	if stopped {
		return v1beta1.UpgradeStateBackupMeta, nil
	}
	return "", nil
}

func (r *MilvusUpgradeReconciler) BackupMeta(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	var ret = new(v1beta1.MilvusUpgradeState)
	onNoPod := func() error {
		err := startBakupMetaPod(ctx, r.Client, upgrade, milvus)
		return fmt.Errorf("start backup-meta pod: %w", err)
	}
	onSuccess := func() error {
		upgrade.Status.MetaBackuped = true
		*ret = v1beta1.UpgradeStateUpdatingMeta
		return nil
	}
	onFailure := func() error {
		*ret = v1beta1.UpgradeStateBakupMetaFailed
		return nil
	}
	err := r.handlePod(ctx, upgrade, BackupMeta, onNoPod, onSuccess, onFailure)
	return *ret, err
}

func (r *MilvusUpgradeReconciler) UpdatingMeta(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	if !upgrade.Status.MetaStorageChanged {
		upgrade.Status.MetaStorageChanged = true
		err := startUpdateMetaPod(ctx, r.Client, upgrade, milvus)
		return "", fmt.Errorf("start update-meta pod: %w", err)
	}

	var ret = new(v1beta1.MilvusUpgradeState)
	onNoPod := func() error {
		*ret = v1beta1.UpgradeStateUpdateMetaFailed
		return errors.New("cannot find update-meta pod")
	}
	onSuccess := func() error {
		*ret = v1beta1.UpgradeStateNewVersionStarting
		return nil
	}
	onFailure := func() error {
		*ret = v1beta1.UpgradeStateUpdateMetaFailed
		return nil
	}
	err := r.handlePod(ctx, upgrade, UpdateMeta, onNoPod, onSuccess, onFailure)
	return *ret, err
}

func (r *MilvusUpgradeReconciler) RollbackRestoringOldMeta(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	if !upgrade.Status.MetaBackuped {
		return v1beta1.UpgradeStateRollbackOldVersionStarting, nil
	}
	var ret = new(v1beta1.MilvusUpgradeState)
	onNoPod := func() error {
		err := startRollbackMetaPod(ctx, r.Client, upgrade, milvus)
		return fmt.Errorf("start backup-meta pod: %w", err)
	}
	onSuccess := func() error {
		*ret = v1beta1.UpgradeStateRollbackOldVersionStarting
		return nil
	}
	onFailure := func() error {
		*ret = v1beta1.UpgradeStateRollbackFailed
		return nil
	}
	err := r.handlePod(ctx, upgrade, RollbackMeta, onNoPod, onSuccess, onFailure)
	return *ret, err
}

var doOnPodPhase = func(list *corev1.PodList, onNoPod, onSuccess, onFailure func() error) error {
	if len(list.Items) == 0 {
		return onNoPod()
	}
	switch list.Items[0].Status.Phase {
	case corev1.PodSucceeded:
		return onSuccess()
	case corev1.PodRunning, corev1.PodPending:
		// do nothing
		return nil
	default:
		// case corev1.PodFailed, corev1.PodUnknown:
	}
	return onFailure()
}

func (r *MilvusUpgradeReconciler) HandleBakupMetaFailed(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	if upgrade.Status.RetriedTimes+1 > upgrade.Spec.MaxRetry {
		return r.HandleUpgradeFailed(ctx, upgrade, milvus)
	}

	err := r.DeleteAllOf(ctx, &corev1.Pod{}, client.MatchingLabels{
		LabelUpgrade:  upgrade.Name,
		LabelTaskKind: BackupMeta,
	},
		client.InNamespace(upgrade.Namespace))
	if err != nil {
		return "", fmt.Errorf("delete backup-meta pod: %w", err)
	}
	upgrade.Status.State = v1beta1.UpgradeStateBackupMeta
	upgrade.Status.RetriedTimes++
	return "", nil
}

func (r *MilvusUpgradeReconciler) handlePod(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, kind string, onNoPod, onSuccess, onFailure func() error) error {
	podList := new(corev1.PodList)
	err := r.Client.List(ctx, podList,
		client.MatchingLabels{
			LabelUpgrade:  upgrade.Name,
			LabelTaskKind: kind,
		},
		client.InNamespace(upgrade.Namespace))
	if err != nil {
		return fmt.Errorf("list pod for %s: %w", kind, err)
	}
	return doOnPodPhase(podList, onNoPod, onSuccess, onFailure)
}

func (r *MilvusUpgradeReconciler) StartingMilvusNewVersion(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (v1beta1.MilvusUpgradeState, error) {
	milvus.Spec.Com.Image = upgrade.Spec.TargetImage
	err := startMilvus(ctx, r.Client, upgrade, milvus)
	if err != nil {
		return "", err
	}
	return v1beta1.UpgradeStateSucceeded, nil
}

func startMilvus(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) error {
	// restore replicas
	if upgrade.Status.ReplicasBeforeUpgrade == nil {
		// no replicas recorded, assume zero
		upgrade.Status.ReplicasBeforeUpgrade = new(v1beta1.MilvusReplicas)
	}
	components := GetComponentsBySpec(milvus.Spec)
	for _, component := range components {
		replica := int32(component.GetMilvusReplicas(upgrade.Status.ReplicasBeforeUpgrade))
		component.SetReplicas(milvus.Spec, &replica)
	}
	milvus.RemoveStoppedAtAnnotation()
	err := cli.Update(ctx, milvus)
	if err != nil {
		return fmt.Errorf("restore replicas: %w", err)
	}
	err = annotateAlphaCR(ctx, cli, upgrade, milvus, v1beta1.AnnotationUpgraded)
	return fmt.Errorf("annotate alpha cr: %w", err)
}

const (
	LabelUpgrade  = v1beta1.MilvusIO + "upgrade"
	LabelTaskKind = v1beta1.MilvusIO + "task-kind"
	BackupMeta    = "backup-meta"
	UpdateMeta    = "update-meta"
	RollbackMeta  = "rollback-meta"
)

func createSubObjectOrIgnore(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, obj client.Object) error {
	err := ctrl.SetControllerReference(upgrade, obj, cli.Scheme())
	if err != nil {
		return fmt.Errorf("set controller reference: %w", err)
	}
	_, err = ctrl.CreateOrUpdate(ctx, cli, obj, func() error { return nil })
	return err
}

func startBakupMetaPod(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) error {
	// create pvc if not exists
	upgrade.Status.BackupPVC = upgrade.Spec.BackupPVC
	if upgrade.Spec.BackupPVC == "" {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      upgrade.Name,
				Namespace: upgrade.Namespace,
				Labels: map[string]string{
					LabelUpgrade: upgrade.Name,
				},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
		}
		upgrade.Status.BackupPVC = pvc.Name
		err := ctrl.SetControllerReference(upgrade, pvc, cli.Scheme())
		if err != nil {
			return fmt.Errorf("set controller reference: %w", err)
		}
		_, err = ctrl.CreateOrUpdate(ctx, cli, pvc, func() error { return nil })
		if err != nil {
			return fmt.Errorf("create backup pvc: %w", err)
		}
	}
	taskConf := taskConfig{
		Command: "backup",
		Kind:    BackupMeta,
	}
	return startTaskPod(ctx, cli, upgrade, milvus, taskConf)
}

func startUpdateMetaPod(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) error {
	taskConf := taskConfig{
		Command: "run",
		Kind:    UpdateMeta,
	}
	return startTaskPod(ctx, cli, upgrade, milvus, taskConf)
}

func startRollbackMetaPod(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) error {
	taskConf := taskConfig{
		Command: "rollback",
		Kind:    RollbackMeta,
	}
	return startTaskPod(ctx, cli, upgrade, milvus, taskConf)
}

func recordOldInfo(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) {
	if upgrade.Status.ReplicasBeforeUpgrade != nil {
		return
	}
	upgrade.Status.ReplicasBeforeUpgrade = new(v1beta1.MilvusReplicas)
	// record replicas
	components := GetComponentsBySpec(milvus.Spec)
	for _, component := range components {
		replicas := component.GetReplicas(milvus.Spec)
		if replicas == nil {
			replicas = int32Ptr(1)
		}
		component.SetStatusReplicas(upgrade.Status.ReplicasBeforeUpgrade, int(*replicas))
	}

	// record image
	upgrade.Status.SourceImage = milvus.Spec.Com.Image
}

func annotateAlphaCR(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus, value string) error {
	var alphaKey client.ObjectKey
	alphaKey.Namespace = milvus.Namespace
	for _, owner := range milvus.OwnerReferences {
		if owner.Kind == "MilvusCluster" {
			alphaKey.Name = owner.Name
			alphaCR := v1alpha1.MilvusCluster{}
			err := cli.Get(ctx, alphaKey, &alphaCR)
			if err != nil {
				return err
			}
			if alphaCR.Annotations == nil {
				alphaCR.Annotations = make(map[string]string)
			}
			alphaCR.Annotations[v1beta1.UpgradeAnnotation] = value
			err = cli.Update(ctx, &alphaCR)
			return err
		}
	}
	return nil
}

func stopMilvus(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) error {
	err := annotateAlphaCR(ctx, cli, upgrade, milvus, v1beta1.AnnotationUpgrading)
	if err != nil {
		return fmt.Errorf("annotate alpha cr: %w", err)
	}
	components := GetComponentsBySpec(milvus.Spec)
	for _, component := range components {
		component.SetReplicas(milvus.Spec, int32Ptr(0))
	}
	return cli.Update(ctx, milvus)
}

func isMilvusStopping(ctx context.Context, cli client.Client, milvus *v1beta1.Milvus) bool {
	components := GetComponentsBySpec(milvus.Spec)
	for _, component := range components {
		replicas := component.GetReplicas(milvus.Spec)
		if *replicas > 0 {
			return false
		}
	}
	return true
}

func isMilvusStopped(ctx context.Context, cli client.Client, milvus *v1beta1.Milvus) (bool, error) {
	if isStoppedByAnnotation(ctx, milvus) {
		return isComponentsDeregistered(ctx, milvus), nil
	}

	deployments, err := listAllDeployments(ctx, cli, client.ObjectKeyFromObject(milvus))
	if err != nil {
		return false, err
	}
	for _, deployment := range deployments {
		if deployment.Status.ObservedGeneration < deployment.Generation {
			// status not updated
			return false, nil
		}
		if deployment.Spec.Replicas == nil {
			// default replica == 1
			return false, nil
		}
		if *deployment.Spec.Replicas > 0 || deployment.Status.Replicas > 0 {
			return false, nil
		}
	}

	return false, markStoppedAtAnnotation(ctx, cli, milvus)
}

func listAllDeployments(ctx context.Context, cli client.Client, milvus client.ObjectKey) ([]appsv1.Deployment, error) {
	list := &appsv1.DeploymentList{}
	opts := &client.ListOptions{
		Namespace: milvus.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			AppLabelInstance: milvus.Name,
		}),
	}
	err := cli.List(ctx, list, opts)
	if err != nil {
		return nil, fmt.Errorf("list milvus deployments: %w", err)
	}
	return list.Items, nil
}

type taskConfig struct {
	Command string
	Kind    string
}

func formatUpgradeObjName(upgradeName string, taskKind string) string {
	return upgradeName + "-" + taskKind
}

const migrationConfFilename = "migration.yaml"

func createConfigMap(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus, taskConf taskConfig) error {
	// create configmap if not exists
	type migrationConfig struct {
		Command       string
		SourceVersion string
		TargetVersion string
		Endpoints     []string
		RootPath      string
		MetaSubPath   string
		KvSubPath     string
	}
	conf := migrationConfig{
		Command:       taskConf.Command,
		SourceVersion: v1beta1.RemovePrefixV(upgrade.Spec.SourceVersion),
		TargetVersion: v1beta1.RemovePrefixV(upgrade.Spec.TargetVersion),
		Endpoints:     milvus.Spec.Dep.Etcd.Endpoints,
	}
	// merge value from milvus's conf
	if rootPath, exist := util.GetStringValue(milvus.Spec.Conf.Data, "etcd", "rootPath"); exist {
		conf.RootPath = rootPath
	} else {
		conf.RootPath = milvus.Name
	}
	if metaSubPath, exist := util.GetStringValue(milvus.Spec.Conf.Data, "etcd", "metaSubPath"); exist {
		conf.MetaSubPath = metaSubPath
	} else {
		conf.MetaSubPath = "meta"
	}
	if kvSubPath, exist := util.GetStringValue(milvus.Spec.Conf.Data, "etcd", "kvSubPath"); exist {
		conf.KvSubPath = kvSubPath
	} else {
		conf.KvSubPath = "kv"
	}
	confYaml, err := util.GetTemplatedValues(config.GetMigrationConfigTemplate(), conf)
	if err != nil {
		return err
	}
	configmap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      formatUpgradeObjName(upgrade.Name, taskConf.Kind),
			Namespace: upgrade.Namespace,
			Labels: map[string]string{
				LabelUpgrade:  upgrade.Name,
				LabelTaskKind: taskConf.Kind,
			},
		},
		Data: map[string]string{
			migrationConfFilename: string(confYaml),
		},
	}
	return createSubObjectOrIgnore(ctx, cli, upgrade, configmap)
}

// startTaskPod start a pod to run upgrade task, we use variable for the convenience of test
var startTaskPod = func(ctx context.Context, cli client.Client, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus, taskConf taskConfig) error {
	err := createConfigMap(ctx, cli, upgrade, milvus, taskConf)
	if err != nil {
		return err
	}
	// create pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      formatUpgradeObjName(upgrade.Name, taskConf.Kind),
			Namespace: upgrade.Namespace,
			Labels: map[string]string{
				LabelUpgrade:      upgrade.Name,
				LabelTaskKind:     taskConf.Kind,
				AppLabelManagedBy: ManagerName,
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{
				{
					Name:  taskConf.Kind,
					Image: upgrade.Spec.ToolImage,
					Command: []string{
						"/milvus/bin/meta-migration",
					},
					Args: []string{
						"-config", "/milvus/configs/migration.yaml",
					},
					VolumeMounts: []corev1.VolumeMount{
						{
							Name:      "backup",
							MountPath: "/milvus/data",
						},
						{
							Name:      "config",
							MountPath: "/milvus/configs/migration.yaml",
							SubPath:   "migration.yaml",
						},
					},
				},
			},

			Volumes: []corev1.Volume{
				{
					Name: "backup",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: upgrade.Status.BackupPVC,
						},
					},
				},
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: formatUpgradeObjName(upgrade.Name, taskConf.Kind),
							},
						},
					},
				},
			},
		},
	}
	return createSubObjectOrIgnore(ctx, cli, upgrade, pod)
}

func isStateIn(state v1beta1.MilvusUpgradeState, states []v1beta1.MilvusUpgradeState) bool {
	for _, s := range states {
		if s == state {
			return true
		}
	}
	return false
}

type MilvusUpgradeReconcilerCommonFunc func(ctx context.Context, upgrade *v1beta1.MilvusUpgrade, milvus *v1beta1.Milvus) (nextState v1beta1.MilvusUpgradeState, err error)
