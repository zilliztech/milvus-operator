package main

import (
	"context"

	"github.com/pkg/errors"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	milvusio "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
)

func main() {
	err := run()
	if err != nil {
		panic(err)
	}
}

var minimumMilvus = milvusio.Milvus{
	ObjectMeta: ctrl.ObjectMeta{},
	Spec: milvusio.MilvusSpec{
		Mode: milvusio.MilvusModeStandalone,
		Conf: milvusio.Values{
			Data: map[string]interface{}{
				"log": map[string]interface{}{
					"level": "info",
				},
			},
		},
		Dep: milvusio.MilvusDependencies{
			Etcd: milvusio.MilvusEtcd{
				InCluster: &milvusio.InClusterConfig{
					DeletionPolicy: milvusio.DeletionPolicyDelete,
					PVCDeletion:    true,
					Values: milvusio.Values{
						Data: map[string]interface{}{
							"replicaCount": 1,
						},
					},
				},
			},
			Storage: milvusio.MilvusStorage{
				InCluster: &milvusio.InClusterConfig{
					DeletionPolicy: milvusio.DeletionPolicyDelete,
					PVCDeletion:    true,
					Values: milvusio.Values{
						Data: map[string]interface{}{
							"mode": "standalone",
							"resources": map[string]interface{}{
								"requests": map[string]interface{}{
									"memory": "100Mi",
								},
							},
						},
					},
				},
			},
		},
	},
}

func run() error {
	cli, err := getClient()
	if err != nil {
		return errors.Wrap(err, "getClient error")
	}

	ctx := context.Background()

	if err := cli.DeleteAllOf(ctx, &minimumMilvus, &client.DeleteAllOfOptions{ListOptions: client.ListOptions{
		LabelSelector: labels.Set{
			"usage": "batch-test",
		}.AsSelector(),
	}}); err != nil {
		return errors.Wrap(err, "delete milvus error")
	}
	return nil
}

func getClient() (client.Client, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get config")
	}
	scheme, err := milvusio.SchemeBuilder.Build()
	if err != nil {
		return nil, errors.Wrap(err, "failed to build scheme")
	}
	cli, err := client.New(conf, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to create client")
	}
	return cli, nil
}
