package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
	"github.com/unlimitechcloud/terraform-provider-remote/remote"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderFunc: remote.Provider,
	})
}
