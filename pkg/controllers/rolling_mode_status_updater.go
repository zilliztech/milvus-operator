package controllers

import (
	"context"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:generate mockgen -package=controllers -source=rolling_mode_status_updater.go -destination=./rolling_mode_status_updater_mock.go RollingModeStatusUpdater

type RollingModeStatusUpdater interface {
	Update(ctx context.Context, mc *v1beta1.Milvus) error
}

type RollingModeStatusUpdaterImpl struct {
	cli        client.Client
	bizFactory DeployControllerBizFactory
}

func NewRollingModeStatusUpdater(cli client.Client, bizFactory DeployControllerBizFactory) RollingModeStatusUpdater {
	return &RollingModeStatusUpdaterImpl{
		cli:        cli,
		bizFactory: bizFactory,
	}
}

func GetExpectedTwoDeployComponents(spec v1beta1.MilvusSpec) []MilvusComponent {
	switch spec.Com.RollingMode {
	case v1beta1.RollingModeV3:
		return GetComponentsBySpec(spec)
	default:
		if spec.Mode == v1beta1.MilvusModeStandalone {
			return []MilvusComponent{}
		}
		return []MilvusComponent{QueryNode}
	}
}

func (c *RollingModeStatusUpdaterImpl) checkUpdated(ctx context.Context, mc *v1beta1.Milvus) (updated bool, err error) {
	if mc.Status.RollingMode == mc.Spec.Com.RollingMode {
		return false, nil
	}
	for _, component := range GetExpectedTwoDeployComponents(mc.Spec) {
		biz := c.bizFactory.GetBiz(component)
		deployMode, err := biz.CheckDeployMode(ctx, *mc)
		if err != nil {
			return false, errors.Wrap(err, "check deploy mode")
		}
		if deployMode != v1beta1.TwoDeployMode {
			return false, nil
		}
	}
	mc.Status.RollingMode = mc.Spec.Com.RollingMode
	return true, nil
}

func (c *RollingModeStatusUpdaterImpl) Update(ctx context.Context, mc *v1beta1.Milvus) error {
	updated, err := c.checkUpdated(ctx, mc)
	if err != nil {
		return errors.Wrap(err, "check updated")
	}
	if !updated {
		return nil
	}
	err = c.cli.Status().Update(ctx, mc)
	return errors.Wrap(err, "update milvus status")
}
