package controller

import (
	"testing"

	kapi "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"

	routev1 "github.com/openshift/api/route/v1"
)

type recordingPlugin struct {
	endpointEvents []endpointEvent
}

type endpointEvent struct {
	eventType watch.EventType
	endpoints *kapi.Endpoints
}

func (p *recordingPlugin) HandleRoute(watch.EventType, *routev1.Route) error { return nil }
func (p *recordingPlugin) HandleEndpoints(eventType watch.EventType, endpoints *kapi.Endpoints) error {
	p.endpointEvents = append(p.endpointEvents, endpointEvent{eventType: eventType, endpoints: endpoints})
	return nil
}
func (p *recordingPlugin) HandleNamespaces(sets.String) error           { return nil }
func (p *recordingPlugin) HandleNode(watch.EventType, *kapi.Node) error { return nil }
func (p *recordingPlugin) Commit() error                                { return nil }

func TestHandleEndpointSlice_FQDNFiltering(t *testing.T) {
	ipv4Type := discoveryv1.AddressTypeIPv4
	ipv6Type := discoveryv1.AddressTypeIPv6
	fqdnType := discoveryv1.AddressTypeFQDN

	tests := []struct {
		name                      string
		endpointAddressValidation bool
		items                     []discoveryv1.EndpointSlice
		expectedEventType         watch.EventType
		expectedAddrCount         int
	}{
		{
			name:                      "IPv4 slices pass through with extended validation",
			endpointAddressValidation: true,
			items: []discoveryv1.EndpointSlice{{
				ObjectMeta:  metav1.ObjectMeta{Name: "slice-1", Namespace: "ns"},
				AddressType: ipv4Type,
				Endpoints: []discoveryv1.Endpoint{{
					Addresses: []string{"10.0.0.1"},
				}},
			}},
			expectedEventType: watch.Modified,
			expectedAddrCount: 1,
		},
		{
			name:                      "IPv6 slices pass through with extended validation",
			endpointAddressValidation: true,
			items: []discoveryv1.EndpointSlice{{
				ObjectMeta:  metav1.ObjectMeta{Name: "slice-1", Namespace: "ns"},
				AddressType: ipv6Type,
				Endpoints: []discoveryv1.Endpoint{{
					Addresses: []string{"2001:db8::1"},
				}},
			}},
			expectedEventType: watch.Modified,
			expectedAddrCount: 1,
		},
		{
			name:                      "FQDN slices are filtered out with extended validation",
			endpointAddressValidation: true,
			items: []discoveryv1.EndpointSlice{{
				ObjectMeta:  metav1.ObjectMeta{Name: "slice-1", Namespace: "ns"},
				AddressType: fqdnType,
				Endpoints: []discoveryv1.Endpoint{{
					Addresses: []string{"evil.example.com"},
				}},
			}},
			expectedEventType: watch.Deleted,
			expectedAddrCount: 0,
		},
		{
			name:                      "FQDN slices pass through without extended validation",
			endpointAddressValidation: false,
			items: []discoveryv1.EndpointSlice{{
				ObjectMeta:  metav1.ObjectMeta{Name: "slice-1", Namespace: "ns"},
				AddressType: fqdnType,
				Endpoints: []discoveryv1.Endpoint{{
					Addresses: []string{"service.example.com"},
				}},
			}},
			expectedEventType: watch.Modified,
			expectedAddrCount: 1,
		},
		{
			name:                      "mixed IPv4 and FQDN slices with extended validation keeps only IPv4",
			endpointAddressValidation: true,
			items: []discoveryv1.EndpointSlice{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "slice-ipv4", Namespace: "ns"},
					AddressType: ipv4Type,
					Endpoints: []discoveryv1.Endpoint{{
						Addresses: []string{"10.0.0.1"},
					}},
				},
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "slice-fqdn", Namespace: "ns"},
					AddressType: fqdnType,
					Endpoints: []discoveryv1.Endpoint{{
						Addresses: []string{"evil.example.com"},
					}},
				},
			},
			expectedEventType: watch.Modified,
			expectedAddrCount: 1,
		},
		{
			name:                      "all FQDN slices filtered results in Deleted event",
			endpointAddressValidation: true,
			items: []discoveryv1.EndpointSlice{
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "slice-fqdn-1", Namespace: "ns"},
					AddressType: fqdnType,
					Endpoints: []discoveryv1.Endpoint{{
						Addresses: []string{"a.example.com"},
					}},
				},
				{
					ObjectMeta:  metav1.ObjectMeta{Name: "slice-fqdn-2", Namespace: "ns"},
					AddressType: fqdnType,
					Endpoints: []discoveryv1.Endpoint{{
						Addresses: []string{"b.example.com"},
					}},
				},
			},
			expectedEventType: watch.Deleted,
			expectedAddrCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plugin := &recordingPlugin{}
			rc := &RouterController{
				Plugin:                    plugin,
				EndpointAddressValidation: tc.endpointAddressValidation,
				firstSyncDone:             true,
				FilteredNamespaceNames:    make(sets.String),
				NamespaceRoutes:           make(map[string]map[string]*routev1.Route),
				NamespaceEndpoints:        make(map[string]map[string]*kapi.Endpoints),
			}

			objMeta := metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "ns",
			}

			rc.HandleEndpointSlice(watch.Added, objMeta, tc.items)

			if len(plugin.endpointEvents) != 1 {
				t.Fatalf("expected 1 endpoint event, got %d", len(plugin.endpointEvents))
			}

			event := plugin.endpointEvents[0]
			if event.eventType != tc.expectedEventType {
				t.Errorf("expected event type %q, got %q", tc.expectedEventType, event.eventType)
			}

			totalAddrs := 0
			for _, subset := range event.endpoints.Subsets {
				totalAddrs += len(subset.Addresses)
			}
			if totalAddrs != tc.expectedAddrCount {
				t.Errorf("expected %d addresses, got %d", tc.expectedAddrCount, totalAddrs)
			}
		})
	}
}
