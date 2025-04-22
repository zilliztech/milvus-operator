package util

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/clientcmd"
)

func mustGetK8sClient(t *testing.T) *K8sClients {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	assert.NoError(t, err)
	clis, err := NewK8sClientsForConfig(config)
	assert.NoError(t, err)
	assert.NotNil(t, clis)
	return clis
}

func Test_NewK8sClientsForConfigOK(t *testing.T) {
	mustGetK8sClient(t)
}

func TestK8sClients_Exist(t *testing.T) {
	clis := mustGetK8sClient(t)

	ctx := context.TODO()
	exists, err := clis.Exist(ctx, schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}, "default", "foo")
	assert.NoError(t, err)
	assert.False(t, exists)

	err = clis.Create(ctx, []byte(fooResource))
	assert.NoError(t, err)
	defer clis.Delete(ctx, []byte(fooResource))

	exists, err = clis.Exist(ctx, schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "configmaps",
	}, "default", "foo")
	assert.NoError(t, err)
	assert.True(t, exists)
}

func TestK8sClients_ListCRDs(t *testing.T) {
	clis := mustGetK8sClient(t)

	crds, err := clis.ListCRDs(context.Background())
	assert.NoError(t, err)
	assert.NotNil(t, crds)
}

const fooResource = `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: foo
data:
  key: value
`

func TestK8sClients_CreateDelete1(t *testing.T) {
	clis := mustGetK8sClient(t)

	ctx := context.TODO()
	// one resource
	err := clis.Create(ctx, []byte(fooResource))
	assert.NoError(t, err)
	err = clis.Delete(ctx, []byte(fooResource))
	assert.NoError(t, err)
}

const foo2Resource = `
#objs
---
# obj1
apiVersion: v1
kind: ConfigMap
metadata:
  name: foo
data:
  key: value

---
# obj2
apiVersion: v1
kind: ConfigMap
metadata:
  name: foo2
data:
  key: value
`

func TestK8sClients_CreateDelete2(t *testing.T) {
	clis := mustGetK8sClient(t)

	ctx := context.TODO()

	// multiple resource
	err := clis.Create(ctx, []byte(foo2Resource))
	assert.NoError(t, err)
	_, err = clis.ClientSet.CoreV1().ConfigMaps("default").Get(ctx, "foo", metav1.GetOptions{})
	assert.NoError(t, err)
	_, err = clis.ClientSet.CoreV1().ConfigMaps("default").Get(ctx, "foo2", metav1.GetOptions{})
	assert.NoError(t, err)
	err = clis.Delete(ctx, []byte(foo2Resource))
	assert.NoError(t, err)
}

func TestK8sClients_CreateDeleteFailed(t *testing.T) {
	clis := mustGetK8sClient(t)
	ctx := context.TODO()
	// bad manifest
	err := clis.Create(ctx, []byte(`bad`))
	assert.Error(t, err)
	// bad manifest
	err = clis.Delete(ctx, []byte(`bad`))
	assert.Error(t, err)

	// not exist
	err = clis.Delete(ctx, []byte(fooResource))
	assert.Error(t, err)
}

// "https://raw.githubusercontent.com/kubernetes/sample-controller/master/artifacts/examples/crd.yaml"
var fooCRD = []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.samplecontroller.k8s.io
  annotations:
    "api-approved.kubernetes.io": "unapproved, experimental-only; please get an approval from Kubernetes API reviewers if you're trying to develop a CRD in the *.k8s.io or *.kubernetes.io groups"
spec:
  group: samplecontroller.k8s.io
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        # schema used for validation
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                deploymentName:
                  type: string
                replicas:
                  type: integer
                  minimum: 1
                  maximum: 10
            status:
              type: object
              properties:
                availableReplicas:
                  type: integer
  names:
    kind: Foo
    plural: foos
  scope: Namespaced`)

func TestK8sClients_Exist_GetCRDVersionsByNames(t *testing.T) {
	clis := mustGetK8sClient(t)

	ctx := context.TODO()

	err := clis.Create(ctx, fooCRD)
	assert.NoError(t, err)
	defer clis.Delete(ctx, []byte(fooCRD))

	crdNames := []string{"foos.samplecontroller.k8s.io"}
	crdVersions, err := clis.GetCRDVersionsByNames(ctx, crdNames)
	assert.NoError(t, err)
	assert.NotNil(t, crdVersions)
}

var helloWorldDeployment = []byte(`---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pause
spec:
  selector:
    matchLabels:
      app: pause
  template:
    metadata:
      labels:
        app: pause
    spec:
      containers:
      - name: pause
        image: nginx:latest
        resources:
          limits:
            memory: "64Mi"
            cpu: "10m"
`)

func TestK8sClients_WaitDeploymentsReadyByNamespace(t *testing.T) {
	clis := mustGetK8sClient(t)

	ctx := context.TODO()
	err := clis.WaitDeploymentsReadyByNamespace(ctx, "empty-namespace")
	assert.Error(t, err)

	err = clis.Create(ctx, helloWorldDeployment)
	assert.NoError(t, err)
	defer clis.Delete(ctx, []byte(helloWorldDeployment))

	err = clis.WaitDeploymentsReadyByNamespace(ctx, "default")
	assert.NoError(t, err)

	// canceled
	ctx, cancel := context.WithTimeout(ctx, time.Millisecond)
	cancel()
	err = clis.WaitDeploymentsReadyByNamespace(ctx, "default")
	assert.Error(t, err)
}
