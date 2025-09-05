package main

import (
	"fmt"
	"os"

	"github.com/openshift-eng/openshift-tests-extension/pkg/cmd"
	e "github.com/openshift-eng/openshift-tests-extension/pkg/extension"
	g "github.com/openshift-eng/openshift-tests-extension/pkg/ginkgo"

	"github.com/spf13/cobra"

	// The import below is necessary to ensure that the OAS operator tests are registered with the extension.
	_ "github.com/openshift/router/test/e2e"
)

func main() {
	registry := e.NewRegistry()
	ext := e.NewExtension("openshift", "payload", "router")

	// Suite: all (includes everything)
	ext.AddSuite(e.Suite{
		Name: "openshift/router/all",
	})

	specs, err := g.BuildExtensionTestSpecsFromOpenShiftGinkgoSuite()
	if err != nil {
		panic(fmt.Sprintf("couldn't build extension test specs from ginkgo: %+v", err.Error()))
	}

	ext.AddSpecs(specs)
	registry.Register(ext)

	root := &cobra.Command{
		Long: "OpenShift Router Tests Extension",
	}

	root.AddCommand(cmd.DefaultExtensionCommands(registry)...)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
