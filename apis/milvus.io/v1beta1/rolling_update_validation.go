package v1beta1

import (
	"reflect"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func (r *Milvus) validateRollingUpdates() field.ErrorList {
	path := field.NewPath("spec", "components")
	allErrs := validateRollingUpdate(r.Spec.Com.RollingUpdate, path.Child("rollingUpdate"))
	// All component types embed ComponentSpec; validate every configured one,
	// including standalone, streamingNode and CDC.
	components := reflect.ValueOf(r.Spec.Com)
	for i := 0; i < components.NumField(); i++ {
		component := components.Field(i)
		if component.Kind() != reflect.Ptr || component.IsNil() || component.Elem().Kind() != reflect.Struct {
			continue
		}
		spec := component.Elem().FieldByName("ComponentSpec")
		if !spec.IsValid() {
			continue
		}
		name := strings.Split(components.Type().Field(i).Tag.Get("json"), ",")[0]
		allErrs = append(allErrs, validateRollingUpdate(spec.Interface().(ComponentSpec).RollingUpdate, path.Child(name, "rollingUpdate"))...)
	}
	return allErrs
}

func validateRollingUpdate(ru *appsv1.RollingUpdateDeployment, path *field.Path) field.ErrorList {
	if ru == nil {
		return nil
	}
	var allErrs field.ErrorList
	for _, item := range []struct {
		name  string
		value *intstr.IntOrString
	}{{"maxSurge", ru.MaxSurge}, {"maxUnavailable", ru.MaxUnavailable}} {
		if item.value == nil {
			continue
		}
		v := item.value
		valid := v.Type == intstr.Int && v.IntVal >= 0 ||
			v.Type == intstr.String && len(validation.IsValidPercent(v.StrVal)) == 0
		n, err := intstr.GetScaledValueFromIntOrPercent(v, 100, false)
		if !valid || err != nil || n < 0 {
			allErrs = append(allErrs, field.Invalid(path.Child(item.name), v, "must be a nonnegative integer or percentage"))
		} else if item.name == "maxUnavailable" && v.Type == intstr.String && n > 100 {
			allErrs = append(allErrs, field.Invalid(path.Child(item.name), v, "must not exceed 100%"))
		}
	}
	if len(allErrs) > 0 {
		return allErrs
	}
	// Match GetDeploymentStrategy defaults for omitted fields.
	surge, unavailable := 1, 0
	if ru.MaxSurge != nil {
		surge, _ = intstr.GetScaledValueFromIntOrPercent(ru.MaxSurge, 100, true)
	}
	if ru.MaxUnavailable != nil {
		unavailable, _ = intstr.GetScaledValueFromIntOrPercent(ru.MaxUnavailable, 100, false)
	}
	if surge == 0 && unavailable == 0 {
		allErrs = append(allErrs, field.Invalid(path, ru, "maxUnavailable must be positive when maxSurge is zero"))
	}
	return allErrs
}
