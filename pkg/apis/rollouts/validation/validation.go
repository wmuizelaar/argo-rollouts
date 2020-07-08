package validation

import (
	"encoding/json"
	"fmt"
	"k8s.io/kubernetes/pkg/apis/apps/validation"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	corev1defaults "k8s.io/kubernetes/pkg/apis/core/v1"

	"github.com/argoproj/argo-rollouts/utils/defaults"
	"k8s.io/apimachinery/pkg/util/intstr"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	unversionedvalidation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	validationutil "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/kubernetes/pkg/apis/core"
	apivalidation "k8s.io/kubernetes/pkg/apis/core/validation"

	"github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
)

const (
	// Validate Spec constants

	// MissingFieldMessage the message to indicate rollout is missing a field
	MissingFieldMessage = "Rollout has missing field '%s'"
	// InvalidSetWeightMessage indicates the setweight value needs to be between 0 and 100
	InvalidSetWeightMessage = "SetWeight needs to be between 0 and 100"
	// InvalidDurationMessage indicates the Duration value needs to be greater than 0
	InvalidDurationMessage = "Duration needs to be greater than 0"
	// InvalidMaxSurgeMaxUnavailable indicates both maxSurge and MaxUnavailable can not be set to zero
	InvalidMaxSurgeMaxUnavailable = "MaxSurge and MaxUnavailable both can not be zero"
	// InvalidStepMessage indicates that a step must have either setWeight or pause set
	InvalidStepMessage = "Step must have one of the following set: experiment, setWeight, or pause"
	// InvalidStrategyMessage indiciates that multiple strategies can not be listed
	InvalidStrategyMessage = "Multiple Strategies can not be listed"
	// DuplicatedServicesBlueGreenMessage the message to indicate that the rollout uses the same service for the active and preview services
	DuplicatedServicesBlueGreenMessage = "This rollout uses the same service for the active and preview services, but two different services are required."
	// DuplicatedServicesMessage the message to indicate that the rollout uses the same service for the active and preview services
	DuplicatedServicesCanaryMessage = "This rollout uses the same service for the stable and canary services, but two different services are required."
	// InvalidAntiAffinityStrategyMessage indicates that Anti-Affinity can only have one strategy listed
	InvalidAntiAffinityStrategyMessage = "AntiAffinity must have exactly one strategy listed"
	// InvalidAntiAffinityWeightMessage indicates that Anti-Affinity must have weight between 1-100
	InvalidAntiAffinityWeightMessage = "AntiAffinity weight must be between 1-100"
	// ScaleDownLimitLargerThanRevisionLimit the message to indicate that the rollout's revision history limit can not be smaller than the rollout's scale down limit
	ScaleDownLimitLargerThanRevisionLimit = "This rollout's revision history limit can not be smaller than the rollout's scale down limit"
	// InvalidTrafficRoutingMessage indicates that both canary and stable service must be set to use Traffic Routing
	InvalidTrafficRoutingMessage = "Canary service and Stable service must to be set to use Traffic Routing"
)

func ValidateRollout(rollout *v1alpha1.Rollout) field.ErrorList {
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, ValidateRolloutSpec(rollout, field.NewPath("spec"))...)
	return allErrs
}

// ValidateRolloutSpec checks for a valid spec otherwise returns a list of errors.
func ValidateRolloutSpec(rollout *v1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	spec := rollout.Spec
	allErrs := field.ErrorList{}

	replicas := defaults.GetReplicasOrDefault(spec.Replicas)
	allErrs = append(allErrs, apivalidation.ValidateNonnegativeField(int64(replicas), fldPath.Child("replicas"))...)

	if spec.Selector == nil {
		message := fmt.Sprintf(MissingFieldMessage, ".spec.selector")
		allErrs = append(allErrs, field.Required(fldPath.Child("selector"), message))
	} else {
		allErrs = append(allErrs, unversionedvalidation.ValidateLabelSelector(spec.Selector, fldPath.Child("selector"))...)
		if len(spec.Selector.MatchLabels)+len(spec.Selector.MatchExpressions) == 0 {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("selector"), spec.Selector, "empty selector is invalid for deployment"))
		}
	}

	selector, err := metav1.LabelSelectorAsSelector(spec.Selector)
	if err != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("selector"), spec.Selector, "invalid label selector"))
	} else {
		// The upstream K8s validation we are using expects the default values of a PodSpec to be set otherwise throwing a validation error.
		// However, the Rollout does not need to have them set since the ReplicaSet it creates will have the default values set.
		// As a result, the controller sets the default values before validation to prevent the validation errors due to the lack of these default fields. See #576 for more info.
		podTemplate := corev1.PodTemplate{
			Template: *spec.Template.DeepCopy(),
		}
		corev1defaults.SetObjectDefaults_PodTemplate(&podTemplate)
		templateCoreV1 := podTemplate.Template
		// ValidatePodTemplateSpecForReplicaSet function requires PodTemplateSpec from "k8s.io/api/core".
		// We must cast spec.Template from "k8s.io/api/core/v1" to "k8s.io/api/core" in order to use ValidatePodTemplateSpecForReplicaSet.
		data, structConvertErr := json.Marshal(&templateCoreV1)
		if structConvertErr != nil {
			allErrs = append(allErrs, field.InternalError(fldPath.Child("template"), structConvertErr))
			return allErrs
		}
		var template core.PodTemplateSpec
		structConvertErr = json.Unmarshal(data, &template)
		if structConvertErr != nil {
			allErrs = append(allErrs, field.InternalError(fldPath.Child("template"), structConvertErr))
			return allErrs
		}
		template.ObjectMeta = spec.Template.ObjectMeta
		allErrs = append(allErrs, validation.ValidatePodTemplateSpecForReplicaSet(&template, selector, replicas, fldPath.Child("template"))...)
	}

	allErrs = append(allErrs, apivalidation.ValidateNonnegativeField(int64(spec.MinReadySeconds), fldPath.Child("minReadySeconds"))...)

	revisionHistoryLimit := defaults.GetRevisionHistoryLimitOrDefault(rollout)
	allErrs = append(allErrs, apivalidation.ValidateNonnegativeField(int64(revisionHistoryLimit), fldPath.Child("revisionHistoryLimit"))...)

	progressDeadlineSeconds := defaults.GetProgressDeadlineSecondsOrDefault(rollout)
	allErrs = append(allErrs, apivalidation.ValidateNonnegativeField(int64(progressDeadlineSeconds), fldPath.Child("progressDeadlineSeconds"))...)
	if progressDeadlineSeconds <= spec.MinReadySeconds {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("progressDeadlineSeconds"), progressDeadlineSeconds, "must be greater than minReadySeconds"))
	}

	allErrs = append(allErrs, ValidateRolloutStrategy(rollout, fldPath.Child("strategy"))...)

	return allErrs
}

func ValidateRolloutStrategy(rollout *v1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	strategy := rollout.Spec.Strategy
	allErrs := field.ErrorList{}
	if strategy.BlueGreen == nil && strategy.Canary == nil {
		message := fmt.Sprintf(MissingFieldMessage, ".spec.strategy.canary or .spec.strategy.blueGreen")
		allErrs = append(allErrs, field.Invalid(fldPath.Child("strategy"), rollout.Spec.Strategy, message))
	} else if strategy.BlueGreen != nil && strategy.Canary != nil {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("strategy"), rollout.Spec.Strategy, InvalidStrategyMessage))
	} else if strategy.BlueGreen != nil {
		allErrs = append(allErrs, ValidateRolloutStrategyBlueGreen(rollout, fldPath)...)
	} else if strategy.Canary != nil {
		allErrs = append(allErrs, ValidateRolloutStrategyCanary(rollout, fldPath)...)
	}
	return allErrs
}

func ValidateRolloutStrategyBlueGreen(rollout *v1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	blueGreen := rollout.Spec.Strategy.BlueGreen
	allErrs := field.ErrorList{}
	if blueGreen.ActiveService == blueGreen.PreviewService {
		allErrs = append(allErrs, field.Duplicate(fldPath.Child("previewService"), DuplicatedServicesBlueGreenMessage))
	}
	revisionHistoryLimit := defaults.GetRevisionHistoryLimitOrDefault(rollout)
	if blueGreen.ScaleDownDelayRevisionLimit != nil && revisionHistoryLimit < *blueGreen.ScaleDownDelayRevisionLimit {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("scaleDownDelayRevisionLimit"), blueGreen.ScaleDownDelayRevisionLimit, ScaleDownLimitLargerThanRevisionLimit))
	}
	allErrs = append(allErrs, ValidateRolloutStrategyAntiAffinity(blueGreen.AntiAffinity, fldPath.Child("antiAffinity"))...)
	return allErrs
}

func ValidateRolloutStrategyCanary(rollout *v1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	canary := rollout.Spec.Strategy.Canary
	allErrs := field.ErrorList{}
	allErrs = append(allErrs, invalidMaxSurgeMaxUnavailable(rollout, fldPath.Child("maxSurge"))...)
	if canary.CanaryService != "" && canary.StableService != "" && canary.CanaryService == canary.StableService {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("stableService"), canary.StableService, DuplicatedServicesCanaryMessage))
	}
	if canary.TrafficRouting != nil && (canary.StableService == "" || canary.CanaryService == "") {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("trafficRouting"), canary.TrafficRouting, InvalidTrafficRoutingMessage))
	}
	for i, step := range canary.Steps {
		stepFldPath := fldPath.Child("steps").Index(i)
		allErrs = append(allErrs, hasMultipleStepsType(step, stepFldPath)...)
		if step.Experiment == nil && step.Pause == nil && step.SetWeight == nil && step.Analysis == nil {
			allErrs = append(allErrs, field.Invalid(stepFldPath, canary.Steps[i], InvalidStepMessage))
		}
		if step.SetWeight != nil && (*step.SetWeight < 0 || *step.SetWeight > 100) {
			allErrs = append(allErrs, field.Invalid(stepFldPath.Child("setWeight"), canary.Steps[i].SetWeight, InvalidSetWeightMessage))
		}
		if step.Pause != nil && step.Pause.DurationSeconds() < 0 {
			allErrs = append(allErrs, field.Invalid(stepFldPath.Child("pause").Child("duration"), canary.Steps[i].Pause.Duration, InvalidDurationMessage))
		}
	}
	allErrs = append(allErrs, ValidateRolloutStrategyAntiAffinity(canary.AntiAffinity, fldPath.Child("antiAffinity"))...)
	return allErrs
}

func ValidateRolloutStrategyAntiAffinity(antiAffinity *v1alpha1.AntiAffinity, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	if antiAffinity != nil {
		preferred, required := antiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, antiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
		if (preferred == nil && required == nil) || (preferred != nil && required != nil) {
			allErrs = append(allErrs, field.Invalid(fldPath, antiAffinity, InvalidAntiAffinityStrategyMessage))
		}
		if preferred != nil && (preferred.Weight < 1 || preferred.Weight > 100) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("weight"), preferred.Weight, InvalidAntiAffinityWeightMessage))
		}
	}
	return allErrs
}

func invalidMaxSurgeMaxUnavailable(rollout *v1alpha1.Rollout, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	maxSurge := defaults.GetMaxSurgeOrDefault(rollout)
	maxUnavailable := defaults.GetMaxUnavailableOrDefault(rollout)
	maxSurgeValue := getIntOrPercentValue(*maxSurge)
	maxUnavailableValue := getIntOrPercentValue(*maxUnavailable)
	if maxSurgeValue == 0 && maxUnavailableValue == 0 {
		allErrs = append(allErrs, field.Invalid(fldPath, rollout.Spec.Strategy.Canary.MaxSurge, InvalidMaxSurgeMaxUnavailable))
	}
	return allErrs
}

func getPercentValue(intOrStringValue intstr.IntOrString) (int, bool) {
	if intOrStringValue.Type != intstr.String {
		return 0, false
	}
	if len(validationutil.IsValidPercent(intOrStringValue.StrVal)) != 0 {
		return 0, false
	}
	value, _ := strconv.Atoi(intOrStringValue.StrVal[:len(intOrStringValue.StrVal)-1])
	return value, true
}

func getIntOrPercentValue(intOrStringValue intstr.IntOrString) int {
	value, isPercent := getPercentValue(intOrStringValue)
	if isPercent {
		return value
	}
	return intOrStringValue.IntValue()
}

func hasMultipleStepsType(s v1alpha1.CanaryStep, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	oneOf := make([]bool, 3)
	oneOf = append(oneOf, s.SetWeight != nil)
	oneOf = append(oneOf, s.Pause != nil)
	oneOf = append(oneOf, s.Experiment != nil)
	oneOf = append(oneOf, s.Analysis != nil)
	hasMultipleStepTypes := false
	for i := range oneOf {
		if oneOf[i] {
			if hasMultipleStepTypes {
				allErrs = append(allErrs, field.Invalid(fldPath, s, InvalidStepMessage))
				break
			}
			hasMultipleStepTypes = true
		}
	}
	return allErrs
}