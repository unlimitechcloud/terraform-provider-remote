package remote

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/xeipuuv/gojsonschema"
)

type remoteClient struct {
	LambdaName string
	Svc        *lambda.Lambda
	once       sync.Once
	schemas    *lambdaSchemaResponse
	schemaErr  error
}

type lambdaSchemaResponse struct {
	Request  map[string]interface{} `json:"request"`
	Response map[string]interface{} `json:"response"`
}

func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"lambda": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc("REMOTE_LAMBDA", nil),
				Description: "Name or ARN of the Lambda function handling lifecycle.",
			},
			"region": {
				Type:        schema.TypeString,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc("AWS_REGION", nil),
				Description: "AWS region for the Lambda function if using name instead of ARN.",
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"remote_resource": resourceRemote(),
		},
		ConfigureContextFunc: configureProvider,
	}
}

func configureProvider(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	lambdaName := d.Get("lambda").(string)
	region := d.Get("region").(string)

	var sess *session.Session
	if strings.HasPrefix(lambdaName, "arn:") {
		sess = session.Must(session.NewSession())
	} else {
		if region == "" {
			return nil, diag.Errorf("region is required when lambda is not an ARN")
		}
		awsCfg := aws.NewConfig().WithRegion(region)
		sess = session.Must(session.NewSession(awsCfg))
	}

	return &remoteClient{
		LambdaName: lambdaName,
		Svc:        lambda.New(sess),
	}, nil
}

type lambdaPayload struct {
	Action   string                 `json:"action"`
	Args     map[string]interface{} `json:"args"`
	State    map[string]interface{} `json:"state,omitempty"`
	Store    map[string]interface{} `json:"store,omitempty"`
	Planning bool                   `json:"planning,omitempty"`
}

type lambdaResponse struct {
	ID      string                 `json:"id"`
	Result  map[string]interface{} `json:"result"`
	Store   map[string]interface{} `json:"store"`
	Replace bool                   `json:"replace"`
	Reason  string                 `json:"reason"`
}

func (c *remoteClient) getSchemas() (*lambdaSchemaResponse, error) {
	schemaWasFetched := false
	c.once.Do(func() {
		log.Printf("[INFO] Requesting schemas from Lambda for the first time...")
		resp, err := invokeLambda(c, lambdaPayload{Action: "schema"})
		if err != nil {
			c.schemaErr = err
			return
		}
		var schemaResp lambdaSchemaResponse
		if resp.Result != nil {
			resultBytes, _ := json.Marshal(resp.Result)
			_ = json.Unmarshal(resultBytes, &schemaResp)
			log.Printf("[INFO] Received schema response from Lambda:")
			pretty, _ := json.MarshalIndent(resp.Result, "", "  ")
			log.Printf("[INFO] Lambda schema JSON:\n%s", string(pretty))
		} else {
			log.Printf("[INFO] Lambda returned no schema in result field.")
		}
		if len(schemaResp.Request) > 0 {
			log.Printf("[INFO] Request schema loaded from Lambda.")
		} else {
			log.Printf("[INFO] No request schema returned from Lambda.")
		}
		if len(schemaResp.Response) > 0 {
			log.Printf("[INFO] Response schema loaded from Lambda.")
		} else {
			log.Printf("[INFO] No response schema returned from Lambda.")
		}
		c.schemas = &schemaResp
		schemaWasFetched = true
	})
	if !schemaWasFetched {
		log.Printf("[INFO] Using cached schema; not fetching from Lambda again.")
	}
	return c.schemas, c.schemaErr
}

func validateWithSchema(schema map[string]interface{}, doc interface{}, side string) error {
	if schema == nil || len(schema) == 0 {
		log.Printf("[INFO] Skipping schema validation for %s: no schema provided by Lambda.", side)
		return nil
	}
	log.Printf("[INFO] Validating %s with JSON schema...", side)
	schemaLoader := gojsonschema.NewGoLoader(schema)
	docLoader := gojsonschema.NewGoLoader(doc)
	result, err := gojsonschema.Validate(schemaLoader, docLoader)
	if err != nil {
		log.Printf("[ERROR] JSON schema validation error (%s): %v", side, err)
		return fmt.Errorf("jsonschema validation error (%s): %w", side, err)
	}
	if !result.Valid() {
		msg := fmt.Sprintf("%s failed schema validation:\n", side)
		for _, desc := range result.Errors() {
			msg += "- " + desc.String() + "\n"
		}
		log.Printf("[ERROR] %s", msg)
		return fmt.Errorf(msg)
	}
	log.Printf("[INFO] %s passed JSON schema validation.", side)
	return nil
}

// --- Helper: parse JSON string (args) into map ---
func parseArgsJSON(argsInput interface{}) (map[string]interface{}, error) {
	switch arr := argsInput.(type) {
	case []interface{}: // expecting an array of strings
		final := map[string]interface{}{}
		for i, el := range arr {
			s, ok := el.(string)
			if !ok {
				log.Printf("[WARN] Args index %d is not a string: %v", i, el)
				continue
			}
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(s), &m); err != nil {
				log.Printf("[WARN] Failed to parse args[%d]: %v\nInput: %s", i, err, s)
				continue
			}
			deepMerge(final, m)
		}
		return final, nil
	case string:
		// Fallback for old usage: single string
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(arr), &m); err != nil {
			return nil, fmt.Errorf("failed to parse args as JSON object: %w\nInput was:\n%s", err, arr)
		}
		return m, nil
	default:
		return nil, fmt.Errorf("args must be either string or array of strings")
	}
}


// --- Helper for getPreviousArgs: returns args from state as map[string]interface{} ---
func getPreviousArgs(d *schema.ResourceData) map[string]interface{} {
	if d == nil || d.IsNewResource() {
		return map[string]interface{}{}
	}
	v, ok := d.GetOk("args")
	if !ok {
		return map[string]interface{}{}
	}
	argsStr, ok := v.(string)
	if !ok || argsStr == "" {
		return map[string]interface{}{}
	}
	m, err := parseArgsJSON(argsStr)
	if err != nil {
		return map[string]interface{}{}
	}
	return m
}

func resourceRemote() *schema.Resource {
	return &schema.Resource{
		CreateContext: resourceRemoteCreate,
		ReadContext:   resourceRemoteRead,
		UpdateContext: resourceRemoteUpdate,
		DeleteContext: resourceRemoteDelete,
		CustomizeDiff: func(ctx context.Context, d *schema.ResourceDiff, meta interface{}) error {
			client := meta.(*remoteClient)
			argsStr := d.Get("args")
			args, err := parseArgsJSON(argsStr)
			if err != nil {
				return fmt.Errorf("CustomizeDiff: %w", err)
			}
			store := map[string]interface{}{}
			if v, ok := d.GetOk("store"); ok {
				storeStr, ok := v.(string)
				if ok && storeStr != "" {
					_ = json.Unmarshal([]byte(storeStr), &store)
				}
			}
			old, _ := d.GetChange("args")
			oldArgsStr, ok := old.(string)
			var oldArgs map[string]interface{}
			isCreate := !ok || oldArgsStr == "" || oldArgsStr == "{}"
			if !isCreate {
				oldArgs, err = parseArgsJSON(oldArgsStr)
				if err != nil {
					return fmt.Errorf("CustomizeDiff: old args not valid JSON: %w", err)
				}
			} else {
				oldArgs = map[string]interface{}{}
			}
			if isCreate {
				log.Printf("[INFO] Skipping diff call to Lambda: this is a create operation (no previous state).")
				return nil
			}
			// Only call Lambda for diff if there is a previous state (i.e., not create)
			res, err := invokeLambda(client, lambdaPayload{
				Action: "diff",
				Args:   args,
				State:  oldArgs,
				Store:  store,
			})
			if err != nil {
				return err
			}
			if res.Replace {
				log.Printf("[INFO] Lambda requested replace: %s", res.Reason)
				if err := d.ForceNew("args"); err != nil {
					return fmt.Errorf("failed to mark 'args' for replacement: %w", err)
				}
			}
			return nil
		},
		Schema: map[string]*schema.Schema{
			"id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"args": {
				Type:     schema.TypeList,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Required: true,
			},
			"result": {
				Type:     schema.TypeMap,
				Computed: true,
				// Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"store": {
				Type:     schema.TypeString,
				Computed: true,
			},
		},
	}
}

func invokeLambda(client *remoteClient, payload lambdaPayload) (*lambdaResponse, error) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	log.Printf("[INFO] invoking Lambda %s with payload: %s", client.LambdaName, string(bytes))
	resp, err := client.Svc.Invoke(&lambda.InvokeInput{
		FunctionName: aws.String(client.LambdaName),
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

	// --- BEGIN: More robust result parsing & debug ---
	var resultVal map[string]interface{}

	// Print type and value of out["result"] for debugging
	if res, ok := out["result"]; ok {
		log.Printf("[DEBUG] Raw Go type for out[\"result\"]: %T", res)
		b, _ := json.MarshalIndent(res, "", "  ")
		log.Printf("[DEBUG] Raw value for out[\"result\"]: %s", string(b))

		switch v := res.(type) {
		case map[string]interface{}:
			resultVal = v
		case string:
			// If it is a string, try to unmarshal it
			if err := json.Unmarshal([]byte(v), &resultVal); err != nil {
				log.Printf("[ERROR] Could not unmarshal result string: %v", err)
				resultVal = map[string]interface{}{}
			}
		default:
			log.Printf("[ERROR] Unexpected result type: %T", v)
			resultVal = map[string]interface{}{}
		}
	} else {
		resultVal = map[string]interface{}{}
	}

	// Print the final parsed resultVal as pretty JSON for debugging
	b, _ := json.MarshalIndent(resultVal, "", "  ")
	log.Printf("[DEBUG] Parsed resultVal to be set: %s", string(b))
	// --- END: More robust result parsing & debug ---

	var storeVal map[string]interface{}
	if store, ok := out["store"]; ok {
		switch v := store.(type) {
		case map[string]interface{}:
			storeVal = v
		case string:
			_ = json.Unmarshal([]byte(v), &storeVal)
		default:
			storeVal = map[string]interface{}{}
		}
	}

	replace, _ := out["replace"].(bool)
	reason, _ := out["reason"].(string)

	id := ""
	if resultVal != nil {
		if idRaw, ok := resultVal["id"]; ok {
			id, _ = idRaw.(string)
		}
	}

	return &lambdaResponse{
		ID:      id,
		Result:  resultVal,
		Store:   storeVal,
		Replace: replace,
		Reason:  reason,
	}, nil
}

func setStoreAsJSONString(d *schema.ResourceData, store map[string]interface{}) error {
	if store == nil {
		return d.Set("store", "")
	}
	bytes, err := json.Marshal(store)
	if err != nil {
		return fmt.Errorf("could not marshal store as JSON: %w", err)
	}
	return d.Set("store", string(bytes))
}

func resourceRemoteCreate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	argsStr := d.Get("args")
	args, err := parseArgsJSON(argsStr)
	if err != nil {
		return diag.FromErr(fmt.Errorf("create: %w", err))
	}
	store := map[string]interface{}{}
	if v, ok := d.GetOk("store"); ok {
		storeStr, ok := v.(string)
		if ok && storeStr != "" {
			_ = json.Unmarshal([]byte(storeStr), &store)
		}
	}
	state := map[string]interface{}{} // No previous state on create

	// Validate args against schema.request
	schemas, err := client.getSchemas()
	if err != nil {
		return diag.FromErr(err)
	}
	if err := validateWithSchema(schemas.Request, args, "request"); err != nil {
		return diag.FromErr(err)
	}

	res, err := invokeLambda(client, lambdaPayload{Action: "create", Args: args, State: state, Store: store, Planning: isPlanning()})
	if err != nil {
		return diag.FromErr(err)
	}
	if res.ID == "" {
		return diag.FromErr(fmt.Errorf("lambda create response missing required 'id' field or returned empty id"))
	}
	// Validate result against schema.response
	if err := validateWithSchema(schemas.Response, res.Result, "response"); err != nil {
		return diag.FromErr(err)
	}
	d.SetId(res.ID)
	d.Set("result", mapStringValues(res.Result))
	if err := setStoreAsJSONString(d, res.Store); err != nil {
		return diag.FromErr(err)
	}
	return nil
}

func resourceRemoteRead(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	argsStr := d.Get("args")
	args, err := parseArgsJSON(argsStr)
	if err != nil {
		return diag.FromErr(fmt.Errorf("read: %w", err))
	}
	store := map[string]interface{}{}
	if v, ok := d.GetOk("store"); ok {
		storeStr, ok := v.(string)
		if ok && storeStr != "" {
			_ = json.Unmarshal([]byte(storeStr), &store)
		}
	}
	state := getPreviousArgs(d)

	// Validate args against schema.request
	schemas, err := client.getSchemas()
	if err != nil {
		return diag.FromErr(err)
	}
	if err := validateWithSchema(schemas.Request, args, "request"); err != nil {
		return diag.FromErr(err)
	}

	res, err := invokeLambda(client, lambdaPayload{Action: "read", Args: args, State: state, Store: store, Planning: isPlanning()})
	if err != nil {
		log.Printf("[ERROR] remote read failed: %v", err)
		return diag.FromErr(fmt.Errorf("remote read failed: %w", err))
	}

	if res.ID == "" {
		log.Printf("[INFO] remote resource no longer exists, clearing ID")
		d.SetId("")
		return nil
	}
	// Validate result against schema.response
	if err := validateWithSchema(schemas.Response, res.Result, "response"); err != nil {
		return diag.FromErr(err)
	}

	d.SetId(res.ID)
	d.Set("result", mapStringValues(res.Result))
	if err := setStoreAsJSONString(d, res.Store); err != nil {
		return diag.FromErr(err)
	}
	return nil
}

func resourceRemoteUpdate(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	argsStr := d.Get("args")
	args, err := parseArgsJSON(argsStr)
	if err != nil {
		return diag.FromErr(fmt.Errorf("update: %w", err))
	}
	store := map[string]interface{}{}
	if v, ok := d.GetOk("store"); ok {
		storeStr, ok := v.(string)
		if ok && storeStr != "" {
			_ = json.Unmarshal([]byte(storeStr), &store)
		}
	}
	state := getPreviousArgs(d)

	// Validate args against schema.request
	schemas, err := client.getSchemas()
	if err != nil {
		return diag.FromErr(err)
	}
	if err := validateWithSchema(schemas.Request, args, "request"); err != nil {
		return diag.FromErr(err)
	}

	// Invoke Lambda, don't mutate state yet
	res, err := invokeLambda(client, lambdaPayload{
		Action:   "update",
		Args:     args,
		State:    state,
		Store:    store,
		Planning: isPlanning(),
	})
	if err != nil {
		return diag.FromErr(err)
	}
	if res.ID == "" {
		return diag.FromErr(fmt.Errorf("lambda update response missing required 'id' field or returned empty id"))
	}
	// Validate result against schema.response
	if err := validateWithSchema(schemas.Response, res.Result, "response"); err != nil {
		return diag.FromErr(err)
	}

	// *** Only now: mutate state ***
	// Use SetId first if needed
	d.SetId(res.ID)

	// You may want to check error for d.Set as well, in case of a bug with map types
	if err := d.Set("result", mapStringValues(res.Result)); err != nil {
		return diag.FromErr(fmt.Errorf("failed to set result: %w", err))
	}
	if err := setStoreAsJSONString(d, res.Store); err != nil {
		return diag.FromErr(err)
	}
	return nil
}


func resourceRemoteDelete(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
	client := m.(*remoteClient)
	argsStr := d.Get("args")
	args, err := parseArgsJSON(argsStr)
	if err != nil {
		return diag.FromErr(fmt.Errorf("delete: %w", err))
	}
	store := map[string]interface{}{}
	if v, ok := d.GetOk("store"); ok {
		storeStr, ok := v.(string)
		if ok && storeStr != "" {
			_ = json.Unmarshal([]byte(storeStr), &store)
		}
	}
	state := getPreviousArgs(d)

	// Validate args against schema.request (do NOT validate result)
	schemas, err := client.getSchemas()
	if err != nil {
		return diag.FromErr(err)
	}
	if err := validateWithSchema(schemas.Request, args, "request"); err != nil {
		return diag.FromErr(err)
	}

	res, err := invokeLambda(client, lambdaPayload{Action: "delete", Args: args, State: state, Store: store, Planning: isPlanning()})
	if err != nil {
		return diag.FromErr(err)
	}
	if res.ID == "" {
		d.SetId("")
		return nil
	}
	// Do NOT validate response after delete
	d.SetId(res.ID)
	d.Set("result", mapStringValues(res.Result))
	if err := setStoreAsJSONString(d, res.Store); err != nil {
		return diag.FromErr(err)
	}
	return nil
}

// Helper to flatten map[string]interface{} for result
func flattenMapValues(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(input))
	for k, v := range input {
		switch v.(type) {
		case map[string]interface{}, []interface{}:
			encoded, err := json.Marshal(v)
			if err == nil {
				out[k] = string(encoded)
			} else {
				out[k] = fmt.Sprintf("%v", v)
			 }
		default:
			out[k] = v
		}
	}
	return out
}

func isPlanning() bool {
	return os.Getenv("TF_LOG") == "TRACE" && os.Getenv("TF_IN_AUTOMATION") == "1"
}

func mapStringValues(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(input))
	for k, v := range input {
		switch val := v.(type) {
		case string:
			out[k] = val
		default:
			// Numbers, bools, etc, convert to string
			out[k] = fmt.Sprintf("%v", val)
		}
	}
	return out
}

// Merges src into dst (modifies dst)
func deepMerge(dst, src map[string]interface{}) {
	for k, v := range src {
		if vmap, ok := v.(map[string]interface{}); ok {
			if dmap, ok := dst[k].(map[string]interface{}); ok {
				deepMerge(dmap, vmap)
				continue
			}
		}
		dst[k] = v
	}
}
