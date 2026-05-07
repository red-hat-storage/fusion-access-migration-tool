package helpers

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ParseGVR converts a dotted resource string like "localdisks.scale.spectrum.ibm.com"
// into a GroupVersionResource, inferring the API version from the group name.
func ParseGVR(resource string) schema.GroupVersionResource {
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

// ResolveGVR tries multiple API versions for a given group/resource, returning the first that exists.
func ResolveGVR(
	ctx context.Context,
	client dynamic.Interface,
	group string,
	resource string,
	versions []string,
) (schema.GroupVersionResource, error) {
	for _, ver := range versions {
		gvr := schema.GroupVersionResource{Group: group, Version: ver, Resource: resource}
		_, err := client.Resource(gvr).List(ctx, metav1.ListOptions{Limit: 1})
		if err == nil {
			return gvr, nil
		}
		if apierrors.IsNotFound(err) {
			continue
		}
		return schema.GroupVersionResource{}, err
	}
	return schema.GroupVersionResource{}, fmt.Errorf(
		"%s/%s API not found; tried versions %v",
		group, resource, versions,
	)
}
