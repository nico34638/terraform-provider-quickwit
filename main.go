package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/nico34638/terraform-provider-quickwit/internal/provider"
)

// Set by goreleaser.
var version = "dev"

//go:generate go tool tfplugindocs generate --provider-name quickwit --rendered-provider-name Quickwit

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/nico34638/quickwit",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
