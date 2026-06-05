package controller

import (
	"fmt"
	"net"

	kapi "k8s.io/api/core/v1"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/watch"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/router/pkg/router"
	"github.com/openshift/router/pkg/router/routeapihelpers"
)

// ExtendedValidator implements the router.Plugin interface to provide
// extended config validation for template based, backend-agnostic routers.
type ExtendedValidator struct {
	// plugin is the next plugin in the chain.
	plugin router.Plugin

	// recorder is an interface for indicating route status.
	recorder RouteStatusRecorder

	// endpointAddressValidation enables endpoint address SSRF checks.
	endpointAddressValidation bool
}

// NewExtendedValidator creates a plugin wrapper that ensures only routes and
// endpoints that pass validation are relayed to the next plugin in the chain.
// Recorder is an interface for indicating route status updates.
func NewExtendedValidator(plugin router.Plugin, recorder RouteStatusRecorder, endpointAddressValidation bool) *ExtendedValidator {
	return &ExtendedValidator{
		plugin:                    plugin,
		recorder:                  recorder,
		endpointAddressValidation: endpointAddressValidation,
	}
}

// HandleNode processes watch events on the node resource
func (p *ExtendedValidator) HandleNode(eventType watch.EventType, node *kapi.Node) error {
	return p.plugin.HandleNode(eventType, node)
}

// HandleEndpoints processes watch events on the Endpoints resource.
func (p *ExtendedValidator) HandleEndpoints(eventType watch.EventType, endpoints *kapi.Endpoints) error {
	if p.endpointAddressValidation {
		for _, subset := range endpoints.Subsets {
			for _, addr := range subset.Addresses {
				if err := validateEndpointAddress(addr.IP); err != nil {
					log.Error(err, "skipping endpoints due to invalid configuration", "endpoints", fmt.Sprintf("%s/%s", endpoints.Namespace, endpoints.Name))
					return fmt.Errorf("invalid endpoint configuration: %v", err)
				}
			}
			for _, addr := range subset.NotReadyAddresses {
				if err := validateEndpointAddress(addr.IP); err != nil {
					log.Error(err, "skipping endpoints due to invalid configuration", "endpoints", fmt.Sprintf("%s/%s", endpoints.Namespace, endpoints.Name))
					return fmt.Errorf("invalid endpoint configuration: %v", err)
				}
			}
		}
	}
	return p.plugin.HandleEndpoints(eventType, endpoints)
}

func validateEndpointAddress(address string) error {
	ip := net.ParseIP(address)
	if ip == nil {
		return fmt.Errorf("address %q is not a valid IP", address)
	}
	return checkRestrictedIP(ip)
}

var (
	azureMetadata = net.ParseIP("168.63.129.16")
)

func checkRestrictedIP(ip net.IP) error {
	if ip.IsUnspecified() {
		return fmt.Errorf("IP address %s is a restricted unspecified IP", ip.String())
	}
	if ip.IsLoopback() {
		return fmt.Errorf("IP address %s is a restricted loopback IP", ip.String())
	}
	if ip.IsLinkLocalUnicast() {
		return fmt.Errorf("IP address %s is a restricted link-local IP", ip.String())
	}
	if ip.Equal(azureMetadata) {
		return fmt.Errorf("IP address %s is a restricted cloud metadata IP", ip.String())
	}
	return nil
}

// HandleRoute processes watch events on the Route resource.
func (p *ExtendedValidator) HandleRoute(eventType watch.EventType, route *routev1.Route) error {
	log.V(10).Info("HandleRoute: ExtendedValidator")
	routeName := routeNameKey(route)
	if err := routeapihelpers.ExtendedValidateRoute(route).ToAggregate(); err != nil {
		log.Error(err, "skipping route due to invalid configuration", "route", routeName)

		p.recorder.RecordRouteRejection(route, "ExtendedValidationFailed", err.Error())
		p.plugin.HandleRoute(watch.Deleted, route)
		return fmt.Errorf("invalid route configuration")
	}

	return p.plugin.HandleRoute(eventType, route)
}

// HandleNamespaces limits the scope of valid routes to only those that match
// the provided namespace list.
func (p *ExtendedValidator) HandleNamespaces(namespaces sets.String) error {
	return p.plugin.HandleNamespaces(namespaces)
}

func (p *ExtendedValidator) Commit() error {
	return p.plugin.Commit()
}
