package main

import (
	"terraform-provider-remote/remote"

	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: remote.Provider,
	})
}
