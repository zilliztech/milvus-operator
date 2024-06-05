package controllers

import "sigs.k8s.io/controller-runtime/pkg/client"

//go:generate mockgen -package=controllers -source=deploy_ctrl_factory.go -destination=./deploy_ctrl_factory_mock.go DeployControllerBizFactory,DeployModeChangerFactory

// DeployControllerBizFactory is the factory of DeployControllerBiz
type DeployControllerBizFactory interface {
	GetBiz(component MilvusComponent) DeployControllerBiz
}

var _ DeployControllerBizFactory = &DeployControllerBizFactoryImpl{}

// DeployControllerBizFactoryImpl is the implementation of DeployControllerBizFactory
type DeployControllerBizFactoryImpl struct {
	modeChangerFactory DeployModeChangerFactory
	statusSyncer       MilvusStatusSyncerInterface
	util               DeployControllerBizUtil
	cli                client.Client
}

// NewDeployControllerBizFactory creates a new DeployControllerBizFactory
func NewDeployControllerBizFactory(modeChangerFactory DeployModeChangerFactory, statusSyncer MilvusStatusSyncerInterface, util DeployControllerBizUtil, cli client.Client) *DeployControllerBizFactoryImpl {
	return &DeployControllerBizFactoryImpl{
		modeChangerFactory: modeChangerFactory,
		statusSyncer:       statusSyncer,
		util:               util,
		cli:                cli,
	}
}

// GetBiz get DeployControllerBiz for the given component
func (f *DeployControllerBizFactoryImpl) GetBiz(component MilvusComponent) DeployControllerBiz {
	return NewDeployControllerBizImpl(component, f.statusSyncer, f.util, f.modeChangerFactory.GetDeployModeChanger(component), f.cli)
}

type DeployModeChangerFactory interface {
	GetDeployModeChanger(component MilvusComponent) DeployModeChanger
}

var _ DeployModeChangerFactory = &DeployModeChangerFactoryImpl{}

type DeployModeChangerFactoryImpl struct {
	cli  client.Client
	util DeployControllerBizUtil
}

func NewDeployModeChangerFactory(cli client.Client, util DeployControllerBizUtil) *DeployModeChangerFactoryImpl {
	return &DeployModeChangerFactoryImpl{
		cli:  cli,
		util: util,
	}
}

func (f *DeployModeChangerFactoryImpl) GetDeployModeChanger(component MilvusComponent) DeployModeChanger {
	return NewDeployModeChanger(component, f.cli, f.util)
}
