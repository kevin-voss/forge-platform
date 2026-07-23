package httpserver_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAPIDeclaresScalingPolicySurface(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../../.."))
	yamlPath := filepath.Join(root, "contracts/openapi/forge-autoscaler.openapi.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Skipf("openapi not in build context: %v", err)
	}
	text := string(raw)
	for _, needle := range []string{
		"/health/live:",
		"/health/ready:",
		"/v1/projects/{project}/environments/{environment}/scalingpolicies:",
		"/v1/projects/{project}/environments/{environment}/scalingpolicies/{name}:",
		"/v1/projects/{project}/environments/{environment}/scalingpolicies/{name}/status:",
		"/v1/projects/{project}/environments/{environment}/scalingpolicies/{name}/override:",
		"/v1/watch/scalingpolicies:",
		"createScalingPolicy",
		"getScalingPolicy",
		"replaceScalingPolicy",
		"patchScalingPolicy",
		"deleteScalingPolicy",
		"replaceScalingPolicyStatus",
		"putScalingPolicyOverride",
		"getScalingPolicyOverride",
		"deleteScalingPolicyOverride",
		"watchScalingPolicies",
		"ScalingPolicy",
		"ScalingPolicySpec",
		"targetRef",
		"minReplicas",
		"maxReplicas",
		"stabilizationWindowSeconds",
		"maxReplicasPerMinute",
		"schedules",
		"endTime",
		"metricOutageFallback",
		"deploymentFreeze",
		"ManualOverride",
		"ADDED",
		"MODIFIED",
		"DELETED",
		"resource_version_conflict",
		"invoice-api-scaling",
		"AbleToScale",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("openapi missing %q", needle)
		}
	}
}
