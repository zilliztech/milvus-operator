package controllers

import "sigs.k8s.io/controller-runtime/pkg/client"

//go:generate mockgen -package=controllers -source=deploy_ctrl_factory.go -destination=./deploy_ctrl_factory_mock.go DeployControllerBizFactory

// DeployControllerBizFactory is the factory of DeployControllerBiz
type DeployControllerBizFactory interface {
	GetBiz(component MilvusComponent) DeployControllerBiz
}

var _ DeployControllerBizFactory = &DeployControllerBizFactoryImpl{}

// DeployControllerBizFactoryImpl is the implementation of DeployControllerBizFactory
type DeployControllerBizFactoryImpl struct {
	modeChanger  DeployModeChanger
	statusSyncer MilvusStatusSyncerInterface
	util         DeployControllerBizUtil
	cli          client.Client
}

// NewDeployControllerBizFactory creates a new DeployControllerBizFactory
func NewDeployControllerBizFactory(modeChanger DeployModeChanger, statusSyncer MilvusStatusSyncerInterface, util DeployControllerBizUtil, cli client.Client) *DeployControllerBizFactoryImpl {
	return &DeployControllerBizFactoryImpl{
		modeChanger:  modeChanger,
		statusSyncer: statusSyncer,
		util:         util,
		cli:          cli,
	}
}

// GetBiz get DeployControllerBiz for the given component
func (f *DeployControllerBizFactoryImpl) GetBiz(component MilvusComponent) DeployControllerBiz {
	return NewDeployControllerBizImpl(component, f.statusSyncer, f.util, f.modeChanger, f.cli)
}
