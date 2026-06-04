package controller

import (
	"fmt"
	"testing"

	kapi "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"

	routev1 "github.com/openshift/api/route/v1"
)

type fakeTestPlugin struct {
	endpoints []*kapi.Endpoints
}

func (p *fakeTestPlugin) HandleRoute(watch.EventType, *routev1.Route) error { return nil }
func (p *fakeTestPlugin) HandleEndpoints(eventType watch.EventType, endpoints *kapi.Endpoints) error {
	p.endpoints = append(p.endpoints, endpoints)
	return nil
}
func (p *fakeTestPlugin) HandleNamespaces(sets.String) error           { return nil }
func (p *fakeTestPlugin) HandleNode(watch.EventType, *kapi.Node) error { return nil }
func (p *fakeTestPlugin) Commit() error                                { return nil }

type fakeTestRecorder struct {
	rejections []string
}

func (r *fakeTestRecorder) RecordRouteRejection(route *routev1.Route, reason, message string) {
	r.rejections = append(r.rejections, fmt.Sprintf("%s: %s", reason, message))
}
func (r *fakeTestRecorder) RecordRouteUpdate(route *routev1.Route, reason, message string) {}
func (r *fakeTestRecorder) RecordRouteUnservableInFutureVersions(route *routev1.Route, reason, message string) {
}
func (r *fakeTestRecorder) RecordRouteUnservableInFutureVersionsClear(route *routev1.Route) {}

func TestExtendedValidator_HandleEndpoints(t *testing.T) {
	tests := []struct {
		name          string
		endpoints     *kapi.Endpoints
		expectBlocked bool
	}{
		{
			name: "valid IP",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						Addresses: []kapi.EndpointAddress{{IP: "1.2.3.4"}},
					},
				},
			},
			expectBlocked: false,
		},
		{
			name: "restricted IP 169.254.169.254",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						Addresses: []kapi.EndpointAddress{{IP: "169.254.169.254"}},
					},
				},
			},
			expectBlocked: true,
		},
		{
			name: "restricted IP 127.0.0.1",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						Addresses: []kapi.EndpointAddress{{IP: "127.0.0.1"}},
					},
				},
			},
			expectBlocked: true,
		},
		{
			name: "FQDN resolving to restricted IP",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						Addresses: []kapi.EndpointAddress{{IP: "169-254-169-254.nip.io"}},
					},
				},
			},
			expectBlocked: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Mock DNS resolution for the test
			// This is a bit tricky since net.LookupIP is global.
			// For the sake of this reproduction, we'll assume the implementation
			// will use a function we can mock or we'll just check if it's currently failing.

			inner := &fakeTestPlugin{}
			recorder := &fakeTestRecorder{}
			validator := NewExtendedValidator(inner, recorder)

			err := validator.HandleEndpoints(watch.Added, tc.endpoints)

			if tc.expectBlocked {
				if err == nil {
					t.Errorf("expected error for blocked endpoints, got nil")
				}
				if len(inner.endpoints) > 0 {
					t.Errorf("expected no endpoints passed to inner plugin, got %d", len(inner.endpoints))
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
				if len(inner.endpoints) == 0 {
					t.Errorf("expected endpoints passed to inner plugin, got 0")
				}
			}
		})
	}
}
