package rest

import (
	"testing"

	"github.com/stretchr/testify/assert"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestNewRestClientImpl(t *testing.T) {
	config := ctrl.GetConfigOrDie()
	restClient, err := newRestClientImpl(config)
	assert.NoError(t, err)
	assert.NotNil(t, restClient)
}
