package main

import (
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// parseGVR converts a dotted resource string like "localdisks.scale.spectrum.ibm.com"
// into a GroupVersionResource, inferring the API version from the group name.
func parseGVR(resource string) schema.GroupVersionResource {
	parts := strings.Split(resource, ".")
	if len(parts) < 2 {
		return schema.GroupVersionResource{Resource: resource}
	}

	resourceName := parts[0]
	group := strings.Join(parts[1:], ".")
	version := "v1"
	if strings.Contains(group, "scale.spectrum.ibm.com") {
		version = "v1beta1"
	} else if strings.Contains(group, "fusion.storage.openshift.io") {
		version = "v1alpha1"
	}
	return schema.GroupVersionResource{Group: group, Version: version, Resource: resourceName}
}
