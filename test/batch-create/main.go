package main

import (
	"context"
	"flag"
	"fmt"
	"strconv"

	milvusio "github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var begin int = 0
var end int = 1000

func main() {
	flag.IntVar(&begin, "begin", begin, "begin index of milvus CR name to create")
	flag.IntVar(&end, "end", end, "end index of milvus CR name to create")
	flag.Parse()
	err := run()
	if err != nil {
		panic(err)
	}
}

// var ak, _ = os.LookupEnv("AK")
// var sk, _ = os.LookupEnv("SK")

var nodeSelector = map[string]string{
	"node-role/default": "true",
}

var replica0 = int32(0)

var minimumMilvus = milvusio.Milvus{
	ObjectMeta: ctrl.ObjectMeta{
		Namespace: "batch-test",
		Labels: map[string]string{
			"usage": "batch-test",
		},
	},
	Spec: milvusio.MilvusSpec{
		Mode: milvusio.MilvusModeStandalone,
		Conf: milvusio.Values{
			Data: map[string]interface{}{
				"log": map[string]interface{}{
					"level": "info",
				},
				"minio": map[string]interface{}{
					"port":       443,
					"bucketName": "gcp-zilliz-infra-test",
					"useSSL":     true,
					"useIAM":     true,
					// "accessKeyID":     ak,
					// "secretAccessKey": sk,
					"cloudProvider": "gcp",
				},
			},
		},
		Com: milvusio.MilvusComponents{
			ComponentSpec: milvusio.ComponentSpec{
				NodeSelector: nodeSelector,
			},
			Standalone: &milvusio.MilvusStandalone{
				ServiceComponent: milvusio.ServiceComponent{
					Component: milvusio.Component{
						Replicas: &replica0,
						ComponentSpec: milvusio.ComponentSpec{
							Resources: &corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									"cpu":    resource.MustParse("0.01"),
									"memory": resource.MustParse("100Mi"),
								},
							},
						},
					},
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
							"nodeSelector": nodeSelector,
							"resources": map[string]interface{}{
								"requests": map[string]interface{}{
									"cpu":    "0.58",
									"memory": "2Gi",
									// "cpu":    "0.01",
									// "memory": "30Mi",
								},
							},
							"replicaCount": 1,
							"readinessProbe": map[string]interface{}{
								"initialDelaySeconds": 5,
								"failureThreshold":    11,
							},
							"livenessProbe": map[string]interface{}{
								"initialDelaySeconds": 5,
								"failureThreshold":    11,
							},
						},
					},
				},
			},
			Storage: milvusio.MilvusStorage{
				External: true,
				Type:     milvusio.StorageTypeS3,
				Endpoint: "storage.googleapis.com",
				// InCluster: &milvusio.InClusterConfig{
				// 	DeletionPolicy: milvusio.DeletionPolicyDelete,
				// 	PVCDeletion:    true,
				// 	Values: milvusio.Values{
				// 		Data: map[string]interface{}{
				// 			"nodeSelector": nodeSelector,
				// 			"mode":         "standalone",
				// 			"resources": map[string]interface{}{
				// 				"requests": map[string]interface{}{
				// 					"cpu":    "0.005",
				// 					"memory": "70Mi",
				// 				},
				// 			},
				// 		},
				// 	},
				// },
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

	baseName := baseName()
	for i := begin; i < end; i++ {
		milvus := minimumMilvus.DeepCopy()
		milvus.Name = baseName + strconv.Itoa(i)
		milvus.Namespace = baseNamespace() + strconv.Itoa(i/100)
		if err := cli.Create(ctx, milvus); err != nil {
			return errors.Wrap(err, "create milvus error")
		}
		fmt.Println("created milvus", "name", milvus.Name)
	}
	return nil
}

func baseName() string {
	return "batch-"
}

func baseNamespace() string {
	return "batch-test-"
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
