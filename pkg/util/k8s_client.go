package util

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
)

//go:generate mockgen -source=./k8s_client.go -destination=./k8s_client_mock.go -package=util K8sClient

// K8sClient wrap functions by k8s clients
type K8sClient interface {
	Exist(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (bool, error)

	// Create creates resources from manifest
	Create(ctx context.Context, manifest []byte) error

	// Delete delete resources from manifest
	Delete(ctx context.Context, manifest []byte) error

	// ListCRDs list all CRDs
	ListCRDs(ctx context.Context) (*apiextensionsv1.CustomResourceDefinitionList, error)

	// GetCRDVersionsByNames returns map[crdName]crdVersion
	GetCRDVersionsByNames(ctx context.Context, crdNames []string) (map[string]string, error)

	// WaitDeploymentReady
	WaitDeploymentsReadyByNamespace(ctx context.Context, namespace string) error
}

type K8sClients struct {
	ClientSet     kubernetes.Interface
	ExtClientSet  clientset.Interface
	DynamicClient dynamic.Interface
}

func NewK8sClientsForConfig(config *rest.Config) (*K8sClients, error) {
	clientSet, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create client set: %w", err)
	}

	extClientSet, err := clientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create ext client set: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	return &K8sClients{
		ClientSet:     clientSet,
		ExtClientSet:  extClientSet,
		DynamicClient: dynamicClient,
	}, nil
}

type applyableObj struct {
	unstructured unstructured.Unstructured
	mapping      *meta.RESTMapping
}

// ListCRDs list all CRDs
func (clis *K8sClients) ListCRDs(ctx context.Context) (*apiextensionsv1.CustomResourceDefinitionList, error) {
	return clis.ExtClientSet.ApiextensionsV1().CustomResourceDefinitions().List(ctx, metav1.ListOptions{})
}

func (clis *K8sClients) GetCRDVersionsByNames(ctx context.Context, crdNames []string) (map[string]string, error) {
	crds, err := clis.ListCRDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list crds: %w", err)
	}
	return crdListToVersionMapByName(crdNames, crds), nil
}

const crdVersionLabel = "app.kubernetes.io/version"

func crdListToVersionMapByName(crdNameList []string, crdList *apiextensionsv1.CustomResourceDefinitionList) map[string]string {
	ret := make(map[string]string)
	for _, crdName := range crdNameList {
		for _, crd := range crdList.Items {
			if crd.Name == crdName {
				ret[crdName] = crd.Labels[crdVersionLabel]
			}
		}
	}
	return ret
}

func (clis K8sClients) Exist(ctx context.Context, gvr schema.GroupVersionResource, namespace, name string) (bool, error) {
	_, err := clis.DynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to get resource %s/%s: %w", gvr, name, err)
	}
	return true, nil
}

// Create creates resource from manifest
func (clis K8sClients) Create(ctx context.Context, manifest []byte) error {
	objs, err := clis.getObjectsFromManifest(manifest)
	if err != nil {
		return fmt.Errorf("failed to get objects from manifest: %w", err)
	}

	for _, obj := range objs {
		cli := clis.getCliByObject(obj)
		_, err = cli.Create(ctx, &obj.unstructured, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create resource %s/%s: %w", obj.unstructured.GetKind(), obj.unstructured.GetName(), err)
		}
	}
	return nil
}

// Delete delete resource from manifest
func (clis K8sClients) Delete(ctx context.Context, manifest []byte) error {
	objs, err := clis.getObjectsFromManifest(manifest)
	if err != nil {
		return fmt.Errorf("failed to get objects from manifest: %w", err)
	}

	for _, obj := range objs {
		cli := clis.getCliByObject(obj)
		err = cli.Delete(ctx, string(obj.unstructured.GetName()), metav1.DeleteOptions{})
		if err != nil {
			return fmt.Errorf("failed to create resource: %w", err)
		}
	}
	return nil
}

func (clis K8sClients) getCliByObject(obj applyableObj) dynamic.ResourceInterface {
	var cli dynamic.ResourceInterface
	if obj.mapping.Scope.Name() == meta.RESTScopeNameNamespace {
		ns := obj.unstructured.GetNamespace()
		if ns == "" {
			ns = "default"
		}
		cli = clis.DynamicClient.Resource(obj.mapping.Resource).Namespace(ns)
	} else {
		cli = clis.DynamicClient.Resource(obj.mapping.Resource)
	}
	return cli
}

var dummyBytes = []byte(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: milvus-operator-dummy
`)

func addDummyObject(input []byte) []byte {
	return append(dummyBytes, input...)
}

func (clis K8sClients) getObjectsFromManifest(originalManifest []byte) ([]applyableObj, error) {
	// Note: yaml.YAMLOrJSONDecoder can't decode yaml not start with '---'so we add dummy obj to make it start with '---'
	manifest := addDummyObject(originalManifest)
	decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(manifest))

	dc := clis.ClientSet.Discovery()

	restMapperRes, err := restmapper.GetAPIGroupResources(dc)
	if err != nil {
		return nil, fmt.Errorf("failed to get api group resources: %w", err)
	}

	restMapper := restmapper.NewDiscoveryRESTMapper(restMapperRes)
	objs := make([]applyableObj, 0)

	isDummy := true
	for {
		ext := runtime.RawExtension{}
		if err := decoder.Decode(&ext); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to decode yaml: %w", err)
		}
		// ref: see Note above
		if isDummy {
			isDummy = false
			continue
		}

		// runtime.Object
		obj, gvk, err := unstructured.UnstructuredJSONScheme.Decode(ext.Raw, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to decode yaml: %w", err)
		}

		mapping, err := restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return nil, fmt.Errorf("failed to get rest mapping: %w", err)
		}

		// runtime.Object转换为unstructed
		unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
		if err != nil {
			return nil, fmt.Errorf("failed to convert to unstructured: %w", err)
		}
		objs = append(objs, applyableObj{
			unstructured: unstructured.Unstructured{Object: unstructuredObj},
			mapping:      mapping,
		})
	}
	return objs, nil
}

func (clis K8sClients) WaitDeploymentsReadyByNamespace(ctx context.Context, namespace string) error {
	const checkInterval = time.Second * 5
	ticker := time.NewTicker(checkInterval)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("ctx canceled to wait deployments ready: %w", ctx.Err())
		case <-ticker.C:
		}
		deploys, err := clis.ClientSet.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list deployments: %w", err)
		}
		if len(deploys.Items) == 0 {
			return fmt.Errorf("no deployments found in namespace %s", namespace)
		}
		var ready = true
		for _, deploy := range deploys.Items {
			if deploy.Status.ReadyReplicas < 1 {
				ready = false
				break
			}
		}
		if ready {
			return nil
		}
	}
}
