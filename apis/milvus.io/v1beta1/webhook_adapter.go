package v1beta1

import (
	"context"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

// Preserve the resource methods while adapting to typed admission handlers.
type legacyDefaulter interface {
	runtime.Object
	Default()
}
type legacyValidator interface {
	runtime.Object
	ValidateCreate() (admission.Warnings, error)
	ValidateUpdate(runtime.Object) (admission.Warnings, error)
	ValidateDelete() (admission.Warnings, error)
}
type legacyDefaulterAdapter[T legacyDefaulter] struct{}

func (legacyDefaulterAdapter[T]) Default(_ context.Context, obj T) error { obj.Default(); return nil }

type legacyValidatorAdapter[T legacyValidator] struct{}

func (legacyValidatorAdapter[T]) ValidateCreate(_ context.Context, obj T) (admission.Warnings, error) {
	return obj.ValidateCreate()
}
func (legacyValidatorAdapter[T]) ValidateUpdate(_ context.Context, oldObj, newObj T) (admission.Warnings, error) {
	return oldObj.ValidateUpdate(newObj)
}
func (legacyValidatorAdapter[T]) ValidateDelete(_ context.Context, obj T) (admission.Warnings, error) {
	return obj.ValidateDelete()
}
