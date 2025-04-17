package v1beta1

import (
	"fmt"

	"golang.org/x/mod/semver"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/zilliztech/milvus-operator/pkg/config"
)

// log is for logging in this package.
var mulog = logf.Log.WithName("milvus-upgrade")

func (r *MilvusUpgrade) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

//+kubebuilder:webhook:path=/mutate-milvus-io-v1beta1-milvusupgrade,mutating=true,failurePolicy=fail,sideEffects=None,groups=milvus.io,resources=milvusupgrades,verbs=create;update,versions=v1beta1,name=mmilvusupgrade.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &MilvusUpgrade{}

const defaultToolImage = "milvusdb/meta-migration:v2.2.0-bugfix-20230112"

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (r *MilvusUpgrade) Default() {
	if r.Spec.TargetImage == "" {
		r.Spec.TargetImage = fmt.Sprintf("%s:v%s", config.DefaultMilvusBaseImage, RemovePrefixV(r.Spec.TargetVersion))
	}
	if r.Spec.ToolImage == "" {
		r.Spec.ToolImage = defaultToolImage
	}
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
//+kubebuilder:webhook:path=/validate-milvus-io-v1beta1-milvusupgrade,mutating=false,failurePolicy=fail,sideEffects=None,groups=milvus.io,resources=milvusupgrades,verbs=create;update,versions=v1beta1,name=vmilvusupgrade.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &MilvusUpgrade{}

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (r *MilvusUpgrade) ValidateCreate() (admission.Warnings, error) {
	return nil, r.Validate()
}

func AddPrefixV(version string) string {
	if version == "" {
		return ""
	}
	if version[0] == 'v' {
		return version
	}
	return "v" + version
}

func RemovePrefixV(version string) string {
	if version == "" {
		return ""
	}
	if version[0] == 'v' {
		return version[1:]
	}
	return version
}

func (r MilvusUpgrade) Validate() error {
	s := &r.Spec
	var allErrs field.ErrorList
	srcVer := AddPrefixV(s.SourceVersion)
	if !semver.IsValid(srcVer) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("sourceVersion"), srcVer, "sourceVersion is not a valid sematic version"))
	}

	if semver.Compare(srcVer, "v2.0.1") < 0 {
		// <2.0.0 or 2.0.0-rcx
		if semver.Compare(srcVer, "v2.0.0") < 0 || semver.Build(srcVer) != "" {
			allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("sourceVersion"), srcVer, "sourceVersion must be greater than 2.0.0"))
		}
	}

	targetVer := AddPrefixV(s.TargetVersion)
	if !semver.IsValid(targetVer) {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("targetVersion"), targetVer, "targetVersion is not a valid sematic version"))
	}
	if semver.Compare(targetVer, "v2.2.0") < 0 {
		allErrs = append(allErrs, field.Invalid(field.NewPath("spec").Child("targetVersion"), targetVer, "targetVersion must be greater than 2.2.0"))
	}
	if len(allErrs) > 0 {
		return apierrors.NewInvalid(schema.GroupKind{Group: GroupVersion.Group, Kind: MilvusUpgradeKind}, r.Name, allErrs)
	}
	return nil
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (r *MilvusUpgrade) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	mulog.Info("validate update", "name", r.Name)
	return nil, r.Validate()
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (r *MilvusUpgrade) ValidateDelete() (admission.Warnings, error) {
	mulog.Info("validate delete", "name", r.Name)
	return nil, nil
}
