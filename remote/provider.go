package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type remoteClient struct {
	LambdaArn string
	Svc       *lambda.Lambda
}

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"lambda_arn": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("REMOTE_LAMBDA_ARN", nil),
				Description: "ARN de la función Lambda que maneja el ciclo de vida.",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"remote_resource": resourceRemote(),
		},
		ConfigureContextFunc: configureProvider,
	}
}

func configureProvider(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	sess := session.Must(session.NewSession())
	return &remoteClient{
		LambdaArn: d.Get("lambda_arn").(string),
		Svc:       lambda.New(sess),
	}, nil
}

type lambdaPayload struct {
	Action   string                 `json:"phase"`
	Args     map[string]interface{} `json:"args"`
	State    map[string]interface{} `json:"state,omitempty"`
	Planning bool                   `json:"planning,omitempty"`
}

func resourceRemote() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceRemoteCreate,
		ReadContext:   resourceRemoteRead,
		UpdateContext: resourceRemoteUpdate,
		DeleteContext: resourceRemoteDelete,

		Schema: map[string]*schema.Schema{
			"id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"args": {
				Type:     schema.TypeMap,
				Required: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"result": {
				Type:     schema.TypeMap,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
		},
	}
}

type lambdaResponse struct {
	ID     string                 `json:"id"`
	Result map[string]interface{} `json:"result"`
}

func invokeLambda(client *remoteClient, payload lambdaPayload) (*lambdaResponse, error) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	log.Printf("[INFO] invoking Lambda %s with payload: %s", client.LambdaArn, string(bytes))
	resp, err := client.Svc.Invoke(&lambda.InvokeInput{
		FunctionName: aws.String(client.LambdaArn),
		Payload:      bytes,
	})
	if err != nil {
		log.Printf("[ERROR] lambda invocation failed: %v", err)
		return nil, err
	}
	if resp.FunctionError != nil {
		log.Printf("[ERROR] lambda returned function error: %s", string(resp.Payload))
		return nil, fmt.Errorf("lambda error: %s", string(resp.Payload))
	}
	log.Printf("[INFO] lambda response: %s", string(resp.Payload))

	var out map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		log.Printf("[ERROR] failed to unmarshal lambda response: %v", err)
		return nil, err
	}

	id, ok := out["id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("lambda response missing required 'id' field")
	}

	result := &lambdaResponse{
		ID:     id,
		Result: out,
	}
	return result, nil
}

func resourceRemoteCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	args := d.Get("args").(map[string]interface{})
	state := dToMap(d)
	res, err := invokeLambda(client, lambdaPayload{Action: "create", Args: args, State: state, Planning: isPlanning()})
	if err != nil {
		return diag.FromErr(err)
	}
	d.SetId(res.ID)
	d.Set("result", res.Result)
	return nil
}

func resourceRemoteRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	args := d.Get("args").(map[string]interface{})
	state := dToMap(d)
	res, err := invokeLambda(client, lambdaPayload{Action: "read", Args: args, State: state, Planning: isPlanning()})
	if err != nil {
		log.Printf("[ERROR] remote read failed: %v", err)
		return diag.FromErr(fmt.Errorf("remote read failed: %w", err))
	}

	if res.ID == "" {
		log.Printf("[INFO] remote resource no longer exists, clearing ID")
		d.SetId("")
		return nil
	}

	d.SetId(res.ID)
	d.Set("result", res.Result)
	return nil
}

func resourceRemoteUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	args := d.Get("args").(map[string]interface{})
	state := dToMap(d)
	res, err := invokeLambda(client, lambdaPayload{Action: "update", Args: args, State: state, Planning: isPlanning()})
	if err != nil {
		return diag.FromErr(err)
	}
	d.Set("result", res.Result)
	return nil
}

func resourceRemoteDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	args := d.Get("args").(map[string]interface{})
	state := dToMap(d)
	_, err := invokeLambda(client, lambdaPayload{Action: "delete", Args: args, State: state, Planning: isPlanning()})
	if err != nil {
		return diag.FromErr(err)
	}
	d.SetId("")
	return nil
}

func dToMap(d *schema.ResourceData) map[string]interface{} {
	out := make(map[string]interface{})
	for k, v := range d.State().Attributes {
		out[k] = v
	}
	return out
}

func isPlanning() bool {
	return os.Getenv("TF_LOG") == "TRACE" && os.Getenv("TF_IN_AUTOMATION") == "1"
}
