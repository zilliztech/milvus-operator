package v1beta1

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type adapterResource struct {
	Milvus
	defaulted bool
	updated   runtime.Object
	err       error
}

func (r *adapterResource) Default() { r.defaulted = true }
func (r *adapterResource) ValidateCreate() (admission.Warnings, error) {
	return admission.Warnings{"create"}, r.err
}
func (r *adapterResource) ValidateUpdate(next runtime.Object) (admission.Warnings, error) {
	r.updated = next
	return admission.Warnings{"update"}, r.err
}
func (r *adapterResource) ValidateDelete() (admission.Warnings, error) {
	return admission.Warnings{"delete"}, r.err
}
func TestWebhookAdapterPreservesResourceSemantics(t *testing.T) {
	ctx := context.Background()
	old := &adapterResource{err: errors.New("validation rejected")}
	next := &adapterResource{}
	d := legacyDefaulterAdapter[*adapterResource]{}
	require.NoError(t, d.Default(ctx, next))
	require.True(t, next.defaulted)
	v := legacyValidatorAdapter[*adapterResource]{}
	warnings, err := v.ValidateCreate(ctx, old)
	require.Equal(t, admission.Warnings{"create"}, warnings)
	require.ErrorIs(t, err, old.err)
	warnings, err = v.ValidateUpdate(ctx, old, next)
	require.Same(t, next, old.updated)
	require.Nil(t, next.updated)
	require.Equal(t, admission.Warnings{"update"}, warnings)
	require.ErrorIs(t, err, old.err)
	warnings, err = v.ValidateDelete(ctx, old)
	require.Equal(t, admission.Warnings{"delete"}, warnings)
	require.ErrorIs(t, err, old.err)
}
