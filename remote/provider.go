package remote

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func Provider() *schema.Provider {
	return &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"remote_resource": resourceRemote(),
		},
		Schema: map[string]*schema.Schema{
			"endpoint": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("REMOTE_ENDPOINT", nil),
				Description: "URL base del endpoint remoto (ej: Lambda o API)",
			},
		},
		ConfigureContextFunc: configureProvider,
	}
}
