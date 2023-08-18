package main

import (
	"flag"
	"fmt"
	"log"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrlConfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/zilliztech/milvus-operator/pkg/config"
	"github.com/zilliztech/milvus-operator/pkg/provisioner"
)

func main() {
	flag.StringVar(&config.OperatorNamespace, "namespace", "milvus-operator", "The namespace of self")
	flag.StringVar(&config.OperatorName, "name", "milvus-operator", "The namespace of self")
	flag.BoolVar(&provisioner.DisableCertManagerInstall, provisioner.DisableCertManagerInstallFlag, provisioner.DisableCertManagerInstall, "Disable auto install cert-manager if not exist")
	flag.BoolVar(&provisioner.DisableCertManagerCheck, provisioner.DisableCertManagerCheckFlag, provisioner.DisableCertManagerCheck, "Disable auto check & install cert-manager")
	flag.Parse()
	certMangerProvisioner, err := provisioner.NewCertManager(ctrlConfig.GetConfigOrDie())
	if err != nil {
		log.Fatal("unable to create cert manager provisioner ", err)
	}
	if !provisioner.DisableCertManagerCheck {
		err = certMangerProvisioner.InstallIfNotExist()
		if err != nil {
			log.Fatal("unable to install cert manager ", err)
		}
	} else {
		fmt.Println("cert-manager check is skipped")
	}

	err = certMangerProvisioner.IssueCertIfNotExist()
	if err != nil {
		log.Fatal("unable to install certification", err)
	}
	// TODO: rollout milvus-operator to minimize pending time
}
