package controller

import (
	"fmt"
	"net"
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

func TestCheckRestrictedIP(t *testing.T) {
	tests := []struct {
		name        string
		ip          string
		expectError bool
	}{
		{
			name:        "valid public IPv4",
			ip:          "1.2.3.4",
			expectError: false,
		},
		{
			name:        "valid private IPv4",
			ip:          "10.0.0.1",
			expectError: false,
		},
		{
			name:        "loopback IPv4",
			ip:          "127.0.0.1",
			expectError: true,
		},
		{
			name:        "loopback IPv6",
			ip:          "::1",
			expectError: true,
		},
		{
			name:        "link-local IPv4 metadata",
			ip:          "169.254.169.254",
			expectError: true,
		},
		{
			name:        "link-local IPv4 other",
			ip:          "169.254.1.1",
			expectError: true,
		},
		{
			name:        "Azure metadata IP",
			ip:          "168.63.129.16",
			expectError: true,
		},
		{
			name:        "valid IPv6",
			ip:          "2001:db8::1",
			expectError: false,
		},
		{
			name:        "link-local IPv6",
			ip:          "fe80::1",
			expectError: true,
		},
		{
			name:        "unspecified IPv4",
			ip:          "0.0.0.0",
			expectError: true,
		},
		{
			name:        "unspecified IPv6",
			ip:          "::",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("failed to parse IP %q", tc.ip)
			}
			err := checkRestrictedIP(ip)
			if tc.expectError && err == nil {
				t.Errorf("expected error for IP %s, got nil", tc.ip)
			}
			if !tc.expectError && err != nil {
				t.Errorf("expected no error for IP %s, got %v", tc.ip, err)
			}
		})
	}
}

func TestValidateEndpointAddress(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		expectError bool
	}{
		{
			name:        "valid public IPv4",
			address:     "10.0.0.1",
			expectError: false,
		},
		{
			name:        "valid IPv6",
			address:     "2001:db8::1",
			expectError: false,
		},
		{
			name:        "restricted loopback IP",
			address:     "127.0.0.1",
			expectError: true,
		},
		{
			name:        "restricted link-local IP",
			address:     "169.254.169.254",
			expectError: true,
		},
		{
			name:        "restricted Azure metadata IP",
			address:     "168.63.129.16",
			expectError: true,
		},
		{
			name:        "unspecified IPv4",
			address:     "0.0.0.0",
			expectError: true,
		},
		{
			name:        "non-IP address rejected",
			address:     "evil.example.com",
			expectError: true,
		},
		{
			name:        "empty string rejected",
			address:     "",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEndpointAddress(tc.address)
			if tc.expectError && err == nil {
				t.Errorf("expected error for address %q, got nil", tc.address)
			}
			if !tc.expectError && err != nil {
				t.Errorf("expected no error for address %q, got %v", tc.address, err)
			}
		})
	}
}

func TestExtendedValidator_HandleEndpoints(t *testing.T) {
	tests := []struct {
		name          string
		endpoints     *kapi.Endpoints
		expectBlocked bool
	}{
		{
			name: "valid IP in Addresses",
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
			name: "restricted link-local IP in Addresses",
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
			name: "restricted loopback IP in Addresses",
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
			name: "restricted IP in NotReadyAddresses",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						NotReadyAddresses: []kapi.EndpointAddress{{IP: "169.254.169.254"}},
					},
				},
			},
			expectBlocked: true,
		},
		{
			name: "valid IP in NotReadyAddresses",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						NotReadyAddresses: []kapi.EndpointAddress{{IP: "10.0.0.5"}},
					},
				},
			},
			expectBlocked: false,
		},
		{
			name: "mixed valid and restricted across subsets",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						Addresses: []kapi.EndpointAddress{{IP: "10.0.0.1"}},
					},
					{
						Addresses: []kapi.EndpointAddress{{IP: "127.0.0.1"}},
					},
				},
			},
			expectBlocked: true,
		},
		{
			name: "Azure metadata IP",
			endpoints: &kapi.Endpoints{
				Subsets: []kapi.EndpointSubset{
					{
						Addresses: []kapi.EndpointAddress{{IP: "168.63.129.16"}},
					},
				},
			},
			expectBlocked: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inner := &fakeTestPlugin{}
			recorder := &fakeTestRecorder{}
			validator := NewExtendedValidator(inner, recorder, true)

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
