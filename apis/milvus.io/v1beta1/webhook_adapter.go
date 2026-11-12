package v1beta1

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

type webhookResource interface {
	runtime.Object
	Default()
	ValidateCreate() (admission.Warnings, error)
	ValidateUpdate(runtime.Object) (admission.Warnings, error)
	ValidateDelete() (admission.Warnings, error)
}

// resourceWebhook adapts the existing resource rules to typed admission hooks.
type resourceWebhook[T webhookResource] struct{}

func (resourceWebhook[T]) Default(_ context.Context, obj T) error {
	obj.Default()
	return nil
}

func (resourceWebhook[T]) ValidateCreate(_ context.Context, obj T) (admission.Warnings, error) {
	return obj.ValidateCreate()
}

func (resourceWebhook[T]) ValidateUpdate(_ context.Context, oldObj, newObj T) (admission.Warnings, error) {
	return newObj.ValidateUpdate(oldObj)
}

func (resourceWebhook[T]) ValidateDelete(_ context.Context, obj T) (admission.Warnings, error) {
	return obj.ValidateDelete()
}
