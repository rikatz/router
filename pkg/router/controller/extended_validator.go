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
}

// NewExtendedValidator creates a plugin wrapper that ensures only routes that
// pass extended validation are relayed to the next plugin in the chain.
// Recorder is an interface for indicating route status updates.
func NewExtendedValidator(plugin router.Plugin, recorder RouteStatusRecorder) *ExtendedValidator {
	return &ExtendedValidator{
		plugin:   plugin,
		recorder: recorder,
	}
}

// HandleNode processes watch events on the node resource
func (p *ExtendedValidator) HandleNode(eventType watch.EventType, node *kapi.Node) error {
	return p.plugin.HandleNode(eventType, node)
}

// HandleEndpoints processes watch events on the Endpoints resource.
func (p *ExtendedValidator) HandleEndpoints(eventType watch.EventType, endpoints *kapi.Endpoints) error {
	for _, subset := range endpoints.Subsets {
		for _, addr := range subset.Addresses {
			if err := validateEndpointAddress(addr.IP); err != nil {
				log.Error(err, "skipping endpoints due to invalid configuration", "endpoints", fmt.Sprintf("%s/%s", endpoints.Namespace, endpoints.Name))
				// We don't have a recordEndpointRejection mechanism, so we log and drop the event.
				// This prevents the router from proxying to the malicious backend.
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
	return p.plugin.HandleEndpoints(eventType, endpoints)
}

func validateEndpointAddress(address string) error {
	ip := net.ParseIP(address)
	if ip != nil {
		return checkRestrictedIP(ip)
	}

	// If not a valid IP, assume it is an FQDN and resolve it
	ips, err := net.LookupIP(address)
	if err != nil {
		// If we can't resolve it, we can't be sure it's safe. However, DNS resolution failures
		// might be transient. For security, failing closed or just logging might be debated.
		// Let's assume if it doesn't resolve right now, we can't validate it.
		// We'll allow transient resolution errors to just pass through, as the router itself
		// would fail to resolve it if it's truly unresolvable. But if it's an SSRF, we MUST block.
		// Actually, HAProxy resolves it at runtime. If we fail to resolve, HAProxy might succeed later.
		// For CVE-2026-42965, the researcher's FQDN resolves. If they use a malicious DNS server that
		// returns a timeout to us but 169.254 to HAProxy, they bypass this.
		// To be safe, if we can't resolve an FQDN, we should probably fail. But let's check
		// what existing validation does. Since there is none, let's just do best-effort or fail closed.
		// For now, let's fail if we can't verify, or maybe just log?
		// Actually, standard practice for SSRF is to fail closed if unresolvable.
		return fmt.Errorf("failed to resolve FQDN %q: %v", address, err)
	}

	for _, resolvedIP := range ips {
		if err := checkRestrictedIP(resolvedIP); err != nil {
			return fmt.Errorf("FQDN %q resolves to restricted IP: %v", address, err)
		}
	}

	return nil
}

func checkRestrictedIP(ip net.IP) error {
	// Block cloud metadata and localhost
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return fmt.Errorf("IP address %s is a restricted cloud metadata IP", ip.String())
	}
	if ip.IsLoopback() {
		return fmt.Errorf("IP address %s is a restricted loopback IP", ip.String())
	}
	return nil
}

// HandleRoute processes watch events on the Route resource.
func (p *ExtendedValidator) HandleRoute(eventType watch.EventType, route *routev1.Route) error {
	log.V(10).Info("HandleRoute: ExtendedValidator")
	// Check if previously seen route and its Spec is unchanged.
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
