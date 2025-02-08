package main

import (
	"context"
	"fmt"

	milvusio "github.com/milvus-io/milvus-operator/apis/milvus.io/v1beta1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
		return fmt.Errorf("getClient error: %w", err)
	}

	ctx := context.Background()

	if err := cli.DeleteAllOf(ctx, &minimumMilvus, &client.DeleteAllOfOptions{ListOptions: client.ListOptions{
		LabelSelector: labels.Set{
			"usage": "batch-test",
		}.AsSelector(),
	}}); err != nil {
		return fmt.Errorf("delete milvus error: %w", err)
	}
	return nil
}

func baseName() string {
	return "batch-"
}

func getClient() (client.Client, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}
	scheme, err := milvusio.SchemeBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build scheme: %w", err)
	}
	cli, err := client.New(conf, client.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	return cli, nil
}
