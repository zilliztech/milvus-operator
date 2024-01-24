package rest

import (
	"bytes"
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
)

//go:generate mockgen -source=./rest_client.go -destination=./rest_client_mock.go -package=rest RestClient

type RestClient interface {
	// Exec exec command in pod
	Exec(ctx context.Context, namespace, pod, container string, cmd []string) (stdout string, stderr string, err error)
}

type RestClientImpl struct {
	restClient rest.Interface
	config     *rest.Config
	scheme     *runtime.Scheme
}

var singletonRestClient RestClient

func GetRestClient() RestClient {
	return singletonRestClient
}

// SetRestClient for unit test
func SetRestClient(r RestClient) {
	singletonRestClient = r
}

func init() {
	config := ctrl.GetConfigOrDie()
	restClient, err := newRestClientImpl(config)
	if err != nil {
		panic(err)
	}
	singletonRestClient = restClient
}

func newRestClientImpl(config *rest.Config) (*RestClientImpl, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, errors.Wrap(err, "failed to add corev1 to scheme")
	}
	config.NegotiatedSerializer = serializer.NewCodecFactory(scheme)
	config.GroupVersion = &corev1.SchemeGroupVersion
	config.APIPath = "api"
	restClient, err := rest.RESTClientFor(config)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create rest client")
	}

	return &RestClientImpl{
		restClient: restClient,
		config:     config,
		scheme:     scheme,
	}, nil
}

func (clis RestClientImpl) Exec(ctx context.Context, namespace, pod, container string, cmd []string) (stdout string, stderr string, err error) {
	req := clis.restClient.Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
		}, runtime.NewParameterCodec(clis.scheme))

	exec, err := remotecommand.NewSPDYExecutor(clis.config, "POST", req.URL())
	if err != nil {
		return "", "", errors.Wrap(err, "failed to create executor")
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	err = exec.Stream(remotecommand.StreamOptions{
		Stdout: &stdoutBuf,
		Stderr: &stderrBuf,
	})
	if err != nil {
		return "", "", errors.Wrap(err, "failed to exec command")
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}
