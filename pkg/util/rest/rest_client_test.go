package rest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestNewRestClientImpl(t *testing.T) {
	config := ctrl.GetConfigOrDie()
	restClient, err := newRestClientImpl(config)
	assert.NoError(t, err)
	assert.NotNil(t, restClient)

	_, _, err = restClient.Exec(context.TODO(), "test", "test", "test", []string{"/bin/true"})
	// We don't expect this to work in tests.
	assert.Error(t, err)
}
